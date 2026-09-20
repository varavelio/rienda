package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Marker glyphs open the labels of the conversation blocks. A solid bar opens
// a turn and a thin one opens an activity inside a turn, so the eye can tell
// where a turn starts at a glance.
const (
	// markerTurn opens the labels of the turns of the conversation.
	markerTurn = "▌"
	// markerActivity opens the labels of the activities inside a turn, such
	// as the reasoning and the tool calls.
	markerActivity = "│"
)

// styles groups the styles of the interface. Foreground colors come from the
// sixteen ANSI colors so the interface adapts to every terminal theme; the
// rules adapt to the terminal background.
//
// Every element keeps a color of its own: the blue of a highlighted row, the
// magenta of the user, the green of the agent and the yellow of the tags and
// the notices. A color shared by two elements would make one read as the
// other, which is what the palette avoids.
type styles struct {
	header    lipgloss.Style
	title     lipgloss.Style
	selected  lipgloss.Style
	dim       lipgloss.Style
	separator lipgloss.Style
	divider   lipgloss.Style
	notice    lipgloss.Style
	footer    lipgloss.Style
	activity  lipgloss.Style
	// on and off style the state of an option of the harness.
	on  lipgloss.Style
	off lipgloss.Style

	// tag styles the label of a turn of the tree and branch styles the marks
	// that place a turn in the branch the session runs.
	tag         lipgloss.Style
	branch      lipgloss.Style
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
	// marker is the glyph that opens the label, marking where the block
	// starts. A solid marker opens a turn and a thin one opens an activity
	// inside a turn.
	marker string

	// markerStyle styles the marker, so it can carry the color of the label
	// without inheriting the styling the glyph does not render well.
	markerStyle lipgloss.Style

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
		return s.rendered(width, label, "")
	}
	return s.rendered(width, label, s.body.Render(wrap(body, width)))
}

// rendered renders a conversation block whose body is already rendered and
// wrapped, which the markdown answers use so their styling survives. The label
// is wrapped to the width of the block; the body is emitted as is.
func (s section) rendered(width int, label, body string) string {
	lines := strings.Split(wrapLabel(s.mark(label), width), "\n")
	if body != "" {
		lines = append(lines, "", body)
	}
	return strings.Join(lines, "\n")
}

// mark prefixes the marker glyph to an already styled label, opening the block
// with the color of its kind.
func (s section) mark(label string) string {
	if s.marker == "" {
		return label
	}
	return s.markerStyle.Render(s.marker) + " " + label
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
		activity:    lipgloss.NewStyle(),
		on:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		off:         lipgloss.NewStyle().Faint(true),
		tag:         lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		branch:      lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		errorText:   lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		inputPrompt: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		inputBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lightDark(lipgloss.Color("252"), lipgloss.Color("236"))).
			Padding(1, 1),

		user: section{
			marker:      markerTurn,
			markerStyle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13")),
			title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13")),
			body:        lipgloss.NewStyle(),
		},
		assistant: section{
			marker:      markerTurn,
			markerStyle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
			title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
			body:        lipgloss.NewStyle(),
		},
		thinking: section{
			marker:      markerActivity,
			markerStyle: lipgloss.NewStyle().Faint(true),
			title:       lipgloss.NewStyle().Faint(true).Italic(true),
			body:        lipgloss.NewStyle().Faint(true).Italic(true),
		},
		tool: section{
			marker:      markerActivity,
			markerStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
			title:       lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
			body:        lipgloss.NewStyle().Faint(true),
		},
		failure: section{
			marker:      markerTurn,
			markerStyle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
			title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
			body:        lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		},
	}
}
