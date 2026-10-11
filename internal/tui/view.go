package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/version"
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

// View renders the interface in the alternate screen. Mouse reporting is
// enabled so the wheel scrolls the conversation instead of reaching the prompt
// as arrow keys, which is all the interface does with the mouse.
func (m *model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
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
	case m.phase == phaseConfirm:
		return m.viewConfirm()
	case m.phase == phasePreparing:
		return m.viewPreparing()
	default:
		return m.viewChat()
	}
}

// brandIdentity renders the logo, the name of the interface, its build version
// and the name of the workspace it runs in, which open the identity line of
// every phase. The logo, the version and the workspace stay static and share
// the faint color of the header text, so they read as part of the identity
// rather than drawing the eye, while the name carries the emphasis; the status
// line carries the only animation of the interface. The workspace is left out
// when it could not be located, so the line never carries an empty segment.
func (m *model) brandIdentity() string {
	line := m.styles.header.Render(varavelLogo+" · ") +
		m.styles.title.Render(brand) +
		m.styles.header.Render(" · "+brandVersion())
	if m.workspace != "" {
		line += m.styles.header.Render(" · " + m.workspace)
	}
	return line
}

// brandVersion returns the build version that closes the identity of the
// interface, tagged so it reads as a version. A development build shows the
// placeholder of the version package untagged, because it carries no release
// number to tag.
func brandVersion() string {
	number := version.Number()
	if number == "dev" {
		return number
	}
	return "v" + number
}

// headerRows returns the rows that open a phase: the identity the phase shows,
// wrapped to the terminal width and trimmed to what it affords, the separator
// under it and the padding below the separator.
func (m *model) headerRows() []string {
	rows := m.cappedIdentity()
	rows = append(rows, m.ruleLine(m.styles.separator), "")
	return rows
}

// footerRows returns the rows that close a phase: the separator between two
// blank rows and the hints of the phase.
func (m *model) footerRows(hints string) []string {
	return []string{"", m.ruleLine(m.styles.separator), "", m.footerHints(hints)}
}

// startHints names the keys the start list answers with, so the list shows
// them and its measurement reads the same text.
func (m *model) startHints() string {
	hint := "type to filter · ↑/↓ move · enter open"
	if m.session != nil {
		hint += " · esc back"
	}
	return hint + " · ctrl+p settings · ctrl+c quit"
}

// footerHints renders the closing hint row of a phase: the hints the phase
// spells out, or the quit request that is pending, which takes their place so
// the reader always sees what the next press does however long the hints are
// and however narrow the terminal is.
func (m *model) footerHints(hints string) string {
	if row, pending := m.pendingQuitRow(); pending {
		return row
	}
	lines := wrapChrome(m.styles.footer.Render(hints), m.width)
	limit := len(m.cappedHints())
	return strings.Join(lines[:min(len(lines), limit)], "\n")
}

// pendingQuitRow renders the row that asks for the press that leaves the
// interface, which is only shown while a quit request waits for it: the phases
// that close with a footer show it in the place of the hints, and the ones that
// close without one, like the preparation screen, show it on its own. It names
// the key that armed the request, so the reader is asked for the press of the
// key that is already under their finger.
// quitRowText names the press that leaves the interface while a quit request
// waits for it, so the row that asks for it and its measurement read the
// same text.
func (m *model) quitRowText() string {
	return m.confirm.key + " again to quit"
}

func (m *model) pendingQuitRow() (string, bool) {
	if m.confirm.action != confirmQuit {
		return "", false
	}
	lines := wrapChrome(m.styles.notice.Render(m.quitRowText()), m.width)
	limit := len(m.cappedHints())
	return strings.Join(lines[:min(len(lines), limit)], "\n"), true
}

// viewStart renders the list that opens the interface: the offer to begin a
// new session and the previous sessions of the workspace, narrowed by the
// query typed into the list.
func (m *model) viewStart() string {
	rows := m.headerRows()
	rows = append(rows, "Start a new session or continue a previous one", "")
	rows = append(rows, m.filterRow(&m.start), "")

	first, last := m.start.window()
	for position := first; position < last; position++ {
		rows = append(rows, m.startLine(position))
	}
	if m.start.empty() {
		rows = append(rows, m.emptyLine("no matches"))
	}

	// The list only returns to the conversation it was opened over when there
	// is one to return to.
	rows = append(rows, m.footerRows(m.startHints())...)
	return strings.Join(rows, "\n")
}

// startLine renders one entry of the start list: the offer of a new session or
// a stored session with its agent and age. The entries the list offers are the
// ones it read last, so a session created while the interface runs shows up
// once the list opens again.
//
// A session whose agent definition is gone is offered like any other, marked so
// the reader knows before opening it that the conversation is read but not run
// until another agent is selected.
func (m *model) startLine(position int) string {
	item := m.starts[m.start.shown[position]]
	if item.newSession {
		return m.row(position == m.start.cursor, "New session")
	}

	info := item.info
	agent := sessionAgent(info)
	details := m.styles.dim.Render(agent + " · " + formatAge(info.UpdatedAt))
	line := m.row(position == m.start.cursor, sessionTitle(info)) + "  " + details
	if !m.knowsAgent(agent) {
		line += "  " + m.styles.notice.Render("missing agent")
	}
	return m.clip(line)
}

// sessionAgent returns the agent a stored session runs, falling back to the
// agent of its header for a session whose metadata does not resolve the active
// branch, which is the case of the ones the interface was handed directly.
func sessionAgent(info session.Info) string {
	if info.ActiveAgent != "" {
		return info.ActiveAgent
	}
	return info.Agent
}

// knowsAgent reports whether the interface holds a definition for an agent, so
// the start list can mark a session whose agent is gone without opening it.
func (m *model) knowsAgent(id string) bool {
	return slices.ContainsFunc(m.agents, func(definition agent.Agent) bool {
		return definition.ID == id
	})
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

// cursorMark returns the gutter that opens a list row: the arrow that marks
// the row the selection rests on, or a blank of the same width. It is defined
// once so every list keeps its rows aligned, and it stays visible on a row the
// user cannot activate, because the reader must always see where the selection
// is.
func cursorMark(selected bool) string {
	if selected {
		return "› "
	}
	return "  "
}

// row renders one list row, highlighted when it holds the cursor.
func (m *model) row(highlighted bool, label string) string {
	if highlighted {
		return m.clip(cursorMark(true) + m.styles.selected.Render(label))
	}
	return m.clip(cursorMark(false) + label)
}

// pinMark opens the row of a pinned model, the solid glyph the tree already
// uses for its own marks, so the two lists read alike.
const pinMark = "●"

// pinPrefix returns what opens a row of the model list: the pin mark for a
// pinned model, and a blank of the same width for any other entry, so every
// row starts at the same column. It is nothing outside the model list.
func (m *model) pinPrefix(entry string) string {
	if m.pickerMode != pickerModel {
		return ""
	}
	if slices.Contains(m.pinned, entry) {
		return pinMark + " "
	}
	// The blank matches the mark in columns, not in bytes: the glyph is more
	// than one byte long.
	return strings.Repeat(" ", utf8.RuneCountInString(pinMark)+1)
}

// listRows returns the list rows that fit on screen outside the chrome of the
// list phase in progress, measured at the terminal width.
func (m *model) listRows() int {
	chrome := m.headerBlockRows() + listPromptRows + listFilterRows +
		listFooterFixedRows + m.hintLines()
	return max(1, m.height-chrome)
}

// listIdentity returns the identity the list phase in progress shows, so the
// list measures the header it renders.
func (m *model) listIdentity() string {
	switch m.phase {
	case phasePicker:
		return m.pickerIdentity()
	case phaseSettings:
		return m.settingsIdentity()
	case phaseTree:
		return m.treeIdentity()
	case phaseConfirm:
		return m.confirmIdentity()
	default:
		return m.brandIdentity()
	}
}

// listHints returns the hints the list phase in progress shows, so the list
// measures the footer it renders.
func (m *model) listHints() string {
	switch m.phase {
	case phasePicker:
		return m.pickerHints()
	case phaseSettings:
		return m.settingsHints()
	case phaseTree:
		return m.treeHints()
	case phaseConfirm:
		return confirmHints
	default:
		return m.startHints()
	}
}

// treeRows returns the list rows the tree shows at once: the rows that fit
// outside its fixed lines, less the row the virtual root occupies above the
// turns of the conversation, so the root never pushes a turn out of the
// window. It keeps a single row, which the window still fills with the turns
// it can.
func (m *model) treeRows() int {
	return max(1, m.listRows()-1)
}

// visibleWindow returns the range of a list of total items that fits in rows
// while keeping the cursor visible, with margin rows always kept below it so the
// reader sees which entries come next instead of running the highlight into the
// bottom edge. The margin is given up only when the list has fewer rows left to
// show than it asks for, so the window still fills with entries and the cursor
// never leaves the screen.
func visibleWindow(cursor, total, rows, margin int) (first, last int) {
	if total <= rows {
		return 0, total
	}
	margin = min(max(margin, 0), rows-1)
	first = min(max(cursor-rows+1+margin, 0), total-rows)
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

// viewPicker renders the list the picker offers, narrowed by the query typed
// into it. The list serves three purposes, and it says which one it is serving:
// choosing the agent of a new session, changing the agent of the open
// conversation, or changing its model.
func (m *model) viewPicker() string {
	rows := m.headerRows()
	rows = append(rows, m.pickerTitle(), "")
	rows = append(rows, m.filterRow(&m.picker), "")

	first, last := m.picker.window()
	for position := first; position < last; position++ {
		rows = append(rows, m.pickerLine(position))
	}
	if m.picker.empty() {
		rows = append(rows, m.emptyLine("no matches"))
	}

	// The picker only returns to the start list when it was opened from it,
	// which is the case whenever the workspace holds a previous session and
	// the list is not changing what the open conversation runs.
	rows = append(rows, m.footerRows(m.pickerHints())...)
	return strings.Join(rows, "\n")
}

// pickerIdentity renders the identity of the picker screen, closing it with
// the legend of the pin mark while it lists models. The legend writes the
// mark the way the rows show it, so the reader meets the same glyph in both
// places instead of guessing what opens a pinned row.
func (m *model) pickerIdentity() string {
	if m.pickerMode != pickerModel {
		return m.brandIdentity()
	}
	header := m.styles.header
	return m.brandIdentity() + header.Render(" · ") +
		pinMark + header.Render(" pinned model")
}

// pickerHints names the keys the picker answers with, so the list shows
// them and its measurement reads the same text.
func (m *model) pickerHints() string {
	hint := "type to filter · ↑/↓ move · enter select"
	if m.pickerMode == pickerModel {
		hint += " · ctrl+x p pin/unpin"
	}
	if m.pickerMode == pickerNewAgent && len(m.starts) > 1 {
		hint += " · esc back"
	}
	return hint + " · ctrl+p settings · ctrl+c quit"
}

// pickerTitle names what the picker is asking for, so the reader never doubts
// whether choosing an entry starts a conversation or changes what the one in
// front of them runs.
func (m *model) pickerTitle() string {
	switch m.pickerMode {
	case pickerAgent:
		return "Switch the agent of the conversation"
	case pickerModel:
		return "Switch the model of the conversation"
	default:
		return "Select an agent"
	}
}

// pickerLine renders one row of the picker. The entry the conversation already
// runs is marked, so changing what it runs shows where the session stands
// before the user moves the highlight. An agent carries a description of its
// own and a model the one the configuration declares for it, so the reader sees
// what a model is before switching to it.
func (m *model) pickerLine(position int) string {
	index := m.picker.shown[position]
	entry := m.roster()[index]

	label := entry
	if m.pickerMode == pickerModel && m.session != nil {
		label = modelLabel(entry, m.session.ModelInfo(entry))
	}

	// A pinned model opens its row with the pin marker, and a model that is
	// not pinned opens it with a blank of the same width, so the rows of the
	// list stay aligned whatever the user pins.
	line := m.row(position == m.picker.cursor, m.pinPrefix(entry)+label)
	if m.pickerMode != pickerNewAgent && m.session != nil && entry == m.activeRef() {
		line += "  " + m.styles.on.Render("current")
	}
	// Only the agents carry a description of their own: the index of a model
	// row names the model roster, which the agent slice does not.
	if m.pickerMode != pickerModel {
		if description := m.agents[index].Description; description != "" {
			line += "  " + m.styles.dim.Render(description)
		}
	}
	return m.clip(line)
}

// modelNamer returns a function that names a model reference the way the
// interface reads it: the provider and the model as display names, or the
// reference when the configuration no longer holds it. It is handed to the
// tree and the conversation so a switch reads with the names the user sees
// everywhere else instead of the raw references the session stores. The
// lookup reads the immutable declarations, so it is safe to call while a run
// holds the session.
func (m *model) modelNamer() modelNameFunc {
	return func(ref string) string {
		if m.session == nil {
			return ref
		}
		return modelSwitchName(ref, m.session.ModelInfo(ref))
	}
}

// modelName returns the name the interface shows a model by on its own, without
// the reference that addresses it: the wire identifier the provider receives,
// which is the real model, together with the extended thinking level the
// configuration declares, as in "deepseek-v4.1 max". A reference the
// configuration no longer holds falls back to the reference itself, so a model
// that is gone still reads as something.
func modelName(ref string, info engine.ModelInfo) string {
	if description := modelDescription(info); description != "" {
		return description
	}
	return ref
}

// modelLabel renders a model reference with the display name the provider
// module declares for it, falling back to the wire identifier, as in
// "customprovider: DeepSeek v4.1" or "opencode-go: gpt-5.2", which is what the
// picker shows so a selection reads what it would run. A model the
// configuration no longer holds reads as the reference alone.
func modelLabel(ref string, info engine.ModelInfo) string {
	if description := modelDescription(info); description != "" {
		return providerLabel(ref, info) + ": " + description
	}
	return ref
}

// modelSwitchName renders a model reference as one half of a switch marker:
// the provider and the model, joined the way the header reads them, so a line
// that shows two models names both the same way the rest of the interface
// does. A reference the configuration no longer holds falls back to the
// reference itself, so a switch that is gone still reads as something.
func modelSwitchName(ref string, info engine.ModelInfo) string {
	if info.ID == "" {
		return ref
	}
	return providerLabel(ref, info) + " · " + modelDescription(info)
}

// providerLabel returns the name the interface shows the provider of a model
// by: the display name its module declares, or the module name when it
// declares none. A reference the configuration no longer holds falls back to
// the module name its two halves carry, so a model that is gone still reads
// under the provider it belonged to.
func providerLabel(ref string, info engine.ModelInfo) string {
	if name := info.Provider; name != "" {
		return name
	}
	return providerOf(ref)
}

// providerOf returns the provider name, the first half of a model reference.
// A reference that fails to split reads whole, so a malformed entry still
// shows something.
func providerOf(ref string) string {
	provider, _, _ := strings.Cut(ref, "/")
	if provider == "" {
		return ref
	}
	return provider
}

// modelDescription returns what describes a model beyond the reference that
// addresses it: the display name the provider module declares, falling back
// to the wire identifier. It is empty when the configuration no longer holds
// the reference, so a caller falls back to the reference rather than showing
// a bare separator.
func modelDescription(info engine.ModelInfo) string {
	if name := info.Name; name != "" {
		return name
	}
	return info.ID
}

// viewSettings renders the command center: the screens it opens, the options
// of the harness and the state of those options.
func (m *model) viewSettings() string {
	rows := m.headerRows()
	rows = append(rows, "Command center", "")
	rows = append(rows, m.settingsInputRow(), "")

	first, last := m.commands.window()
	for position := first; position < last; position++ {
		rows = append(rows, m.commandLine(position))
	}
	if m.commands.empty() {
		rows = append(rows, m.emptyLine("no matches"))
	}

	rows = append(rows, m.footerRows(m.settingsHints())...)
	return strings.Join(rows, "\n")
}

// settingsIdentity renders the identity of the command center.
func (m *model) settingsIdentity() string {
	return m.brandIdentity() + m.styles.header.Render(" · settings")
}

// settingsInputRow renders the input of the command center: the name of the
// session while it is edited, the query that narrows the commands otherwise.
func (m *model) settingsInputRow() string {
	if m.renaming {
		return m.clip(m.rename.View())
	}
	return m.filterRow(&m.commands)
}

// settingsHints returns the keys the command center listens to, or the failure
// of the last change to the session, which takes their place so the screen
// reports it without leaving the command center.
func (m *model) settingsHints() string {
	switch {
	case m.renameErr != "":
		return "error: " + m.renameErr + " · esc cancel"
	case m.renaming:
		return "type a name · enter save · esc cancel"
	default:
		return "type to filter · ↑/↓ move · enter run · esc close"
	}
}

// commandLine renders one entry of the command center. The commands that open
// a screen carry no state, so they only show what they do; the options of the
// harness also show whether they are on. A command the interface cannot run
// right now stays faint and never takes the highlight, so the list never
// promises a screen it cannot open, and its note says why it cannot run so the
// reader is never left guessing. It keeps the cursor marker, because the reader
// must always see where the selection is.
func (m *model) commandLine(position int) string {
	entry := commandList[m.commands.shown[position]]
	if entry.Enabled != nil && !entry.Enabled(m) {
		note := entry.Note
		if entry.NoteOff != nil {
			if reason := entry.NoteOff(m); reason != "" {
				note = reason
			}
		}
		return m.clip(cursorMark(position == m.commands.cursor) +
			m.styles.dim.Render(entry.Label+"  "+note))
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
	rows := m.headerRows()
	rows = append(rows, "Session tree", "")
	rows = append(rows, m.treeInputRow(), "")

	first, last := m.tree.filter.window()
	if len(m.tree.nodes) > 0 && !m.tree.filter.empty() {
		rows = append(rows, m.treeRootRow(first == 0))
	}
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

// treeRootRow renders the virtual root the tree hangs from: the faint label
// that stands for the origin of the conversation, drawn above the first turn
// so a conversation that opens several branches shows them born from one turn
// instead of reading as sibling turns with no parent. It carries no cursor and
// no mark, because it is not a turn the reader can reach: it only places the
// tree on a root. Its label is aligned with the content of the turns, so the
// root reads directly above the column the first turn opens.
//
// The row is rendered even once the window scrolled past the first turn, as a
// blank one, so the tree keeps the same rows whether the root is on screen or
// not and the reader never sees the screen shift under it.
func (m *model) treeRootRow(shown bool) string {
	if !shown {
		return ""
	}
	return m.clip(cursorMark(false) + treeGutterGap + " " + m.styles.dim.Render(treeRootLabel))
}

// treeLine renders one turn of the tree: the cursor that opens the highlighted
// row, the gutter that marks the branch the turn belongs to and the turn
// itself. The cursor styles only the message of the turn, so the mark, the tag
// and the author keep the colors that tell them apart while the reader walks
// the tree.
func (m *model) treeLine(position int) string {
	node := m.tree.nodes[m.tree.filter.shown[position]]
	highlighted := position == m.tree.filter.cursor

	return m.clip(
		cursorMark(highlighted) + m.treeGutter(node) + " " + m.treeTurn(node, highlighted),
	)
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
	line += m.treeNameStyle(node.entry).Render(treeName(node))
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
// narrow row shows what the room left by the guides and the connector of its
// level allows, which is measured from the glyphs really drawn so a turn of the
// trunk is not cut short by the column it does not open. A row too narrow to
// leave the message any room shows it whole, which the terminal clips.
func (m *model) treeMessageWidth(node treeNode) int {
	prefix := utf8.RuneCountInString(treeGuides(node.guides)) +
		utf8.RuneCountInString(treeConnector(node))
	return max(0, min(treeMessageMax, m.width-treeRowReserve-prefix))
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

// treeConnector returns the glyph that opens a turn of the tree, placing it in
// the branch it belongs to: the last turn of a group closes it and the turns
// before it keep it open, while a turn that continues the one before it only
// draws the column it shares with it, or nothing at all when it opens the tree
// from the virtual root. The first turns of a conversation that opened several
// of them hang from the root, so each one draws the elbow that branches it
// beside the others. A continuation whose column closes draws a blank of its
// width, so its message stays aligned with the turn it continues. A turn whose
// children are folded carries the glyph that says so, so the reader knows a
// subtree is hidden under it: the turn that opens a branch shows it even when
// it closes no group, so a tree folded down to its roots still shows that they
// hold turns.
func treeConnector(node treeNode) string {
	switch {
	case node.folded && node.open:
		return "⊞─ "
	case node.folded:
		return "⊟─ "
	case node.parent < 0 && len(node.guides) == 0:
		return ""
	case node.continued && len(node.guides) == 0:
		return ""
	case node.continued && node.open:
		return treeLine
	case node.continued:
		return treeGap
	case node.open:
		return "├─ "
	default:
		return "└─ "
	}
}

// treeName returns the author of a turn, which the tree shows before the
// message so the reader always knows who wrote it. An answer names the agent
// that wrote it, which is the agent the branch ran at that turn, so a
// conversation that changed agent shows who wrote what. A checkpoint and a
// selection of what the branch runs carry a label of their own instead of an
// author.
func treeName(node treeNode) string {
	switch {
	case node.entry.Kind == session.KindCompaction:
		return "Compaction:"
	case isSelection(node.entry.Kind):
		return selectionLabel(node.entry.Kind) + ":"
	case node.entry.Message.Role == llm.RoleUser:
		return "You:"
	default:
		return "Agent (" + node.agent + "):"
	}
}

// treeNameStyle returns the style of the author of a turn, the color the
// conversation gives the same author. A checkpoint and a selection of what the
// branch runs are secondary metadata rather than voices of the conversation, so
// both take the faint style the conversation also gives them.
func (m *model) treeNameStyle(entry session.Entry) lipgloss.Style {
	switch {
	case entry.Kind == session.KindCompaction, isSelection(entry.Kind):
		return m.styles.metadata.title
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
	rows := m.headerRows()
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
		strings.Join(m.headerRows(), "\n"),
		m.conversation.view(),
	}
	// A status block the terminal cannot afford contributes no row at all.
	if m.activityHeight() > 0 {
		parts = append(parts, m.activityBlock())
	}
	// The completion popups share the rows of the conversation, so they only
	// take the ones the terminal can spare. Only one popup is ever active:
	// the slash token completes on whitespace and hands the prompt to mentions.
	if height := m.slashHeight(); height > 0 {
		parts = append(parts, m.slashList(height))
	}
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
		if m.status != "" {
			return m.clip(m.styles.notice.Render(m.status))
		}
		switch {
		case m.runnable != nil:
			return m.clip(m.styles.notice.Render(
				"⚠ " + m.stoppedLabel() + " · pick other before continuing the conversation",
			))
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

// stoppedLabel explains why the branch the conversation shows holds nothing to
// run, naming the agent or the model the branch runs that the roster no longer
// holds. It is the notice the status line shows instead of a run in flight, so
// a conversation whose agent disappeared says what to do about it.
func (m *model) stoppedLabel() string {
	switch m.runnable.Kind {
	case engine.RunnableUnknownModel:
		return "the " + m.runnable.ID + " model is gone"
	default:
		return "the " + m.runnable.ID + " agent is gone"
	}
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
	// The completion popup needs no margin: every suggestion is one key away.
	first, last := visibleWindow(m.mention.cursor, len(m.mention.items), rows, 0)

	lines := make([]string, 0, rows)
	for index := first; index < last; index++ {
		lines = append(lines, m.row(index == m.mention.cursor, m.mention.items[index].Path))
	}
	return strings.Join(lines, "\n")
}

// identity is what the chat header shows of the open session: the agent the
// branch runs, the model it runs and, when the user named it, the name. It is a
// value the interface caches, so the header renders without reading the session,
// which a run holds while it works.
type identity struct {
	// agent is the agent the branch runs.
	agent string

	// provider names the provider the model of the branch belongs to: the
	// display name its module declares, or the module name when it declares
	// none. It is empty when the configuration no longer holds the model.
	provider string

	// model names the model the branch runs: its wire identifier, which is
	// the real model the provider receives, or the reference that addresses
	// it when the configuration no longer holds one.
	model string

	// thinking names the thinking mode the branch runs: the level the user
	// picked, empty for off, which the header shows as its own segment.
	thinking string

	// named reports that the user named the session, so title is the name the
	// user chose rather than the one derived from the first message.
	named bool

	// title is the name of the session, meaningful when named is set.
	title string
}

// identityFrom reads the identity of a session, called from the moments the
// identity changes: the transcript is rebuilt and the session is named. The
// store serializes the read, so it is safe even while a run is in flight.
func identityFrom(s Session) identity {
	info := s.Info()
	ref := s.ActiveModel()
	model := s.ModelInfo(ref)
	return identity{
		agent:    s.ActiveAgent(),
		provider: model.Provider,
		model:    modelName(ref, model),
		thinking: s.ActiveThinking(),
		named:    info.Named,
		title:    info.Title,
	}
}

// chatIdentity renders the identity of the session: the brand followed by the
// agent, the provider and model the branch runs, the thinking mode and, when
// the user named it, the name, which closes the line so the reader sees the
// name the session is found under in the list. The provider leads the model,
// so which account a model belongs to reads before the model itself. The
// thinking mode is always on the line, off included, so a cycle or a selection
// reads where the conversation stands without leaving it. It renders the
// cached identity, so it never reads the session.
func (m *model) chatIdentity() string {
	thinking := m.identity.thinking
	if thinking == "" {
		thinking = thinkingOff
	}
	parts := []string{m.identity.agent}
	if m.identity.provider != "" {
		// The provider leads the model, so which account a model belongs to
		// reads before the model itself.
		parts = append(parts, m.identity.provider)
	}
	parts = append(parts, m.identity.model, "thinking "+thinking)
	if m.identity.named {
		parts = append(parts, m.identity.title)
	}
	return m.brandIdentity() + m.styles.header.Render(" · "+strings.Join(parts, " · "))
}

// viewConfirm renders the question an extension asked: what asks, what it
// wants and the choices, which the user moves through with the arrows and
// answers with enter. It is a screen of its own rather than a dialog over the
// chat, so the answer is deliberate and the run visibly waits.
func (m *model) viewConfirm() string {
	rows := m.headerRows()
	rows = append(rows, m.confirmScreen.title, "")
	for line := range strings.SplitSeq(wrap(m.confirmScreen.body, max(1, m.width-2)), "\n") {
		rows = append(rows, m.clip(line))
	}
	rows = append(rows, "")

	for index, choice := range confirmChoices {
		rows = append(rows, m.row(index == m.confirmScreen.cursor, choice))
	}

	rows = append(rows, m.footerRows(confirmHints)...)
	return strings.Join(rows, "\n")
}

// confirmHints names the keys the question screen answers with, so it shows
// them and its measurement reads the same text.
const confirmHints = "↑/↓ move · enter select · esc deny · ctrl+c quit"

// confirmIdentity renders the identity of the question screen.
func (m *model) confirmIdentity() string {
	return m.brandIdentity() + m.styles.header.Render(" · confirm")
}

// viewInput renders the prompt input inside a box.
func (m *model) viewInput() string {
	return m.styles.inputBox.Width(max(1, m.width)).Render(m.input.View())
}

// chatFooter renders the fixed row under the input box: the live context
// figure and the keys the interface listens to. The run in flight is reported
// by the activity line above the input, not here.
func (m *model) chatFooter() string {
	return m.footerHints(m.chatFooterText())
}

// chatFooterText joins the segments the chat footer shows, so the footer shows
// them and its measurement reads the same text.
func (m *model) chatFooterText() string {
	parts := make([]string, 0, 3)
	if !m.conversation.atBottom() {
		parts = append(parts, "↑ scrolled")
	}
	if figure := m.contextLabel(); figure != "" {
		parts = append(parts, figure)
	}
	// The keys lead with the ones a session uses all the time, so a narrow
	// terminal cuts the rare ones instead of the ones the reader needs.
	parts = append(
		parts,
		"@ files · enter send · ctrl+x select · ctrl+t tree · ctrl+p · ctrl+c quit",
	)

	return strings.Join(parts, " · ")
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
		"ctx %s%% · %s/%s",
		formatPercent(m.context.Percent),
		formatTokens(m.context.Used),
		formatTokens(m.context.Window),
	))
}

// formatPercent renders a percentage with the one decimal the footer shows,
// dropping it when the figure is whole so a round number reads as a round
// number. The measurement already carries the rounding, so the only job here is
// to hide a zero decimal.
func formatPercent(percent float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(percent, 'f', 1, 64), ".0")
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
		return m.renderCompaction(current, width)
	case entrySwitch:
		return m.renderSwitch(current, width)
	default:
		return m.styles.failure.block(width, "Error", current.text())
	}
}

// metadataRow renders a row of metadata that separates two turns of the
// conversation, a checkpoint or a selection of what the branch runs: the prefix,
// which already carries the marker that opens the row and the styled label that
// names it, and the body the row carries, all on a single line. The row wraps
// inside the given width, so a long body never outgrows the terminal.
func metadataRow(prefix, body string, width int) string {
	row := prefix + ": " + body
	if width <= 0 {
		return row
	}
	return lipgloss.Wrap(row, width, "")
}

// renderCompaction renders a checkpoint as a row of metadata between two turns,
// exactly as a switch is drawn, so the conversation shows where it was
// summarized without devoting a block of its own to it: the thick marker that
// opens a turn, the faint label of the checkpoint and the note that closes it,
// all on a single line.
func (m *model) renderCompaction(current *entry, width int) string {
	prefix := m.styles.metadata.mark(m.styles.metadata.title.Render("Compaction"))
	return metadataRow(prefix, m.styles.metadata.body.Render(current.text()), width)
}

// renderSwitch renders a selection of what the branch runs as a row of metadata
// between two turns, exactly as a checkpoint is drawn, so a switch is never lost
// by reading the conversation instead of the tree: the thick marker that opens a
// turn, the faint label that names the switch and the transition the selection
// wrote, all on a single line.
//
// The transition keeps the plain style the rest of the row does not, so the
// values the switch replaced and selected stay readable beside the faint label.
func (m *model) renderSwitch(current *entry, width int) string {
	prefix := m.styles.metadata.title.Render(markerTurn) + " " +
		m.styles.metadata.title.Render(selectionLabel(current.selectionKind))
	return metadataRow(prefix, current.text(), width)
}

// renderAssistantBlock renders an answer of the model, formatted as markdown
// so headings, emphasis, code, lists and links read the way the model wrote
// them.
func (m *model) renderAssistantBlock(current *entry, width int) string {
	if !m.preferences.RenderMarkdown {
		return m.styles.assistant.block(width, m.assistantName(current.agent), current.text())
	}

	body := m.markdown.render(current.text(), width, m.hasDarkBG)
	label := m.styles.assistant.title.Render(m.assistantName(current.agent))
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

// assistantName returns the label shown for the answers of an agent, naming
// the agent that wrote them.
func (m *model) assistantName(agent string) string {
	return "Agent: " + agent
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

// fullIdentityLines wraps the chat identity to the terminal width with no
// affordability trim, so the room checks that decide what the terminal gives
// up measure the full need without calling back into the capped chrome,
// which would recurse through the caps.
func (m *model) fullIdentityLines() []string {
	return wrapChrome(m.chatIdentity(), m.width)
}

// fullHintLines wraps the chat hints to the terminal width with no
// affordability trim, following the pending-quit row exactly as the render
// does, for the same reason the full identity lines exist.
func (m *model) fullHintLines() []string {
	if m.confirm.action == confirmQuit {
		return wrapChrome(m.quitRowText(), m.width)
	}
	return wrapChrome(m.chatFooterText(), m.width)
}

// chromeSource returns the identity and hints the phase in progress shows, so
// the render and the measurement of the chrome read the same text.
func (m *model) chromeSource() (identity, hints string) {
	if m.phase == phaseChat {
		return m.chatIdentity(), m.chatFooterText()
	}
	return m.listIdentity(), m.listHints()
}

// chromeCaps trims the wrapped identity and hint needs to what the terminal
// affords: the full need while it fits, fewer only when the chrome plus the
// content floors outgrow the screen. The content floors (one transcript or
// list row, the input) win over the chrome, so a tiny terminal trims the
// chrome instead of pushing the prompt off screen. Both caps keep at least
// one line, which the rows the chrome renders always spend.
func (m *model) chromeCaps(identityWant, hintWant int) (identityCap, hintCap int) {
	fixed := listPromptRows + listFilterRows + listFooterFixedRows + 1
	if m.phase == phaseChat {
		// Completion popups share the rows of the conversation, never the
		// rows of the chrome: they shrink the transcript through
		// transcriptHeight, so counting them here would trim the footer
		// every time a popup opens.
		fixed = inputBoxRows + m.input.Height() + 1 + m.activityHeight()
	}
	room := m.height - fixed
	identityCap = min(identityWant, max(1, room-headerFixedRows-1))
	identityCap = max(1, identityCap)
	hintCap = min(hintWant, max(1, room-(identityCap+headerFixedRows)))
	hintCap = max(1, hintCap)
	return identityCap, hintCap
}

// cappedChrome wraps the identity and hints the phase shows to the terminal
// width, trimmed to what it affords, so the render and the measurement share
// the exact lines. While a quit request waits, the hints give way to the quit
// row here exactly as they do in the render.
func (m *model) cappedChrome() (identityLines, hintLines []string) {
	identity, hints := m.chromeSource()
	if m.confirm.action == confirmQuit {
		hints = m.quitRowText()
	}
	identityWant := len(wrapChrome(identity, m.width))
	hintWant := len(wrapChrome(hints, m.width))
	identityCap, hintCap := m.chromeCaps(identityWant, hintWant)
	return wrapChrome(identity, m.width)[:identityCap],
		wrapChrome(hints, m.width)[:hintCap]
}

// cappedIdentity wraps the identity the phase shows, trimmed to what the
// terminal affords.
func (m *model) cappedIdentity() []string {
	lines, _ := m.cappedChrome()
	return lines
}

// cappedHints wraps the hints the phase shows, trimmed to what the terminal
// affords.
func (m *model) cappedHints() []string {
	_, lines := m.cappedChrome()
	return lines
}

// headerBlockRows returns the rows the header spends at the terminal width:
// the wrapped identity lines plus the separator and the blank row under it.
func (m *model) headerBlockRows() int {
	return len(m.cappedIdentity()) + headerFixedRows
}

// hintLines returns the rows the footer hints spend at the terminal width,
// following the pending-quit row when it takes the hints' place.
func (m *model) hintLines() int {
	return len(m.cappedHints())
}

// chromeSeparator joins the segments of a header identity or a footer hint.
const chromeSeparator = " · "

// wrapChrome wraps one styled chrome line (a header identity or a footer hint)
// into screen lines. It folds whole segments at their separators before
// breaking inside words, so a narrow terminal reads complete entries on their
// own lines. A segment longer than the width still hard-breaks, so the wrap
// always terminates however narrow the terminal is. The styles travel with
// their segments, and the separators keep the styling they were rendered
// with, so a wrapped line reads as the same identity. A width of zero or less
// keeps the text whole, which leaves the terminal to clip it.
func wrapChrome(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	parts := splitChrome(text)
	var lines []string
	var line strings.Builder
	lineWidth := 0
	flush := func() {
		if line.Len() > 0 {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
	}
	for index := 0; index < len(parts); index += 2 {
		segment := parts[index]
		prefix := ""
		if lineWidth > 0 {
			prefix = parts[index-1]
		}
		if lineWidth > 0 &&
			lineWidth+ansi.StringWidth(prefix)+ansi.StringWidth(segment) > width {
			flush()
			prefix = ""
		}
		if line.Len() == 0 && ansi.StringWidth(segment) > width {
			for hard := range strings.SplitSeq(ansi.Wrap(segment, width, ""), "\n") {
				lines = append(lines, hard)
			}
			if index+1 < len(parts) {
				line.WriteString(parts[index+1])
				lineWidth = ansi.StringWidth(parts[index+1])
			}
			continue
		}
		line.WriteString(prefix)
		line.WriteString(segment)
		lineWidth += ansi.StringWidth(prefix) + ansi.StringWidth(segment)
	}
	flush()
	if len(lines) == 0 {
		return []string{text}
	}
	return lines
}

// splitChrome cuts a styled chrome line at its segment separators, returning
// alternating segments and separators with their raw text, styles included,
// so the wrap folds whole segments and rejoins them byte-identical. The cut
// positions come from the plain text, mapped back onto the raw string, so a
// separator is found even when escape sequences run through it.
func splitChrome(text string) []string {
	plain, raw := stripChrome(text)
	marks := strings.Split(plain, chromeSeparator)
	parts := make([]string, 0, len(marks)*2-1)
	offset := 0
	previous := 0
	for index, mark := range marks {
		end := offset + len(mark)
		// The slice opens where the previous one closed rather than where its
		// plain text starts, so the escape sequences between them stay with
		// the part that follows instead of falling out of the rejoin.
		parts = append(parts, text[previous:raw[end]])
		previous = raw[end]
		offset = end
		if index+1 < len(marks) {
			end += len(chromeSeparator)
			parts = append(parts, text[previous:raw[end]])
			previous = raw[end]
			offset = end
		}
	}
	return parts
}

// stripChrome returns the plain text of a styled chrome line and, for every
// byte of it, the byte offset of that same content in the raw string, so
// positions found in the plain text map back onto the raw one.
func stripChrome(text string) (string, []int) {
	var plain strings.Builder
	var raw []int
	rest := text
	offset := 0
	for len(rest) > 0 {
		if rest[0] == 0x1b && len(rest) > 1 {
			end := 2
			switch rest[1] {
			case '[':
				for end < len(rest) && (rest[end] < 0x40 || rest[end] > 0x7e) {
					end++
				}
				end++
			case ']':
				for end < len(rest) && rest[end] != 0x07 {
					if rest[end] == 0x1b && end+1 < len(rest) && rest[end+1] == 0x5c {
						end += 2
						break
					}
					end++
				}
				if end < len(rest) && rest[end] == 0x07 {
					end++
				}
			}
			offset += end
			rest = rest[end:]
			continue
		}
		size := 1
		for size < len(rest) && !utf8.RuneStart(rest[size]) {
			size++
		}
		plain.WriteString(rest[:size])
		for range size {
			raw = append(raw, offset)
		}
		offset += size
		rest = rest[size:]
	}
	raw = append(raw, offset)
	return plain.String(), raw
}
