package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// styles groups the styles of the interface. Foreground colors come from the
// sixteen ANSI colors so the interface adapts to every terminal theme; the
// rules adapt to the terminal background.
type styles struct {
	header      lipgloss.Style
	title       lipgloss.Style
	selected    lipgloss.Style
	dim         lipgloss.Style
	separator   lipgloss.Style
	divider     lipgloss.Style
	notice      lipgloss.Style
	footer      lipgloss.Style
	on          lipgloss.Style
	off         lipgloss.Style
	errorText   lipgloss.Style
	inputBox    lipgloss.Style
	inputPrompt lipgloss.Style

	user      section
	assistant section
	thinking  section
	tool      section
	failure   section
}

// section groups the styles of one kind of conversation block.
type section struct {
	// title styles the label of the block.
	title lipgloss.Style

	// body styles the content of the block.
	body lipgloss.Style
}

// block renders a conversation block of the given width: the label styled by
// the section, a blank line and the wrapped body.
func (s section) block(width int, label, body string) string {
	return s.titled(width, s.title.Render(label), body)
}

// titled renders a conversation block with a label that already carries its
// styling, which the tool blocks use to mix the invocation and its arguments.
// The label and the body wrap to the width of the block, so a long title
// breaks into several lines instead of running past the terminal.
func (s section) titled(width int, label, body string) string {
	if body == "" {
		return s.styled(width, label, "")
	}
	return s.styled(width, label, s.body.Render(wrap(body, width)))
}

// styled renders a conversation block whose body is already rendered and
// wrapped, which the markdown answers use so their styling survives. The label
// is wrapped to the width of the block; the body is emitted as is.
func (s section) styled(width int, label, body string) string {
	lines := strings.Split(wrapLabel(label, width), "\n")
	if body != "" {
		lines = append(lines, "", body)
	}
	return strings.Join(lines, "\n")
}

// wrapLabel wraps a label that already carries styling to width columns. It
// keeps the styles on every line it produces, which the plain text wrapper
// used for bodies does not need to do.
func wrapLabel(label string, width int) string {
	if width <= 0 {
		return label
	}
	return lipgloss.Wrap(label, width, "")
}

// newStyles builds the styles of the interface for the given terminal
// background.
func newStyles(isDark bool) styles {
	lightDark := lipgloss.LightDark(isDark)

	return styles{
		header:   lipgloss.NewStyle().Faint(true),
		title:    lipgloss.NewStyle().Bold(true),
		selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		dim:      lipgloss.NewStyle().Faint(true),
		separator: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("252"), lipgloss.Color("236"))),
		divider: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("246"), lipgloss.Color("242"))),
		notice:      lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		footer:      lipgloss.NewStyle().Faint(true),
		on:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		off:         lipgloss.NewStyle().Faint(true),
		errorText:   lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		inputPrompt: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		inputBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lightDark(lipgloss.Color("252"), lipgloss.Color("236"))).
			Padding(1, 1),

		user: section{
			title: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
			body:  lipgloss.NewStyle(),
		},
		assistant: section{
			title: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
			body:  lipgloss.NewStyle(),
		},
		thinking: section{
			title: lipgloss.NewStyle().Faint(true).Italic(true),
			body:  lipgloss.NewStyle().Faint(true).Italic(true),
		},
		tool: section{
			title: lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
			body:  lipgloss.NewStyle().Faint(true),
		},
		failure: section{
			title: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
			body:  lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		},
	}
}
