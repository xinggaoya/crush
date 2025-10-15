package mcp

import (
	"fmt"

	"github.com/charmbracelet/lipgloss/v2"

	"github.com/xinggaoya/crush/internal/config"
	"github.com/xinggaoya/crush/internal/llm/agent"
	"github.com/xinggaoya/crush/internal/tui/components/core"
	"github.com/xinggaoya/crush/internal/tui/styles"
)

// RenderOptions contains options for rendering MCP lists.
type RenderOptions struct {
	MaxWidth    int
	MaxItems    int
	ShowSection bool
	SectionName string
}

// RenderMCPList renders a list of MCP status items with the given options.
func RenderMCPList(opts RenderOptions) []string {
	t := styles.CurrentTheme()
	mcpList := []string{}

	if opts.ShowSection {
		sectionName := opts.SectionName
		if sectionName == "" {
			sectionName = "MCPs"
		}
		section := t.S().Subtle.Render(sectionName)
		mcpList = append(mcpList, section, "")
	}

	mcps := config.Get().MCP.Sorted()
	if len(mcps) == 0 {
		mcpList = append(mcpList, t.S().Base.Foreground(t.Border).Render("None"))
		return mcpList
	}

	// Get MCP states
	mcpStates := agent.GetMCPStates()

	// Determine how many items to show
	maxItems := len(mcps)
	if opts.MaxItems > 0 {
		maxItems = min(opts.MaxItems, len(mcps))
	}

	for i, l := range mcps {
		if i >= maxItems {
			break
		}

		// Determine icon and color based on state
		icon := t.ItemOfflineIcon
		description := ""
		extraContent := ""

		if state, exists := mcpStates[l.Name]; exists {
			switch state.State {
			case agent.MCPStateDisabled:
				description = t.S().Subtle.Render("disabled")
			case agent.MCPStateStarting:
				icon = t.ItemBusyIcon
				description = t.S().Subtle.Render("starting...")
			case agent.MCPStateConnected:
				icon = t.ItemOnlineIcon
				if state.ToolCount > 0 {
					extraContent = t.S().Subtle.Render(fmt.Sprintf("%d tools", state.ToolCount))
				}
			case agent.MCPStateError:
				icon = t.ItemErrorIcon
				if state.Error != nil {
					description = t.S().Subtle.Render(fmt.Sprintf("error: %s", state.Error.Error()))
				} else {
					description = t.S().Subtle.Render("error")
				}
			}
		} else if l.MCP.Disabled {
			description = t.S().Subtle.Render("disabled")
		}

		mcpList = append(mcpList,
			core.Status(
				core.StatusOpts{
					Icon:         icon.String(),
					Title:        l.Name,
					Description:  description,
					ExtraContent: extraContent,
				},
				opts.MaxWidth,
			),
		)
	}

	return mcpList
}

// RenderMCPBlock renders a complete MCP block with optional truncation indicator.
func RenderMCPBlock(opts RenderOptions, showTruncationIndicator bool) string {
	t := styles.CurrentTheme()
	mcpList := RenderMCPList(opts)

	// Add truncation indicator if needed
	if showTruncationIndicator && opts.MaxItems > 0 {
		mcps := config.Get().MCP.Sorted()
		if len(mcps) > opts.MaxItems {
			remaining := len(mcps) - opts.MaxItems
			if remaining == 1 {
				mcpList = append(mcpList, t.S().Base.Foreground(t.FgMuted).Render("…"))
			} else {
				mcpList = append(mcpList,
					t.S().Base.Foreground(t.FgSubtle).Render(fmt.Sprintf("…and %d more", remaining)),
				)
			}
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, mcpList...)
	if opts.MaxWidth > 0 {
		return lipgloss.NewStyle().Width(opts.MaxWidth).Render(content)
	}
	return content
}
