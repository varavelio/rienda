package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varavelio/rienda/internal/agent"
)

// brand is the name of the interface, shown at the top of every phase.
const brand = "rienda"

// maxToolLabel caps the characters of a tool invocation the interface shows,
// so a call carrying a huge argument cannot flood the conversation.
const maxToolLabel = 400

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
	case m.phase == phaseSettings:
		return m.viewSettings()
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
	rows = append(rows, m.footerRows("↑/↓ move · enter select · ctrl+p settings · ctrl+c quit")...)
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

	rows = append(
		rows,
		m.footerRows("↑/↓ move · enter open · esc back · ctrl+p settings · ctrl+c quit")...,
	)
	return strings.Join(rows, "\n")
}

// sessionLine renders one row of the session list.
func (m *model) sessionLine(index int) string {
	info := m.sessions[index]
	title := info.Title
	if title == "" {
		title = "untitled session"
	}
	details := m.styles.dim.Render(info.Agent + " · " + formatAge(info.UpdatedAt))
	return m.clip(m.row(index == m.chosen, title) + "  " + details)
}

// row renders one list row, highlighted when it holds the cursor.
func (m *model) row(highlighted bool, label string) string {
	if highlighted {
		return m.clip("› " + m.styles.selected.Render(label))
	}
	return m.clip("  " + label)
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

	rows = append(rows, m.footerRows("↑/↓ move · enter select · ctrl+p settings · ctrl+c quit")...)
	return strings.Join(rows, "\n")
}

// pickerLine renders one agent row of the picker.
func (m *model) pickerLine(index int, definition agent.Agent) string {
	line := m.row(index == m.cursor, definition.ID)
	if definition.Description == "" {
		return line
	}
	return m.clip(line + "  " + m.styles.dim.Render(definition.Description))
}

// viewSettings renders the command center: the options of the harness and
// their state.
func (m *model) viewSettings() string {
	rows := m.headerRows(m.settingsIdentity())
	rows = append(rows, "Command center", "")

	first, last := visibleWindow(m.settingCursor, len(preferencesList), m.listRows())
	for index := first; index < last; index++ {
		rows = append(rows, m.preferenceLine(index))
	}

	rows = append(rows, m.footerRows("↑/↓ move · enter toggle · esc close")...)
	return strings.Join(rows, "\n")
}

// settingsIdentity renders the identity of the command center.
func (m *model) settingsIdentity() string {
	return m.styles.title.Render(brand) + m.styles.header.Render(" · settings")
}

// preferenceLine renders one option of the command center with its state.
func (m *model) preferenceLine(index int) string {
	option := preferencesList[index]
	state := m.styles.off.Render("[off]")
	if option.IsOn(m.preferences) {
		state = m.styles.on.Render("[on]")
	}
	label := m.row(index == m.settingCursor, option.Label) + "  " + state
	return m.clip(label + "  " + m.styles.dim.Render(option.Note))
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
		m.conversation.view(),
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
	if !m.conversation.atBottom() {
		parts = append(parts, "↑ scrolled")
	}

	switch {
	case m.running:
		hint := "esc interrupts"
		if m.confirmInterrupt {
			hint = "esc again to interrupt"
		}
		parts = append(parts, m.spinner.View()+" working… · "+hint)
	case m.usageIn > 0 || m.usageOut > 0:
		parts = append(parts, fmt.Sprintf(
			"tokens %d in · %d out · enter send · ctrl+p settings · ctrl+j newline · ctrl+c quit",
			m.usageIn,
			m.usageOut,
		))
	default:
		parts = append(parts, "enter send · ctrl+p settings · ctrl+j newline · ctrl+c quit")
	}

	text := m.clip(m.styles.footer.Render(strings.Join(parts, " · ")))
	return text
}

// renderEntry renders the transcript entry at the given index as a
// conversation block, preceded by the divider that separates it from the
// previous one.
func (m *model) renderEntry(index int) string {
	block := m.renderBlock(index)
	if index == 0 || block == "" {
		return block
	}
	return m.divider() + "\n" + block
}

// renderBlock renders the content of the transcript entry at the given index.
func (m *model) renderBlock(index int) string {
	current := &m.transcript.entries[index]
	width := m.width

	switch current.kind {
	case entryUser:
		return m.styles.user.block(width, "You", current.text())
	case entryAssistant:
		return m.renderAssistantBlock(current, width)
	case entryThinking:
		return m.renderThinkingEntry(current, width, m.active(index))
	case entryTool:
		return m.renderToolEntry(current, width)
	case entryNotice:
		return m.styles.notice.Render(wrap(current.text(), width))
	default:
		return m.styles.failure.block(width, "Error", current.text())
	}
}

// renderAssistantBlock renders an answer of the model, formatted as markdown
// so headings, emphasis, code, lists and links read the way the model wrote
// them.
func (m *model) renderAssistantBlock(current *entry, width int) string {
	if !m.preferences.RenderMarkdown {
		return m.styles.assistant.block(width, m.assistantName(), current.text())
	}

	body := m.markdown.render(current.text(), width, m.hasDarkBG)
	label := m.styles.assistant.title.Render(m.assistantName())
	return m.styles.assistant.styled(width, label, body)
}

// divider returns the rule that separates two conversation blocks.
func (m *model) divider() string {
	return strings.Join([]string{"", m.ruleLine(m.styles.divider), ""}, "\n")
}

// active reports whether the model is still writing the entry at the given
// index, which is the last one of a run in flight.
func (m *model) active(index int) bool {
	return m.running && index == len(m.transcript.entries)-1
}

// assistantName returns the name shown for the answers of the agent.
func (m *model) assistantName() string {
	return m.session.Info().Agent
}

// renderThinkingEntry renders a reasoning block: the whole text when the
// preferences ask for it, a spinner while it streams and a collapsed line
// afterwards otherwise.
func (m *model) renderThinkingEntry(current *entry, width int, active bool) string {
	if !m.preferences.HideThinking {
		return m.styles.thinking.block(width, "Thinking", current.text())
	}

	label := "Thinking"
	if active {
		label = m.spinner.View() + " " + label
	}
	return m.styles.thinking.titled(width, m.styles.thinking.title.Render(label), "")
}

// renderToolEntry renders a tool invocation. The preferences decide whether
// the output is shown or only the invocation, which keeps a noisy tool from
// flooding the conversation.
func (m *model) renderToolEntry(current *entry, width int) string {
	name := m.styles.tool.title
	switch {
	case current.toolError:
		name = m.styles.errorText
	case !current.toolDone:
		name = m.styles.dim
	}

	label := name.Render(current.toolName)
	if current.toolArguments != "" {
		label += " " + m.styles.dim.Render(toolLabel(current.toolArguments))
	}
	if !current.toolDone {
		label = m.spinner.View() + " " + label
	}

	if m.preferences.HideToolOutput {
		return m.styles.tool.titled(width, label, "")
	}

	output := strings.TrimRight(current.text(), "\n")
	if current.truncated {
		output += "\n[output truncated]"
	}
	return m.styles.tool.titled(width, label, output)
}

// toolLabel compacts the arguments of an invocation into one line and caps how
// much of them is shown.
func toolLabel(arguments string) string {
	return ansi.Truncate(strings.Join(strings.Fields(arguments), " "), maxToolLabel, "…")
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

// wrap wraps plain text to width columns, keeping words together when
// possible. It runs on the plain text of a block, before the style of the
// block is applied, so it does not need to track styles.
func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	return ansi.Wrap(text, width, "")
}
