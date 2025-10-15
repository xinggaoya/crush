package header

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/xinggaoya/crush/internal/config"
	"github.com/xinggaoya/crush/internal/csync"
	"github.com/xinggaoya/crush/internal/fsext"
	"github.com/xinggaoya/crush/internal/lsp"
	"github.com/xinggaoya/crush/internal/pubsub"
	"github.com/xinggaoya/crush/internal/session"
	"github.com/xinggaoya/crush/internal/tui/styles"
	"github.com/xinggaoya/crush/internal/tui/util"
	"github.com/charmbracelet/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type Header interface {
	util.Model
	SetSession(session session.Session) tea.Cmd
	SetWidth(width int) tea.Cmd
	SetDetailsOpen(open bool)
	ShowingDetails() bool
}

type header struct {
	width       int
	session     session.Session
	lspClients  *csync.Map[string, *lsp.Client]
	detailsOpen bool
}

func New(lspClients *csync.Map[string, *lsp.Client]) Header {
	return &header{
		lspClients: lspClients,
		width:      0,
	}
}

func (h *header) Init() tea.Cmd {
	return nil
}

func (h *header) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pubsub.Event[session.Session]:
		if msg.Type == pubsub.UpdatedEvent {
			if h.session.ID == msg.Payload.ID {
				h.session = msg.Payload
			}
		}
	}
	return h, nil
}

func (h *header) View() string {
	if h.session.ID == "" {
		return ""
	}

	const (
		gap          = " "
		diag         = "╱"
		minDiags     = 3
		leftPadding  = 1
		rightPadding = 1
	)

	t := styles.CurrentTheme()

	var b strings.Builder

	b.WriteString(t.S().Base.Foreground(t.Secondary).Render("Charm™"))
	b.WriteString(gap)
	b.WriteString(styles.ApplyBoldForegroundGrad("CRUSH", t.Secondary, t.Primary))
	b.WriteString(gap)

	availDetailWidth := h.width - leftPadding - rightPadding - lipgloss.Width(b.String()) - minDiags
	details := h.details(availDetailWidth)

	remainingWidth := h.width -
		lipgloss.Width(b.String()) -
		lipgloss.Width(details) -
		leftPadding -
		rightPadding

	if remainingWidth > 0 {
		b.WriteString(t.S().Base.Foreground(t.Primary).Render(
			strings.Repeat(diag, max(minDiags, remainingWidth)),
		))
		b.WriteString(gap)
	}

	b.WriteString(details)

	return t.S().Base.Padding(0, rightPadding, 0, leftPadding).Render(b.String())
}

func (h *header) details(availWidth int) string {
	s := styles.CurrentTheme().S()

	var parts []string

	errorCount := 0
	for l := range h.lspClients.Seq() {
		for _, diagnostics := range l.GetDiagnostics() {
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == protocol.SeverityError {
					errorCount++
				}
			}
		}
	}

	if errorCount > 0 {
		parts = append(parts, s.Error.Render(fmt.Sprintf("%s%d", styles.ErrorIcon, errorCount)))
	}

	agentCfg := config.Get().Agents["coder"]
	model := config.Get().GetModelByType(agentCfg.Model)
	percentage := (float64(h.session.CompletionTokens+h.session.PromptTokens) / float64(model.ContextWindow)) * 100
	formattedPercentage := s.Muted.Render(fmt.Sprintf("%d%%", int(percentage)))
	parts = append(parts, formattedPercentage)

	const keystroke = "ctrl+d"
	if h.detailsOpen {
		parts = append(parts, s.Muted.Render(keystroke)+s.Subtle.Render(" close"))
	} else {
		parts = append(parts, s.Muted.Render(keystroke)+s.Subtle.Render(" open "))
	}

	dot := s.Subtle.Render(" • ")
	metadata := strings.Join(parts, dot)
	metadata = dot + metadata

	// Truncate cwd if necessary, and insert it at the beginning.
	const dirTrimLimit = 4
	cwd := fsext.DirTrim(fsext.PrettyPath(config.Get().WorkingDir()), dirTrimLimit)
	cwd = ansi.Truncate(cwd, max(0, availWidth-lipgloss.Width(metadata)), "…")
	cwd = s.Muted.Render(cwd)

	return cwd + metadata
}

func (h *header) SetDetailsOpen(open bool) {
	h.detailsOpen = open
}

// SetSession implements Header.
func (h *header) SetSession(session session.Session) tea.Cmd {
	h.session = session
	return nil
}

// SetWidth implements Header.
func (h *header) SetWidth(width int) tea.Cmd {
	h.width = width
	return nil
}

// ShowingDetails implements Header.
func (h *header) ShowingDetails() bool {
	return h.detailsOpen
}
