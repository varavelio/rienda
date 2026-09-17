package tui

import "github.com/charmbracelet/lipgloss"

// styles groups the styles of the interface. Colors are chosen from the
// sixteen ANSI colors so the interface adapts to every terminal theme.
type styles struct {
	header      lipgloss.Style
	title       lipgloss.Style
	selected    lipgloss.Style
	dim         lipgloss.Style
	userLabel   lipgloss.Style
	thinking    lipgloss.Style
	tool        lipgloss.Style
	toolError   lipgloss.Style
	toolOutput  lipgloss.Style
	notice      lipgloss.Style
	errorText   lipgloss.Style
	inputPrompt lipgloss.Style
}

// newStyles builds the styles of the interface.
func newStyles() styles {
	return styles{
		header:      lipgloss.NewStyle().Faint(true),
		title:       lipgloss.NewStyle().Bold(true),
		selected:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		dim:         lipgloss.NewStyle().Faint(true),
		userLabel:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		thinking:    lipgloss.NewStyle().Faint(true).Italic(true),
		tool:        lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
		toolError:   lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		toolOutput:  lipgloss.NewStyle().Faint(true),
		notice:      lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		errorText:   lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		inputPrompt: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
	}
}
