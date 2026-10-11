package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/slash"
)

// slashGlyph opens a custom command, the prompt shortcut expanded on submit.
const slashGlyph = '/'

// maxSlashItems caps how many suggestions the interface shows at once, so the
// completion popup never takes over the conversation.
const maxSlashItems = 8

// slashMenu is the state of the command completion the prompt opens with a /.
type slashMenu struct {
	// active reports that the prompt holds a command the popup follows.
	active bool

	// query is the text written between the slash and the cursor.
	query string

	// closed reports that the user canceled the menu with escape. The popup
	// stays closed while the menu keeps the canceled text, so the
	// cancellation is not undone by a key that does not change it.
	closed bool

	// items are the suggestions for the query, sorted by ID.
	items []slash.Command

	// cursor is the highlighted suggestion.
	cursor int
}

// slashToken returns the text written after the slash that ends at the cursor,
// and whether the prompt holds one. A command opens with a / that is the very
// first rune of the prompt, so a slash anywhere else is left alone, and it
// runs to the cursor without crossing whitespace, so typing a space completes
// the token and hands the prompt to the mention system.
func slashToken(value string, row, col int) (string, bool) {
	if row != 0 {
		return "", false
	}
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return "", false
	}
	runes := []rune(lines[0])
	if len(runes) == 0 || runes[0] != slashGlyph {
		return "", false
	}
	col = min(max(col, 0), len(runes))
	if col < 1 {
		return "", false
	}
	query := string(runes[1:col])
	if strings.ContainsFunc(query, func(r rune) bool {
		return r == ' ' || r == '\t'
	}) {
		return "", false
	}
	return query, true
}

// syncSlash follows the command the prompt holds and refreshes the
// suggestions it offers. It mirrors syncMention without any filesystem read:
// ranking loaded commands costs nothing.
func (m *model) syncSlash() {
	if m.phase != phaseChat {
		m.slash = slashMenu{}
		return
	}
	query, ok := slashToken(m.input.Value(), m.input.Line(), m.input.Column())
	switch {
	case !ok:
		m.slash = slashMenu{}
	case m.slash.active && m.slash.query == query:
		// The query did not change; the suggestions still stand.
	case m.slash.closed && m.slash.query == query:
		// The user canceled this menu; it stays closed.
	default:
		m.slash = m.suggestSlash(query)
	}
}

// suggestSlash returns the menu for a query, filtering the loaded commands by
// case-insensitive substring on ID or description. An empty query offers every
// command; the input arrives sorted by ID so ties read deterministically.
func (m *model) suggestSlash(query string) slashMenu {
	lowered := strings.ToLower(query)
	items := make([]slash.Command, 0, len(m.slashCommands))
	for _, cmd := range m.slashCommands {
		if lowered == "" ||
			strings.Contains(strings.ToLower(cmd.ID), lowered) ||
			strings.Contains(strings.ToLower(cmd.Description), lowered) {
			items = append(items, cmd)
		}
	}
	if len(items) > maxSlashItems {
		items = items[:maxSlashItems]
	}
	return slashMenu{active: true, query: query, items: items}
}

// dismissSlash closes the popup until the query changes.
func (m *model) dismissSlash() {
	m.slash.active = false
	m.slash.closed = true
	m.slash.items = nil
}

// handleSlashKey handles the keys of the popup while the prompt holds a
// command. It reports whether it consumed the key, which happens only while
// the menu is active, so every other key reaches the prompt as usual.
func (m *model) handleSlashKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.slash.active {
		return nil, false
	}

	switch key.String() {
	case keyEscape:
		m.dismissSlash()
		return nil, true
	case keyEnter, keyTab:
		return m.acceptSlash()
	case keyUp:
		m.moveSlash(-1)
		return nil, true
	case keyDown:
		m.moveSlash(1)
		return nil, true
	}
	return nil, false
}

// moveSlash highlights the suggestion a direction away, wrapping around the
// list so the user cycles through the suggestions.
func (m *model) moveSlash(direction int) {
	m.slash.cursor = moveCursor(m.slash.cursor, direction, len(m.slash.items))
}

// acceptSlash completes the highlighted suggestion. It reports whether there
// was one to complete, so enter still sends the prompt when the menu offers
// nothing.
func (m *model) acceptSlash() (tea.Cmd, bool) {
	if len(m.slash.items) == 0 {
		return nil, false
	}
	return m.completeSlash(m.slash.items[m.slash.cursor]), true
}

// completeSlash replaces the menu query with the accepted command name while
// keeping the / that opens it, then adds a space so the token reads complete
// and the popup closes. Expansion still waits for submit: the prompt holds a
// literal shortcut until it is sent.
func (m *model) completeSlash(cmd slash.Command) tea.Cmd {
	m.eraseSlashQuery()
	m.input.InsertString(cmd.ID)
	m.input.InsertString(" ")
	return m.syncPrompt()
}

// eraseSlashQuery deletes the query written after the slash and leaves every
// other character of the prompt untouched. The deletion is replayed the way a
// key does, so the cursor of the prompt lands right after the slash whatever
// the wrapping of its line.
func (m *model) eraseSlashQuery() {
	for range len([]rune(m.slash.query)) {
		m.input, _ = m.input.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
}

// slashHeight returns the rows the command popup occupies, or zero when the
// popup is closed or the terminal cannot spare a row beyond the conversation
// the interface always keeps visible.
func (m *model) slashHeight() int {
	items := len(m.slash.items)
	if items == 0 || !m.slash.active {
		return 0
	}

	room := m.height - len(
		m.fullIdentityLines(),
	) - headerFixedRows - m.activityHeight() - inputBoxRows -
		len(
			m.fullHintLines(),
		) - m.input.Height() - minTranscriptRows
	if room <= 0 {
		return 0
	}
	return min(items, room)
}

// slashList renders the completion popup, one /name plus description per row.
// The highlighted suggestion is the one the user accepts with enter.
func (m *model) slashList(rows int) string {
	first, last := visibleWindow(m.slash.cursor, len(m.slash.items), rows, 0)

	lines := make([]string, 0, rows)
	for index := first; index < last; index++ {
		item := m.slash.items[index]
		label := "/" + item.ID + m.styles.dim.Render(": "+item.Description)
		lines = append(lines, m.row(index == m.slash.cursor, label))
	}
	return strings.Join(lines, "\n")
}
