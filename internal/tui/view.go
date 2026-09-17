package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/varavelio/rienda/internal/agent"
)

// View renders the interface.
func (m *model) View() string {
	switch {
	case m.fatal != nil:
		return m.styles.errorText.Render("error: "+m.fatal.Error()) + "\n"
	case m.phase == phasePicker:
		return m.viewPicker()
	case m.phase == phasePreparing:
		return m.viewPreparing()
	default:
		return m.viewChat()
	}
}

// viewPicker renders the list of agents to choose from.
func (m *model) viewPicker() string {
	lines := []string{
		m.styles.title.Render("rienda"),
		"",
		"Select an agent",
		"",
	}
	for i, definition := range m.agents {
		lines = append(lines, m.pickerLine(i, definition))
	}
	lines = append(lines, "", m.styles.dim.Render("↑/↓ move · enter select · ctrl+c quit"))
	return strings.Join(lines, "\n")
}

// pickerLine renders one agent row of the picker.
func (m *model) pickerLine(index int, definition agent.Agent) string {
	marker, name := "  ", definition.ID
	if index == m.cursor {
		marker, name = "› ", m.styles.selected.Render(name)
	}
	if definition.Description == "" {
		return marker + name
	}
	return marker + name + "  " + m.styles.dim.Render(definition.Description)
}

// viewPreparing renders the session preparation screen.
func (m *model) viewPreparing() string {
	return m.styles.title.Render("rienda") + "\n\n" +
		m.spinner.View() + " preparing the session of " + m.agents[m.selected].ID + "\n"
}

// viewChat renders the header, the conversation, the status line and the input.
func (m *model) viewChat() string {
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.viewHeader(),
		m.viewport.View(),
		m.viewStatus(),
		m.input.View(),
	)
}

// viewHeader renders the session identity.
func (m *model) viewHeader() string {
	info := m.session.Info()
	parts := []string{"rienda", info.Agent, info.Model}
	if info.ID != "" {
		parts = append(parts, info.ID)
	}
	return m.clip(m.styles.header.Render(strings.Join(parts, " · ")))
}

// viewStatus renders the spinner of a run in flight, the token usage or the
// keys the interface listens to.
func (m *model) viewStatus() string {
	switch {
	case m.running:
		return m.clip(m.spinner.View() + " working… · esc interrupts")
	case m.usageIn > 0 || m.usageOut > 0:
		return m.clip(m.styles.dim.Render(fmt.Sprintf(
			"tokens %d in · %d out · enter send · ctrl+c quit",
			m.usageIn,
			m.usageOut,
		)))
	default:
		return m.clip(m.styles.dim.Render("enter send · esc interrupts · ctrl+c quit"))
	}
}

// renderTranscript renders the whole conversation wrapped to the viewport
// width.
func (m *model) renderTranscript() string {
	blocks := make([]string, 0, len(m.transcript.entries))
	for _, current := range m.transcript.entries {
		blocks = append(blocks, m.renderEntry(current))
	}
	return strings.Join(blocks, "\n\n")
}

// renderEntry renders one transcript entry.
func (m *model) renderEntry(current entry) string {
	width := m.viewport.Width
	switch current.kind {
	case entryUser:
		return m.styles.userLabel.Render("› ") + wrap(current.text, width-2)
	case entryAssistant:
		return wrap(current.text, width)
	case entryThinking:
		return m.styles.thinking.Render(wrap(current.text, width))
	case entryTool:
		return m.renderToolEntry(current, width)
	case entryNotice:
		return m.styles.notice.Render(wrap(current.text, width))
	default:
		return m.styles.errorText.Render(wrap("error: "+current.text, width))
	}
}

// renderToolEntry renders a tool invocation with its output.
func (m *model) renderToolEntry(current entry, width int) string {
	marker, style := "●", m.styles.tool
	switch {
	case current.toolError:
		marker, style = "✗", m.styles.toolError
	case !current.toolDone:
		marker = "○"
	}

	head := marker + " " + current.toolName
	if current.toolArguments != "" {
		head += " " + current.toolArguments
	}

	lines := []string{m.clip(style.Render(head))}
	if output := strings.TrimRight(current.text, "\n"); output != "" {
		lines = append(lines, m.styles.toolOutput.Render(indent(wrap(output, width-2))))
	}
	if current.truncated {
		lines = append(lines, m.styles.dim.Render("  [output truncated]"))
	}
	return strings.Join(lines, "\n")
}

// clip truncates a rendered line to the terminal width.
func (m *model) clip(text string) string {
	if m.width <= 0 {
		return text
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(text)
}

// wrap wraps text to width columns, keeping words together when possible.
func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

// indent prefixes every line of text with two spaces.
func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}
