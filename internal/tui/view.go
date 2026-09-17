package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varavelio/rienda/internal/agent"
)

// brand is the name of the interface, shown at the top of every phase.
const brand = "rienda"

// View renders the interface in the alternate screen.
func (m *model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

// render builds the interface of the phase in progress.
func (m *model) render() string {
	switch {
	case m.fatal != nil:
		return m.styles.errorText.Render("error: "+m.fatal.Error()) + "\n"
	case m.phase == phaseMenu:
		return m.viewMenu()
	case m.phase == phaseSessions:
		return m.viewSessions()
	case m.phase == phasePicker:
		return m.viewPicker()
	case m.phase == phasePreparing:
		return m.viewPreparing()
	default:
		return m.viewChat()
	}
}

// headerRows returns the rows that open a phase: the identity line, the
// separator under it and the padding below the separator.
func (m *model) headerRows(identity string) []string {
	return []string{m.clip(identity), m.ruleLine(m.styles.separator), ""}
}

// footerRows returns the rows that close a phase: the separator between two
// blank rows and the hints of the phase.
func (m *model) footerRows(hints string) []string {
	return []string{"", m.ruleLine(m.styles.separator), "", m.clip(m.styles.footer.Render(hints))}
}

// viewMenu renders the choice between a new session and a previous one.
func (m *model) viewMenu() string {
	rows := m.headerRows(m.styles.title.Render(brand))
	rows = append(rows,
		"What do you want to do?",
		"",
		m.row(m.menu == menuNew, "New session"),
		m.row(m.menu == menuContinue, "Continue a previous session"),
	)
	rows = append(rows, m.footerRows("↑/↓ move · enter select · ctrl+c quit")...)
	return strings.Join(rows, "\n")
}

// viewSessions renders the list of previous sessions of the workspace.
func (m *model) viewSessions() string {
	rows := m.headerRows(m.styles.title.Render(brand))
	rows = append(rows, "Continue a previous session", "")

	first, last := visibleWindow(m.chosen, len(m.sessions), m.listRows())
	for index := first; index < last; index++ {
		rows = append(rows, m.sessionLine(index))
	}

	rows = append(rows, m.footerRows("↑/↓ move · enter open · esc back · ctrl+c quit")...)
	return strings.Join(rows, "\n")
}

// sessionLine renders one row of the session list.
func (m *model) sessionLine(index int) string {
	info := m.sessions[index]
	title := info.Title
	if title == "" {
		title = "untitled session"
	}
	return m.row(index == m.chosen, title) + "  " + m.styles.dim.Render(
		info.Agent+" · "+formatAge(info.UpdatedAt),
	)
}

// row renders one list row, highlighted when it holds the cursor.
func (m *model) row(highlighted bool, label string) string {
	if highlighted {
		return "› " + m.styles.selected.Render(label)
	}
	return "  " + label
}

// listRows returns the list rows that fit on screen outside the fixed lines of
// the list phases.
func (m *model) listRows() int {
	return max(1, m.height-listChrome)
}

// visibleWindow returns the range of a list of total items that fits in rows
// while keeping the cursor visible.
func visibleWindow(cursor, total, rows int) (first, last int) {
	if total <= rows {
		return 0, total
	}
	first = min(max(cursor-rows+1, 0), total-rows)
	return first, first + rows
}

// formatAge returns a short, human friendly age of a moment in time.
func formatAge(moment time.Time) string {
	if moment.IsZero() {
		return "unknown"
	}

	elapsed := time.Since(moment)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	default:
		return moment.Local().Format("Jan 2")
	}
}

// viewPicker renders the list of agents to choose from.
func (m *model) viewPicker() string {
	rows := m.headerRows(m.styles.title.Render(brand))
	rows = append(rows, "Select an agent", "")

	first, last := visibleWindow(m.cursor, len(m.agents), m.listRows())
	for index := first; index < last; index++ {
		rows = append(rows, m.pickerLine(index, m.agents[index]))
	}

	rows = append(rows, m.footerRows("↑/↓ move · enter select · ctrl+c quit")...)
	return strings.Join(rows, "\n")
}

// pickerLine renders one agent row of the picker.
func (m *model) pickerLine(index int, definition agent.Agent) string {
	line := m.row(index == m.cursor, definition.ID)
	if definition.Description == "" {
		return line
	}
	return line + "  " + m.styles.dim.Render(definition.Description)
}

// viewPreparing renders the session preparation screen.
func (m *model) viewPreparing() string {
	rows := m.headerRows(m.styles.title.Render(brand))
	rows = append(rows, m.spinner.View()+" preparing the session of "+m.agents[m.selected].ID, "")
	return strings.Join(rows, "\n")
}

// viewChat renders the header, the conversation, the input and the footer of
// the chat.
func (m *model) viewChat() string {
	return lipgloss.JoinVertical(
		lipgloss.Left,
		strings.Join(m.headerRows(m.chatIdentity()), "\n"),
		m.viewport.View(),
		m.viewInput(),
		m.chatFooter(),
	)
}

// chatIdentity renders the identity of the session: the brand followed by the
// agent, the model and the session id.
func (m *model) chatIdentity() string {
	info := m.session.Info()
	parts := []string{info.Agent, info.Model}
	if info.ID != "" {
		parts = append(parts, info.ID)
	}
	return m.styles.title.Render(brand) + m.styles.header.Render(" · "+strings.Join(parts, " · "))
}

// viewInput renders the prompt input inside a box, padded from the transcript
// above.
func (m *model) viewInput() string {
	return "\n" + m.styles.inputBox.Width(max(1, m.width)).Render(m.input.View())
}

// chatFooter renders the row under the input box: the run in flight or the
// token usage, together with the keys the interface listens to.
func (m *model) chatFooter() string {
	parts := make([]string, 0, 2)
	if !m.running && !m.viewport.AtBottom() {
		parts = append(parts, "↑ scrolled")
	}

	switch {
	case m.running:
		parts = append(parts, m.spinner.View()+" working… · esc interrupts")
	case m.usageIn > 0 || m.usageOut > 0:
		parts = append(parts, fmt.Sprintf(
			"tokens %d in · %d out · enter send · ctrl+j newline · ctrl+c quit",
			m.usageIn,
			m.usageOut,
		))
	default:
		parts = append(parts, "enter send · ctrl+j newline · ctrl+c quit")
	}

	text := m.clip(m.styles.footer.Render(strings.Join(parts, " · ")))
	return text
}

// renderTranscript renders the whole conversation: the blocks separated by the
// divider rule, resting on the bottom of the transcript.
func (m *model) renderTranscript() string {
	if len(m.transcript.entries) == 0 {
		return ""
	}

	blocks := make([]string, 0, len(m.transcript.entries))
	for _, current := range m.transcript.entries {
		blocks = append(blocks, m.renderEntry(current))
	}

	divider := strings.Join([]string{"", m.ruleLine(m.styles.divider), ""}, "\n")
	return anchorBottom(strings.Join(blocks, "\n"+divider+"\n"), m.viewport.Height())
}

// anchorBottom pads text with blank lines above so it rests on the bottom of a
// space of the given height.
func anchorBottom(text string, height int) string {
	if pad := height - (strings.Count(text, "\n") + 1); pad > 0 {
		return strings.Repeat("\n", pad) + text
	}
	return text
}

// renderEntry renders one transcript entry as a conversation block.
func (m *model) renderEntry(current entry) string {
	width := m.viewport.Width()
	switch current.kind {
	case entryUser:
		return m.styles.user.block(width, "You", current.text)
	case entryAssistant:
		return m.styles.assistant.block(width, m.assistantName(), current.text)
	case entryThinking:
		return m.styles.thinking.block(width, "Thinking", current.text)
	case entryTool:
		return m.renderToolEntry(current, width)
	case entryNotice:
		return m.styles.notice.Render(wrap(current.text, width))
	default:
		return m.styles.failure.block(width, "Error", current.text)
	}
}

// assistantName returns the name shown for the answers of the agent.
func (m *model) assistantName() string {
	return m.session.Info().Agent
}

// renderToolEntry renders a tool invocation with its output.
func (m *model) renderToolEntry(current entry, width int) string {
	name := m.styles.tool.title
	switch {
	case current.toolError:
		name = m.styles.errorText
	case !current.toolDone:
		name = m.styles.dim
	}

	label := name.Render(current.toolName)
	if current.toolArguments != "" {
		label += " " + m.styles.dim.Render(current.toolArguments)
	}

	output := strings.TrimRight(current.text, "\n")
	if current.truncated {
		output += "\n[output truncated]"
	}
	return m.styles.tool.titled(width, label, output)
}

// ruleLine renders a horizontal line of the given style across the terminal.
func (m *model) ruleLine(style lipgloss.Style) string {
	return m.clip(style.Render(strings.Repeat("─", max(1, m.width))))
}

// clip truncates a rendered line to the terminal width.
func (m *model) clip(text string) string {
	return clipText(m.width, text)
}

// clipText truncates a rendered line to the given width.
func clipText(width int, text string) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(text)
}

// wrap wraps text to width columns, keeping words together when possible.
func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

// indentLines prefixes every line of text with prefix.
func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
