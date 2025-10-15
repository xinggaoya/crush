package models

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/v2/help"
	"github.com/charmbracelet/bubbles/v2/key"
	"github.com/charmbracelet/bubbles/v2/spinner"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/xinggaoya/crush/internal/config"
	"github.com/xinggaoya/crush/internal/tui/components/core"
	"github.com/xinggaoya/crush/internal/tui/components/dialogs"
	"github.com/xinggaoya/crush/internal/tui/exp/list"
	"github.com/xinggaoya/crush/internal/tui/styles"
	"github.com/xinggaoya/crush/internal/tui/util"
	"github.com/charmbracelet/lipgloss/v2"
)

const (
	ModelsDialogID dialogs.DialogID = "models"

	defaultWidth = 60
)

const (
	LargeModelType int = iota
	SmallModelType

	largeModelInputPlaceholder = "Choose a model for large, complex tasks"
	smallModelInputPlaceholder = "Choose a model for small, simple tasks"
)

// ModelSelectedMsg is sent when a model is selected
type ModelSelectedMsg struct {
	Model     config.SelectedModel
	ModelType config.SelectedModelType
}

// CloseModelDialogMsg is sent when a model is selected
type CloseModelDialogMsg struct{}

// ModelDialog interface for the model selection dialog
type ModelDialog interface {
	dialogs.DialogModel
}

type ModelOption struct {
	Provider catwalk.Provider
	Model    catwalk.Model
}

type modelDialogCmp struct {
	width   int
	wWidth  int
	wHeight int

	modelList *ModelListComponent
	keyMap    KeyMap
	help      help.Model

	// API key state
	needsAPIKey       bool
	apiKeyInput       *APIKeyInput
	selectedModel     *ModelOption
	selectedModelType config.SelectedModelType
	isAPIKeyValid     bool
	apiKeyValue       string
}

func NewModelDialogCmp() ModelDialog {
	keyMap := DefaultKeyMap()

	listKeyMap := list.DefaultKeyMap()
	listKeyMap.Down.SetEnabled(false)
	listKeyMap.Up.SetEnabled(false)
	listKeyMap.DownOneItem = keyMap.Next
	listKeyMap.UpOneItem = keyMap.Previous

	t := styles.CurrentTheme()
	modelList := NewModelListComponent(listKeyMap, largeModelInputPlaceholder, true)
	apiKeyInput := NewAPIKeyInput()
	apiKeyInput.SetShowTitle(false)
	help := help.New()
	help.Styles = t.S().Help

	return &modelDialogCmp{
		modelList:   modelList,
		apiKeyInput: apiKeyInput,
		width:       defaultWidth,
		keyMap:      DefaultKeyMap(),
		help:        help,
	}
}

func (m *modelDialogCmp) Init() tea.Cmd {
	return tea.Batch(m.modelList.Init(), m.apiKeyInput.Init())
}

func (m *modelDialogCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.wWidth = msg.Width
		m.wHeight = msg.Height
		m.apiKeyInput.SetWidth(m.width - 2)
		m.help.Width = m.width - 2
		return m, m.modelList.SetSize(m.listWidth(), m.listHeight())
	case APIKeyStateChangeMsg:
		u, cmd := m.apiKeyInput.Update(msg)
		m.apiKeyInput = u.(*APIKeyInput)
		return m, cmd
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Select):
			if m.isAPIKeyValid {
				return m, m.saveAPIKeyAndContinue(m.apiKeyValue)
			}
			if m.needsAPIKey {
				// Handle API key submission
				m.apiKeyValue = m.apiKeyInput.Value()
				provider, err := m.getProvider(m.selectedModel.Provider.ID)
				if err != nil || provider == nil {
					return m, util.ReportError(fmt.Errorf("provider %s not found", m.selectedModel.Provider.ID))
				}
				providerConfig := config.ProviderConfig{
					ID:      string(m.selectedModel.Provider.ID),
					Name:    m.selectedModel.Provider.Name,
					APIKey:  m.apiKeyValue,
					Type:    provider.Type,
					BaseURL: provider.APIEndpoint,
				}
				return m, tea.Sequence(
					util.CmdHandler(APIKeyStateChangeMsg{
						State: APIKeyInputStateVerifying,
					}),
					func() tea.Msg {
						start := time.Now()
						err := providerConfig.TestConnection(config.Get().Resolver())
						// intentionally wait for at least 750ms to make sure the user sees the spinner
						elapsed := time.Since(start)
						if elapsed < 750*time.Millisecond {
							time.Sleep(750*time.Millisecond - elapsed)
						}
						if err == nil {
							m.isAPIKeyValid = true
							return APIKeyStateChangeMsg{
								State: APIKeyInputStateVerified,
							}
						}
						return APIKeyStateChangeMsg{
							State: APIKeyInputStateError,
						}
					},
				)
			}
			// Normal model selection
			selectedItem := m.modelList.SelectedModel()

			var modelType config.SelectedModelType
			if m.modelList.GetModelType() == LargeModelType {
				modelType = config.SelectedModelTypeLarge
			} else {
				modelType = config.SelectedModelTypeSmall
			}

			// Check if provider is configured
			if m.isProviderConfigured(string(selectedItem.Provider.ID)) {
				return m, tea.Sequence(
					util.CmdHandler(dialogs.CloseDialogMsg{}),
					util.CmdHandler(ModelSelectedMsg{
						Model: config.SelectedModel{
							Model:           selectedItem.Model.ID,
							Provider:        string(selectedItem.Provider.ID),
							ReasoningEffort: selectedItem.Model.DefaultReasoningEffort,
							MaxTokens:       selectedItem.Model.DefaultMaxTokens,
						},
						ModelType: modelType,
					}),
				)
			} else {
				// Provider not configured, show API key input
				m.needsAPIKey = true
				m.selectedModel = selectedItem
				m.selectedModelType = modelType
				m.apiKeyInput.SetProviderName(selectedItem.Provider.Name)
				return m, nil
			}
		case key.Matches(msg, m.keyMap.Tab):
			if m.needsAPIKey {
				u, cmd := m.apiKeyInput.Update(msg)
				m.apiKeyInput = u.(*APIKeyInput)
				return m, cmd
			}
			if m.modelList.GetModelType() == LargeModelType {
				m.modelList.SetInputPlaceholder(smallModelInputPlaceholder)
				return m, m.modelList.SetModelType(SmallModelType)
			} else {
				m.modelList.SetInputPlaceholder(largeModelInputPlaceholder)
				return m, m.modelList.SetModelType(LargeModelType)
			}
		case key.Matches(msg, m.keyMap.Close):
			if m.needsAPIKey {
				if m.isAPIKeyValid {
					return m, nil
				}
				// Go back to model selection
				m.needsAPIKey = false
				m.selectedModel = nil
				m.isAPIKeyValid = false
				m.apiKeyValue = ""
				m.apiKeyInput.Reset()
				return m, nil
			}
			return m, util.CmdHandler(dialogs.CloseDialogMsg{})
		default:
			if m.needsAPIKey {
				u, cmd := m.apiKeyInput.Update(msg)
				m.apiKeyInput = u.(*APIKeyInput)
				return m, cmd
			} else {
				u, cmd := m.modelList.Update(msg)
				m.modelList = u
				return m, cmd
			}
		}
	case tea.PasteMsg:
		if m.needsAPIKey {
			u, cmd := m.apiKeyInput.Update(msg)
			m.apiKeyInput = u.(*APIKeyInput)
			return m, cmd
		} else {
			var cmd tea.Cmd
			m.modelList, cmd = m.modelList.Update(msg)
			return m, cmd
		}
	case spinner.TickMsg:
		u, cmd := m.apiKeyInput.Update(msg)
		m.apiKeyInput = u.(*APIKeyInput)
		return m, cmd
	}
	return m, nil
}

func (m *modelDialogCmp) View() string {
	t := styles.CurrentTheme()

	if m.needsAPIKey {
		// Show API key input
		m.keyMap.isAPIKeyHelp = true
		m.keyMap.isAPIKeyValid = m.isAPIKeyValid
		apiKeyView := m.apiKeyInput.View()
		apiKeyView = t.S().Base.Width(m.width - 3).Height(lipgloss.Height(apiKeyView)).PaddingLeft(1).Render(apiKeyView)
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			t.S().Base.Padding(0, 1, 1, 1).Render(core.Title(m.apiKeyInput.GetTitle(), m.width-4)),
			apiKeyView,
			"",
			t.S().Base.Width(m.width-2).PaddingLeft(1).AlignHorizontal(lipgloss.Left).Render(m.help.View(m.keyMap)),
		)
		return m.style().Render(content)
	}

	// Show model selection
	listView := m.modelList.View()
	radio := m.modelTypeRadio()
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		t.S().Base.Padding(0, 1, 1, 1).Render(core.Title("Switch Model", m.width-lipgloss.Width(radio)-5)+" "+radio),
		listView,
		"",
		t.S().Base.Width(m.width-2).PaddingLeft(1).AlignHorizontal(lipgloss.Left).Render(m.help.View(m.keyMap)),
	)
	return m.style().Render(content)
}

func (m *modelDialogCmp) Cursor() *tea.Cursor {
	if m.needsAPIKey {
		cursor := m.apiKeyInput.Cursor()
		if cursor != nil {
			cursor = m.moveCursor(cursor)
			return cursor
		}
	} else {
		cursor := m.modelList.Cursor()
		if cursor != nil {
			cursor = m.moveCursor(cursor)
			return cursor
		}
	}
	return nil
}

func (m *modelDialogCmp) style() lipgloss.Style {
	t := styles.CurrentTheme()
	return t.S().Base.
		Width(m.width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocus)
}

func (m *modelDialogCmp) listWidth() int {
	return m.width - 2
}

func (m *modelDialogCmp) listHeight() int {
	return m.wHeight / 2
}

func (m *modelDialogCmp) Position() (int, int) {
	row := m.wHeight/4 - 2 // just a bit above the center
	col := m.wWidth / 2
	col -= m.width / 2
	return row, col
}

func (m *modelDialogCmp) moveCursor(cursor *tea.Cursor) *tea.Cursor {
	row, col := m.Position()
	if m.needsAPIKey {
		offset := row + 3 // Border + title + API key input offset
		cursor.Y += offset
		cursor.X = cursor.X + col + 2
	} else {
		offset := row + 3 // Border + title
		cursor.Y += offset
		cursor.X = cursor.X + col + 2
	}
	return cursor
}

func (m *modelDialogCmp) ID() dialogs.DialogID {
	return ModelsDialogID
}

func (m *modelDialogCmp) modelTypeRadio() string {
	t := styles.CurrentTheme()
	choices := []string{"Large Task", "Small Task"}
	iconSelected := "◉"
	iconUnselected := "○"
	if m.modelList.GetModelType() == LargeModelType {
		return t.S().Base.Foreground(t.FgHalfMuted).Render(iconSelected + " " + choices[0] + "  " + iconUnselected + " " + choices[1])
	}
	return t.S().Base.Foreground(t.FgHalfMuted).Render(iconUnselected + " " + choices[0] + "  " + iconSelected + " " + choices[1])
}

func (m *modelDialogCmp) isProviderConfigured(providerID string) bool {
	cfg := config.Get()
	if _, ok := cfg.Providers.Get(providerID); ok {
		return true
	}
	return false
}

func (m *modelDialogCmp) getProvider(providerID catwalk.InferenceProvider) (*catwalk.Provider, error) {
	cfg := config.Get()
	providers, err := config.Providers(cfg)
	if err != nil {
		return nil, err
	}
	for _, p := range providers {
		if p.ID == providerID {
			return &p, nil
		}
	}
	return nil, nil
}

func (m *modelDialogCmp) saveAPIKeyAndContinue(apiKey string) tea.Cmd {
	if m.selectedModel == nil {
		return util.ReportError(fmt.Errorf("no model selected"))
	}

	cfg := config.Get()
	err := cfg.SetProviderAPIKey(string(m.selectedModel.Provider.ID), apiKey)
	if err != nil {
		return util.ReportError(fmt.Errorf("failed to save API key: %w", err))
	}

	// Reset API key state and continue with model selection
	selectedModel := *m.selectedModel
	return tea.Sequence(
		util.CmdHandler(dialogs.CloseDialogMsg{}),
		util.CmdHandler(ModelSelectedMsg{
			Model: config.SelectedModel{
				Model:           selectedModel.Model.ID,
				Provider:        string(selectedModel.Provider.ID),
				ReasoningEffort: selectedModel.Model.DefaultReasoningEffort,
				MaxTokens:       selectedModel.Model.DefaultMaxTokens,
			},
			ModelType: m.selectedModelType,
		}),
	)
}
