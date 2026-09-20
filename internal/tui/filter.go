package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varavelio/rienda/internal/fuzzy"
)

// filterPrompt opens the query input of every list.
const filterPrompt = "> "

// filterPromptWidth is the number of columns the query input spends on its
// prompt.
const filterPromptWidth = 2

// filter is a list of items the user narrows by typing. It keeps a query input
// that stays focused, the indexes of the items the query matches, best match
// first, and the position of the highlight among them, so the user can search
// and move the highlight at the same time.
type filter struct {
	// count is the number of items the list offers.
	count int

	// text returns the text of the item at an index, which the query is
	// matched against. The list reads the items through it instead of holding
	// a copy of them.
	text func(int) string

	// input holds the query the user types.
	input textinput.Model

	// shown holds the indexes of the items the query matches, best match
	// first. An empty query shows every item in its original order.
	shown []int

	// cursor is the position in shown the highlight holds.
	cursor int
}

// newFilter builds a list of count items whose text the caller reads with the
// text accessor, narrowed by the query typed into its input. The input opens
// focused, so the user can search as soon as the list appears.
func newFilter(
	count int,
	text func(int) string,
	placeholder string,
	base styles,
	isDark bool,
) filter {
	input := textinput.New()
	input.Prompt = filterPrompt
	input.Placeholder = placeholder
	input.SetStyles(newFilterStyles(base, isDark))
	input.Focus()

	built := filter{count: count, text: text, input: input}
	built.refresh()
	return built
}

// refresh re-ranks the items against the current query and returns the
// highlight to the best match.
func (l *filter) refresh() {
	indexes := make([]int, l.count)
	for index := range indexes {
		indexes[index] = index
	}
	l.shown = fuzzy.Search(l.query(), indexes, l.text)
	l.cursor = 0
}

// query returns the text the user typed.
func (l *filter) query() string {
	return l.input.Value()
}

// update forwards a message to the query input and re-ranks the list when the
// query changed. A message that does not change the query, such as a cursor
// movement, only reaches the input.
func (l *filter) update(msg tea.Msg) tea.Cmd {
	before := l.query()
	var cmd tea.Cmd
	l.input, cmd = l.input.Update(msg)
	if l.query() != before {
		l.refresh()
	}
	return cmd
}

// move shifts the highlight delta positions through the matches, wrapping
// around at both ends so the user cycles through them.
func (l *filter) move(delta int) {
	l.cursor = moveCursor(l.cursor, delta, len(l.shown))
}

// selected returns the index of the highlighted item, or -1 when the query
// matches none.
func (l *filter) selected() int {
	if len(l.shown) == 0 {
		return -1
	}
	return l.shown[l.cursor]
}

// empty reports whether the query matches no item.
func (l *filter) empty() bool {
	return len(l.shown) == 0
}

// window returns the positions of the matches that fit in rows while keeping
// the highlight visible.
func (l *filter) window(rows int) (first, last int) {
	return visibleWindow(l.cursor, len(l.shown), rows)
}

// clear empties the query and shows every item again. It reports whether there
// was a query to clear.
func (l *filter) clear() bool {
	if l.query() == "" {
		return false
	}
	l.input.Reset()
	l.refresh()
	return true
}

// setWidth sets the columns the query input shows, so a long query scrolls
// inside the line instead of running past the terminal.
func (l *filter) setWidth(width int) {
	l.input.SetWidth(max(1, width-filterPromptWidth))
}

// setStyles applies the styles of the current terminal background to the query
// input.
func (l *filter) setStyles(base styles, isDark bool) {
	l.input.SetStyles(newFilterStyles(base, isDark))
}

// view renders the query input.
func (l *filter) view() string {
	return l.input.View()
}

// newFilterStyles builds the styles of a query input for the given terminal
// background. The cursor does not blink, so a list needs no blink commands and
// the caret stays still under the reader.
func newFilterStyles(base styles, isDark bool) textinput.Styles {
	input := textinput.DefaultStyles(isDark)
	input.Focused.Prompt = base.inputPrompt
	input.Focused.Placeholder = base.dim
	input.Focused.Text = lipgloss.NewStyle()
	input.Cursor.Blink = false
	input.Blurred = input.Focused
	return input
}
