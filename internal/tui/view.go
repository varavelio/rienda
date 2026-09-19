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
const brand = "varavel rienda"

// maxToolLabel caps the characters of a tool invocation the interface shows,
// so a call carrying a huge argument cannot flood the conversation.
const maxToolLabel = 400

// previewLines caps how many trailing lines a collapsed block shows, so the
// reader keeps a sense of what is happening without the block flooding the
// conversation.
const previewLines = 3

// View renders the interface in the alternate screen.
func (m *model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

// render builds the interface of the phase in progress, padded to the terminal
// width.
func (m *model) render() string {
	text := m.renderPhase()
	if m.width <= 0 {
		return text
	}
	return padLines(text, m.width)
}

// padLines pads every line to width cells. A line that changed but kept its
// width lets the renderer overwrite it cell by cell without leaving the tail of
// a longer line behind, which shows as stale characters glued to the new
// content.
func padLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if gap := width - ansi.StringWidth(line); gap > 0 {
			lines[i] = line + strings.Repeat(" ", gap)
		}
	}
	return strings.Join(lines, "\n")
}

// renderPhase builds the interface of the phase in progress.
func (m *model) renderPhase() string {
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

// brandIdentity renders the logo and the name that open the identity line of
// every phase. The logo stays static and shares the faint color of the header
// text, so it reads as part of the identity rather than drawing the eye; the
// status line carries the only animation of the interface.
func (m *model) brandIdentity() string {
	return m.styles.header.Render(varavelLogo+" · ") + m.styles.title.Render(brand)
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
	rows := m.headerRows(m.brandIdentity())
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
	rows := m.headerRows(m.brandIdentity())
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
	rows := m.headerRows(m.brandIdentity())
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
	return m.brandIdentity() + m.styles.header.Render(" · settings")
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
	rows := m.headerRows(m.brandIdentity())
	mark := m.styles.dim.Render(m.spinner.View())
	rows = append(rows, mark+" preparing the session of "+m.agents[m.selected].ID, "")
	return strings.Join(rows, "\n")
}

// viewChat renders the header, the conversation, the status line, the input
// and the footer of the chat.
func (m *model) viewChat() string {
	parts := []string{
		strings.Join(m.headerRows(m.chatIdentity()), "\n"),
		m.conversation.view(),
	}
	// A status block the terminal cannot afford contributes no row at all.
	if m.activityHeight() > 0 {
		parts = append(parts, m.activityBlock())
	}
	parts = append(parts, m.viewInput(), m.chatFooter())
	return strings.Join(parts, "\n")
}

// activityBlock renders the status of the run in flight between the
// conversation and the prompt, with two blank rows above and one below, so the
// status line breathes without touching the content or the input box. A very
// short terminal keeps only the status line while a run is in flight, and
// nothing while idle.
func (m *model) activityBlock() string {
	if m.activityHeight() == 0 {
		return ""
	}
	if m.activityHeight() == 1 {
		return m.activityLine()
	}
	return "\n\n" + m.activityLine() + "\n"
}

// activityLine renders the single status row that reports the run in flight:
// the spinner and what it is doing, with the key that interrupts it. The row
// is blank while no run is in flight, which keeps the block the same height.
func (m *model) activityLine() string {
	if !m.running {
		return ""
	}

	hint := m.styles.footer.Render("esc to interrupt")
	if m.confirmInterrupt {
		hint = m.styles.notice.Render("esc again to interrupt")
	}
	line := m.styles.dim.Render(m.spinner.View()) + " " +
		m.styles.activity.Render(m.activityLabel()) +
		m.styles.footer.Render(" · ") + hint
	return m.clip(line)
}

// activityLabel describes what the run in flight is doing.
func (m *model) activityLabel() string {
	switch m.activity {
	case activityThinking:
		return "thinking"
	case activityTool:
		return "running " + m.activityTool
	default:
		return "working"
	}
}

// chatIdentity renders the identity of the session: the brand followed by the
// agent, the model and the session id.
func (m *model) chatIdentity() string {
	info := m.session.Info()
	parts := []string{info.Agent, info.Model}
	if info.ID != "" {
		parts = append(parts, info.ID)
	}
	return m.brandIdentity() + m.styles.header.Render(" · "+strings.Join(parts, " · "))
}

// viewInput renders the prompt input inside a box.
func (m *model) viewInput() string {
	return m.styles.inputBox.Width(max(1, m.width)).Render(m.input.View())
}

// chatFooter renders the fixed row under the input box: the token usage and
// the keys the interface listens to. The run in flight is reported by the
// activity line above the input, not here.
func (m *model) chatFooter() string {
	parts := make([]string, 0, 3)
	if !m.conversation.atBottom() {
		parts = append(parts, "↑ scrolled")
	}
	if m.usageIn > 0 || m.usageOut > 0 {
		parts = append(parts, fmt.Sprintf("tokens %d in · %d out", m.usageIn, m.usageOut))
	}
	parts = append(parts, "enter send · ctrl+p settings · ctrl+j newline · ctrl+c quit")

	return m.clip(m.styles.footer.Render(strings.Join(parts, " · ")))
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
		return m.renderThinkingEntry(current, width)
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
	return m.styles.assistant.rendered(width, label, body)
}

// divider returns the rule that separates two conversation blocks.
func (m *model) divider() string {
	return strings.Join([]string{"", m.ruleLine(m.styles.divider), ""}, "\n")
}

// assistantName returns the name shown for the answers of the agent.
func (m *model) assistantName() string {
	return m.session.Info().Agent
}

// renderThinkingEntry renders a reasoning block: the whole text when the
// preference expands it, a preview of its trailing lines otherwise. The run in
// flight is reported by the activity line, so the block carries no spinner of
// its own.
func (m *model) renderThinkingEntry(current *entry, width int) string {
	body := m.blockBody(current.text(), m.preferences.ExpandThinking, width)
	return m.styles.thinking.block(width, "Thinking", body)
}

// blockBody returns the body of a collapsible block: the whole text when the
// preference expands it, or a preview of its trailing lines otherwise.
func (m *model) blockBody(text string, expanded bool, width int) string {
	if expanded {
		return strings.TrimRight(text, "\n")
	}
	return tailPreview(text, previewLines, width)
}

// tailPreview returns a compact window on the tail of text, prefixed with an
// ellipsis when earlier content was dropped. The window is measured in rows as
// the terminal shows them: text is wrapped to width first, so a long paragraph
// previews as the last limit rows and the block keeps a steady height whatever
// the shape of the content. An empty text yields an empty preview.
func tailPreview(text string, limit, width int) string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}

	rows := strings.Split(wrap(text, width), "\n")
	if len(rows) <= limit {
		return strings.Join(rows, "\n")
	}

	tail := rows[len(rows)-limit:]
	tail[0] = clipText(width, "\u2026 "+tail[0])
	return strings.Join(tail, "\n")
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

	output := strings.TrimRight(current.text(), "\n")
	if current.truncated {
		output += "\n[output truncated]"
	}
	body := m.blockBody(output, m.preferences.ExpandToolOutput, width)
	return m.styles.tool.titled(width, label, body)
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
