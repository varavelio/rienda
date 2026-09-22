package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
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

// Thresholds of the context figure the chat footer shows.
const (
	// contextWarningPercent is the window usage past which the figure is shown
	// as a warning.
	contextWarningPercent = 70

	// contextCriticalPercent is the window usage past which the figure is
	// shown as a failure.
	contextCriticalPercent = 90
)

// Units of the token figure the chat footer shows. A count below tokensPerK is
// rendered plain, and the larger counts are rendered in the unit that keeps
// them short.
const (
	// tokensPerK is the number of tokens one "k" stands for.
	tokensPerK = 1000

	// tokensPerM is the number of tokens one "m" stands for.
	tokensPerM = 1000 * tokensPerK
)

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
	case m.phase == phaseStart:
		return m.viewStart()
	case m.phase == phasePicker:
		return m.viewPicker()
	case m.phase == phaseSettings:
		return m.viewSettings()
	case m.phase == phaseTree:
		return m.viewTree()
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
	return []string{"", m.ruleLine(m.styles.separator), "", m.footerHints(hints)}
}

// footerHints renders the closing hint row of a phase: the hints the phase
// spells out, or the quit request that is pending, which takes their place so
// the reader always sees what the next press does however long the hints are
// and however narrow the terminal is.
func (m *model) footerHints(hints string) string {
	if row, pending := m.pendingQuitRow(); pending {
		return row
	}
	return m.clip(m.styles.footer.Render(hints))
}

// pendingQuitRow renders the row that asks for the press that leaves the
// interface, which is only shown while a quit request waits for it: the phases
// that close with a footer show it in the place of the hints, and the ones that
// close without one, like the preparation screen, show it on its own. It names
// the key that armed the request, so the reader is asked for the press of the
// key that is already under their finger.
func (m *model) pendingQuitRow() (string, bool) {
	if m.confirm.action != confirmQuit {
		return "", false
	}
	return m.clip(m.styles.notice.Render(m.confirm.key + " again to quit")), true
}

// viewStart renders the list that opens the interface: the offer to begin a
// new session and the previous sessions of the workspace, narrowed by the
// query typed into the list.
func (m *model) viewStart() string {
	rows := m.headerRows(m.brandIdentity())
	rows = append(rows, "Start a new session or continue a previous one", "")
	rows = append(rows, m.filterRow(&m.start), "")

	first, last := m.start.window(m.listRows())
	for position := first; position < last; position++ {
		rows = append(rows, m.startLine(position))
	}
	if m.start.empty() {
		rows = append(rows, m.emptyLine("no matches"))
	}

	// The list only returns to the conversation it was opened over when there
	// is one to return to.
	hint := "type to filter · ↑/↓ move · enter open"
	if m.session != nil {
		hint += " · esc back"
	}
	rows = append(rows, m.footerRows(hint+" · ctrl+p settings · ctrl+c quit")...)
	return strings.Join(rows, "\n")
}

// startLine renders one entry of the start list: the offer of a new session or
// a stored session with its agent and age. The entries the list offers are the
// ones it read last, so a session created while the interface runs shows up
// once the list opens again.
func (m *model) startLine(position int) string {
	item := m.starts[m.start.shown[position]]
	if item.newSession {
		return m.row(position == m.start.cursor, "New session")
	}

	info := item.info
	details := m.styles.dim.Render(info.Agent + " · " + formatAge(info.UpdatedAt))
	return m.clip(m.row(position == m.start.cursor, sessionTitle(info)) + "  " + details)
}

// sessionTitle returns the title of a session, naming the ones that were never
// titled so every entry of a list reads as something.
func sessionTitle(info session.Info) string {
	if info.Title == "" {
		return "untitled session"
	}
	return info.Title
}

// filterRow renders the query input of a list, which stays focused while the
// arrows move the highlight.
func (m *model) filterRow(list *filter) string {
	return m.clip(list.view())
}

// emptyLine renders the notice a list shows when it has nothing to offer.
func (m *model) emptyLine(text string) string {
	return m.clip("  " + m.styles.dim.Render(text))
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

// formatElapsed renders how long a turn took for a person to read, rounded to
// the nearest second so the count stays steady while a run streams instead of
// flickering through fractions of a second, as in "8s", "1m30s" or "3h0m0s".
func formatElapsed(d time.Duration) string {
	return d.Round(time.Second).String()
}

// viewPicker renders the list of agents to choose from, narrowed by the query
// typed into it.
func (m *model) viewPicker() string {
	rows := m.headerRows(m.brandIdentity())
	rows = append(rows, "Select an agent", "")
	rows = append(rows, m.filterRow(&m.picker), "")

	first, last := m.picker.window(m.listRows())
	for position := first; position < last; position++ {
		rows = append(rows, m.pickerLine(position))
	}
	if m.picker.empty() {
		rows = append(rows, m.emptyLine("no matches"))
	}

	// The picker only returns to the start list when it was opened from it,
	// which is the case whenever the workspace holds a previous session.
	hint := "type to filter · ↑/↓ move · enter select"
	if len(m.starts) > 1 {
		hint += " · esc back"
	}
	rows = append(rows, m.footerRows(hint+" · ctrl+p settings · ctrl+c quit")...)
	return strings.Join(rows, "\n")
}

// pickerLine renders one agent row of the picker.
func (m *model) pickerLine(position int) string {
	definition := m.agents[m.picker.shown[position]]
	line := m.row(position == m.picker.cursor, definition.ID)
	if definition.Description == "" {
		return line
	}
	return m.clip(line + "  " + m.styles.dim.Render(definition.Description))
}

// viewSettings renders the command center: the screens it opens, the options
// of the harness and the state of those options.
func (m *model) viewSettings() string {
	rows := m.headerRows(m.settingsIdentity())
	rows = append(rows, "Command center", "")
	rows = append(rows, m.filterRow(&m.commands), "")

	first, last := m.commands.window(m.listRows())
	for position := first; position < last; position++ {
		rows = append(rows, m.commandLine(position))
	}
	if m.commands.empty() {
		rows = append(rows, m.emptyLine("no matches"))
	}

	rows = append(rows, m.footerRows("type to filter · ↑/↓ move · enter run · esc close")...)
	return strings.Join(rows, "\n")
}

// settingsIdentity renders the identity of the command center.
func (m *model) settingsIdentity() string {
	return m.brandIdentity() + m.styles.header.Render(" · settings")
}

// commandLine renders one entry of the command center. The commands that open
// a screen carry no state, so they only show what they do; the options of the
// harness also show whether they are on. A command the interface cannot run
// right now stays faint and takes no highlight, so the list never promises a
// screen it cannot open, and its note says why it cannot run so the reader is
// never left guessing.
func (m *model) commandLine(position int) string {
	entry := commandList[m.commands.shown[position]]
	if entry.Enabled != nil && !entry.Enabled(m) {
		note := entry.Note
		if entry.NoteOff != nil {
			if reason := entry.NoteOff(m); reason != "" {
				note = reason
			}
		}
		return m.clip("  " + m.styles.dim.Render(entry.Label+"  "+note))
	}

	label := m.row(position == m.commands.cursor, entry.Label)
	if entry.IsOn == nil {
		return m.clip(label + "  " + m.styles.dim.Render(entry.Note))
	}

	state := m.styles.off.Render("[off]")
	if entry.IsOn(m.preferences) {
		state = m.styles.on.Render("[on]")
	}
	return m.clip(label + "  " + state + "  " + m.styles.dim.Render(entry.Note))
}

// viewTree renders the tree of the session: the turns of the conversation with
// the branch they belong to, the tags that label them and the turn the session
// is at.
func (m *model) viewTree() string {
	rows := m.headerRows(m.treeIdentity())
	rows = append(rows, "Session tree", "")
	rows = append(rows, m.treeInputRow(), "")

	first, last := m.tree.filter.window(m.listRows())
	for position := first; position < last; position++ {
		rows = append(rows, m.treeLine(position))
	}
	switch {
	case len(m.tree.nodes) == 0:
		rows = append(rows, m.emptyLine("the session holds no turn yet"))
	case m.tree.filter.empty():
		rows = append(rows, m.emptyLine("no matches"))
	}

	rows = append(rows, m.footerRows(m.treeHints())...)
	return strings.Join(rows, "\n")
}

// treeIdentity renders the identity of the tree screen, closing it with the
// legend of the marks that place a turn in the tree. The legend writes both
// marks with the styles the rows use, so it shows what tells the branch from
// the turn the session is at instead of naming two marks that read the same.
func (m *model) treeIdentity() string {
	header := m.styles.header
	return m.brandIdentity() + header.Render(" · tree · ") +
		m.styles.dim.Render(treeBranchMark) + header.Render(" branch · ") +
		m.styles.branch.Bold(true).Render(treeCurrentMark) + header.Render(" current")
}

// treeInputRow renders the input of the tree: the tag of the highlighted turn
// while it is edited, the query that narrows the tree otherwise.
func (m *model) treeInputRow() string {
	if m.tree.editing {
		return m.clip(m.tree.tag.View())
	}
	return m.filterRow(&m.tree.filter)
}

// treeHints returns the keys the tree listens to, or the failure of the last
// change to the session, which takes their place so the screen reports it
// without leaving the tree.
func (m *model) treeHints() string {
	switch {
	case m.tree.err != "":
		return "error: " + m.tree.err + " · esc back"
	case m.tree.editing:
		return "type a tag · enter save · esc cancel"
	default:
		return "↑/↓ move · enter rewind · ctrl+f/a/o fold · ctrl+t tag · esc back"
	}
}

// treeLine renders one turn of the tree: the cursor that opens the highlighted
// row, the gutter that marks the branch the turn belongs to and the turn
// itself. The cursor styles only the message of the turn, so the mark, the tag
// and the author keep the colors that tell them apart while the reader walks
// the tree.
func (m *model) treeLine(position int) string {
	node := m.tree.nodes[m.tree.filter.shown[position]]
	highlighted := position == m.tree.filter.cursor

	cursor := "  "
	if highlighted {
		cursor = "› "
	}
	return m.clip(cursor + m.treeGutter(node) + " " + m.treeTurn(node, highlighted))
}

// treeTurn renders the content of one turn of the tree, without the cursor and
// the gutter that open its row: the connector that places the turn, the tag
// that labels it, its author and its message. The tag leads the turn, before
// the author and the message, so it stays visible however long the message is,
// and the message is cut to the room the row leaves it.
func (m *model) treeTurn(node treeNode, highlighted bool) string {
	line := treeGuides(node.guides) + treeConnector(node)
	if node.entry.Tag != "" {
		line += m.styles.tag.Render("#"+node.entry.Tag) + " "
	}
	line += m.treeNameStyle(node.entry).Render(treeName(node.entry, m.session.Info().Agent))
	line += " " + m.treeMessage(node, highlighted)
	return line
}

// treeMessage returns the message of one turn of the tree, cut to the room its
// row leaves it and written in the color of the highlight when the row holds
// the cursor.
func (m *model) treeMessage(node treeNode, highlighted bool) string {
	text := clipText(m.treeMessageWidth(node), node.text)
	if highlighted {
		return m.styles.selected.Render(text)
	}
	return text
}

// treeMessageWidth returns the columns the message of a turn may take, so a
// long message never floods the tree: it never grows past treeMessageMax, and a
// narrow row shows what the room the gutter leaves it allows. A row too narrow to
// leave the message any room shows it whole, which the terminal clips.
func (m *model) treeMessageWidth(node treeNode) int {
	room := m.width - treeMessageReserve - len(node.guides)*len(treeLevel)
	return max(0, min(treeMessageMax, room))
}

// clipText cuts a line to the given width, keeping it whole when the width is
// zero, which leaves the terminal to clip it.
func clipText(width int, text string) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(text)
}

// treeGuides draws the columns above a turn: a vertical line under every level
// that still holds a turn to close, so the turns of a subtree stay connected to
// the turn they follow, and a gap under every level that holds nothing more.
func treeGuides(guides []bool) string {
	var line strings.Builder
	line.Grow(len(guides) * len(treeLevel))
	for _, open := range guides {
		if open {
			line.WriteString(treeLine)
			continue
		}
		line.WriteString(treeGap)
	}
	return line.String()
}

// treeConnector returns the glyph that opens a turn of the tree: the last turn
// of a group closes it and the turns before it keep it open, while a turn that
// opens a branch holds no connector, because it hangs from nothing. A turn
// whose children are folded carries the glyph that says so, so the reader
// knows a subtree is hidden under it: the turn that opens a branch shows it
// even when it closes no group, so a tree folded down to its roots still shows
// that they hold turns.
func treeConnector(node treeNode) string {
	switch {
	case node.folded && node.last:
		return "⊟─ "
	case node.folded:
		return "⊞─ "
	case node.parent < 0:
		return ""
	case node.last:
		return "└─ "
	default:
		return "├─ "
	}
}

// treeName returns the author of a turn, which the tree shows before the
// message so the reader always knows who wrote it. A checkpoint carries its
// own label instead of an author.
func treeName(entry session.Entry, agent string) string {
	switch {
	case entry.Kind == session.KindCompaction:
		return "Compaction:"
	case entry.Message.Role == llm.RoleUser:
		return "You:"
	default:
		return "Agent (" + agent + "):"
	}
}

// treeNameStyle returns the style of the author of a turn, the color the
// conversation gives the same author.
func (m *model) treeNameStyle(entry session.Entry) lipgloss.Style {
	switch {
	case entry.Kind == session.KindCompaction:
		return m.styles.compaction.title
	case entry.Message.Role == llm.RoleUser:
		return m.styles.user.title
	default:
		return m.styles.assistant.title
	}
}

// treeGutter returns the cell the tree opens the row of a turn with: the dot of
// the turn the session is at, written bright, the dot of the rest of the branch
// the session runs, written faint, and a blank for the turns of the branches
// the session left behind.
func (m *model) treeGutter(node treeNode) string {
	switch {
	case node.current:
		return m.styles.branch.Bold(true).Render(treeCurrentMark)
	case node.active:
		return m.styles.dim.Render(treeBranchMark)
	default:
		return treeGutterGap
	}
}

// viewPreparing renders the session preparation screen. It names what is
// being opened through the label recorded when the preparation started, so it
// never indexes a selection that may not exist.
func (m *model) viewPreparing() string {
	rows := m.headerRows(m.brandIdentity())
	mark := m.styles.dim.Render(m.spinner.View())
	rows = append(rows, mark+" preparing the session of "+m.preparing, "")
	if row, pending := m.pendingQuitRow(); pending {
		rows = append(rows, row)
	}
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
	// The completion popup shares the rows of the conversation, so it only
	// takes the ones the terminal can spare.
	if height := m.mentionHeight(); height > 0 {
		parts = append(parts, m.mentionList(height))
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
	switch m.activityHeight() {
	case 0:
		return ""
	case 1:
		return m.activityLine()
	case noticeRows:
		return "\n" + m.activityLine() + "\n"
	default:
		return "\n\n" + m.activityLine() + "\n"
	}
}

// activityLine renders the single status row that reports the run in flight:
// the spinner and what it is doing, with the key that interrupts it. Once the
// run is over it reports where the conversation goes on, which is where the
// session was moved back to, and stays blank otherwise.
func (m *model) activityLine() string {
	if !m.running {
		switch {
		case m.rewound && m.fork:
			return m.clip(m.styles.notice.Render(
				"↩ rewound · the next message starts a new branch",
			))
		case m.rewound:
			return m.clip(m.styles.notice.Render(
				"↩ rewound · the conversation continues from here",
			))
		}
		return ""
	}

	hint := m.styles.footer.Render(keyEscape + " to interrupt")
	if m.confirm.action == confirmInterrupt {
		hint = m.styles.notice.Render(m.confirm.key + " again to interrupt")
	}
	line := m.styles.dim.Render(m.spinner.View()) + " " +
		m.styles.activity.Render(m.activityLabel()) +
		m.styles.footer.Render(" · ") +
		m.styles.dim.Render(formatElapsed(time.Since(m.runStart))) +
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
	case activityCompacting:
		return "compacting the conversation"
	default:
		return "working"
	}
}

// mentionList renders the completion popup, the suggestions of the mention the
// prompt holds, inside the rows the terminal gives it. The highlighted
// suggestion is the one the user accepts with enter.
func (m *model) mentionList(rows int) string {
	first, last := visibleWindow(m.mention.cursor, len(m.mention.items), rows)

	lines := make([]string, 0, rows)
	for index := first; index < last; index++ {
		lines = append(lines, m.row(index == m.mention.cursor, m.mention.items[index].Path))
	}
	return strings.Join(lines, "\n")
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

// chatFooter renders the fixed row under the input box: the live context
// figure and the keys the interface listens to. The run in flight is reported
// by the activity line above the input, not here.
func (m *model) chatFooter() string {
	parts := make([]string, 0, 3)
	if !m.conversation.atBottom() {
		parts = append(parts, "↑ scrolled")
	}
	if figure := m.contextLabel(); figure != "" {
		parts = append(parts, figure)
	}
	// The keys lead with the ones a session uses all the time, so a narrow
	// terminal cuts the rare ones instead of the ones the reader needs.
	parts = append(parts, "@ files · enter send · ctrl+t tree · ctrl+p settings · ctrl+c quit")

	return m.footerHints(strings.Join(parts, " · "))
}

// contextLabel renders the live context figure of the active branch, colored
// by how much of the window it uses: faint while it fits, a warning past
// contextWarningPercent and a failure past contextCriticalPercent. It is empty
// while the interface holds no measurement.
func (m *model) contextLabel() string {
	if m.context.Window <= 0 {
		return ""
	}

	style := m.styles.footer
	switch {
	case m.context.Percent > contextCriticalPercent:
		style = m.styles.errorText
	case m.context.Percent > contextWarningPercent:
		style = m.styles.notice
	}
	return style.Render(fmt.Sprintf(
		"ctx %d%% · %s/%s",
		m.context.Percent,
		formatTokens(m.context.Used),
		formatTokens(m.context.Window),
	))
}

// formatTokens renders a token count for the chat footer: the plain number
// below a thousand, the thousands with a decimal below a million and the
// millions with a decimal above it, so a small context keeps its precision
// while a window of millions stays short. The decimal of a unit is its tenth —
// a hundred tokens in the thousands and a hundred thousand in the millions —
// and it is dropped when the count is a whole multiple of the unit.
func formatTokens(count int) string {
	if count >= tokensPerM {
		return formatMillions(count)
	}
	if count < tokensPerK {
		return strconv.Itoa(count)
	}

	whole, tenths := roundTenths(count, tokensPerK)
	if whole < tokensPerK {
		return renderScaled(whole, tenths, "k")
	}
	// The rounding reached the next unit, which renders the count as 1m rather
	// than as 1000k.
	return formatMillions(count)
}

// formatMillions renders a token count in millions, with the decimal of the
// unit.
func formatMillions(count int) string {
	whole, tenths := roundTenths(count, tokensPerM)
	return renderScaled(whole, tenths, "m")
}

// roundTenths returns a count in units of scale, split into its whole part and
// its number of tenths and rounded to the nearest tenth. The arithmetic stays
// in integers, which keeps a count that lands off a unit boundary from
// rendering as the unit below it.
func roundTenths(count, scale int) (whole, tenths int) {
	step := scale / 10
	rounded := (count + step/2) / step
	return rounded / 10, rounded % 10
}

// renderScaled renders a value in units of unit, dropping the decimal when it
// carries none.
func renderScaled(whole, tenths int, unit string) string {
	if tenths == 0 {
		return strconv.Itoa(whole) + unit
	}
	return strconv.Itoa(whole) + "." + strconv.Itoa(tenths) + unit
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
// The entry that closes a run also carries the time the turn took, shown as a
// faint footnote under its content.
func (m *model) renderBlock(index int) string {
	current := &m.transcript.entries[index]
	block := m.renderBlockBody(current, m.width)
	if current.elapsed > 0 {
		block += "\n\n" + m.renderElapsed(current.elapsed)
	}
	return block
}

// renderBlockBody renders the content of a transcript entry without the
// footnote that closes a turn.
func (m *model) renderBlockBody(current *entry, width int) string {
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
	case entryCompaction:
		return m.styles.compaction.block(width, "Compaction", current.text())
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

// renderElapsed renders how long a turn took as a faint footnote under the last
// message of the turn, so the reader sees the time the agent worked at the end
// of every answer.
func (m *model) renderElapsed(elapsed time.Duration) string {
	return m.clip(m.styles.dim.Render("took " + formatElapsed(elapsed)))
}

// divider returns the rule that separates two conversation blocks.
func (m *model) divider() string {
	return strings.Join([]string{"", m.ruleLine(m.styles.divider), ""}, "\n")
}

// assistantName returns the label shown for the answers of the agent, naming
// the agent they belong to.
func (m *model) assistantName() string {
	return "Agent: " + m.session.Info().Agent
}

// renderThinkingEntry renders a reasoning block: the whole text when the
// preference expands it, a preview of its trailing lines otherwise. The run in
// flight is reported by the activity line, so the block carries no spinner of
// its own.
func (m *model) renderThinkingEntry(current *entry, width int) string {
	body := m.blockBody(current.text(), m.preferences.ExpandThinking, width)
	return m.styles.thinking.block(width, "Agent: thinking", body)
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

	label := name.Render("Tool: " + current.toolName)
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

// wrap wraps plain text to width columns, keeping words together when
// possible. It runs on the plain text of a block, before the style of the
// block is applied, so it does not need to track styles.
func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	return ansi.Wrap(text, width, "")
}
