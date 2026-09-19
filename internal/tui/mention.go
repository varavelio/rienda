package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/filecomplete"
)

// mentionGlyph opens a mention, the reference to a project file the model is
// asked to read.
const mentionGlyph = '@'

// maxMentionItems caps how many suggestions the interface asks for and shows
// at once, so the completion popup never takes over the conversation.
const maxMentionItems = 8

// fileScanner reads the paths of the project that complete a mention. It runs
// outside the update loop, so the interface never waits for the project to be
// read, and it is called again whenever a mention opens, so a file created
// while the interface runs shows up. The filecomplete package provides the
// production implementation, which keeps the model free of the filesystem.
type fileScanner func() ([]filecomplete.Suggestion, error)

// filesScannedMsg carries the paths of the project read again.
type filesScannedMsg struct {
	items []filecomplete.Suggestion
	err   error
}

// mention is the state of the file completion the prompt opens with an @.
type mention struct {
	// active reports that the prompt holds a mention the popup follows.
	active bool

	// query is the text written between the mention and the cursor.
	query string

	// closed reports that the user canceled the mention with escape. The
	// popup stays closed while the mention keeps the canceled text, so the
	// cancellation is not undone by a key that does not change it.
	closed bool

	// items are the suggestions of the query, best match first.
	items []filecomplete.Suggestion

	// cursor is the highlighted suggestion.
	cursor int
}

// mentionToken returns the text written after the mention that ends at the
// cursor, and whether the prompt holds one. A mention opens with an @ that
// starts a word, so an at sign inside one, as in an email address, is left
// alone, and it runs to the cursor across a line without spaces, so the cursor
// always closes it.
func mentionToken(value string, row, col int) (string, bool) {
	lines := strings.Split(value, "\n")
	if row < 0 || row >= len(lines) {
		return "", false
	}

	runes := []rune(lines[row])
	col = min(max(col, 0), len(runes))

	start := col
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if start >= col || runes[start] != mentionGlyph {
		return "", false
	}
	return string(runes[start+1 : col]), true
}

// syncMention follows the mention the prompt holds and refreshes the
// suggestions it offers. It returns the command that reads the project again
// when a mention opens, so a file created while the interface runs shows up;
// every other change only re-ranks the paths already known, which costs
// nothing.
func (m *model) syncMention() tea.Cmd {
	if m.scanFiles == nil || m.phase != phaseChat {
		m.mention = mention{}
		return nil
	}

	opened := !m.mention.active
	query, ok := mentionToken(m.input.Value(), m.input.Line(), m.input.Column())
	switch {
	case !ok:
		m.mention = mention{}
	case m.mention.active && m.mention.query == query:
		// The mention did not change; the suggestions still stand.
	case m.mention.closed && m.mention.query == query:
		// The user canceled this mention; it stays closed.
	default:
		m.mention = m.suggest(query)
	}
	if opened && m.mention.active {
		return m.scanFilesCmd()
	}
	return nil
}

// syncPrompt refreshes everything that follows from the prompt: the completion
// of its mention and the layout, whose rows the popup shares with the
// conversation. It is called once the prompt may have changed.
func (m *model) syncPrompt() tea.Cmd {
	cmd := m.syncMention()
	m.syncLayout()
	return cmd
}

// scanFilesCmd returns the command that reads the project again and reports
// its paths as a filesScannedMsg. The read runs outside the update loop, so a
// slow filesystem never stalls the interface, and it never fails the prompt:
// a read that fails keeps the paths already known.
func (m *model) scanFilesCmd() tea.Cmd {
	if m.scanFiles == nil {
		return nil
	}

	scan := m.scanFiles
	return func() tea.Msg {
		items, err := scan()
		return filesScannedMsg{items: items, err: err}
	}
}

// applyScan keeps the paths of the project read again and updates the
// suggestions of the mention in progress, so a file created while the
// interface runs shows up without the user reopening the completion. It keeps
// the highlighted suggestion when it still exists.
func (m *model) applyScan(msg filesScannedMsg) {
	if msg.err != nil {
		return
	}

	m.candidates = msg.items
	if !m.mention.active {
		return
	}

	m.mention.items = filecomplete.Rank(m.candidates, m.mention.query, maxMentionItems)
	m.mention.cursor = min(m.mention.cursor, max(0, len(m.mention.items)-1))
	m.syncLayout()
}

// suggest returns the mention of a query, ranking the paths already known of
// the project.
func (m *model) suggest(query string) mention {
	items := filecomplete.Rank(m.candidates, query, maxMentionItems)
	return mention{active: true, query: query, items: items}
}

// dismissMention closes the popup until the mention changes.
func (m *model) dismissMention() {
	m.mention.active = false
	m.mention.closed = true
	m.mention.items = nil
}

// handleMentionKey handles the keys of the popup while the prompt holds a
// mention. It reports whether it consumed the key, which happens only while
// the mention is active, so every other key reaches the prompt as usual.
func (m *model) handleMentionKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.mention.active {
		return nil, false
	}

	switch key.String() {
	case keyEscape:
		m.dismissMention()
		return nil, true
	case keyEnter, keyTab:
		return m.acceptMention()
	case keyUp:
		m.moveMention(-1)
		return nil, true
	case keyDown:
		m.moveMention(1)
		return nil, true
	}
	return nil, false
}

// moveMention highlights the suggestion a direction away, wrapping around the
// list so the user cycles through the suggestions.
func (m *model) moveMention(direction int) {
	m.mention.cursor = moveCursor(m.mention.cursor, direction, len(m.mention.items))
}

// acceptMention completes the highlighted suggestion. It reports whether there
// was one to complete, so enter still sends the prompt when the mention offers
// nothing.
func (m *model) acceptMention() (tea.Cmd, bool) {
	if len(m.mention.items) == 0 {
		return nil, false
	}

	return m.completeMention(m.mention.items[m.mention.cursor]), true
}

// completeMention replaces the mention query with the accepted path while
// keeping the @ that opens it, so the prompt holds a literal reference: the
// model reads the file and the interface only spells its path. A file is
// followed by a space, which closes the popup; a directory keeps it open so
// the user can keep narrowing the path inside it.
func (m *model) completeMention(suggestion filecomplete.Suggestion) tea.Cmd {
	m.eraseMentionQuery()
	m.input.InsertString(suggestion.Path)
	if !suggestion.IsDir {
		m.input.InsertString(" ")
	}
	return m.syncPrompt()
}

// eraseMentionQuery deletes the query written after the mention and leaves
// every other character of the prompt untouched. The deletion is replayed the
// way a key does, so the cursor of the prompt lands right after the mention
// whatever the wrapping of its line.
func (m *model) eraseMentionQuery() {
	for range len([]rune(m.mention.query)) {
		m.input, _ = m.input.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
}
