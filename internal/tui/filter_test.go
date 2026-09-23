package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// filterOf builds a filter over the given items, ready to be narrowed by a
// query.
func filterOf(items []string) filter {
	return newFilter(
		len(items),
		func(index int) string { return items[index] },
		"Search",
		newStyles(true),
		true,
	)
}

// typeQuery pushes text into a filter one keystroke at a time, the way the
// keyboard delivers it.
func typeQuery(f *filter, query string) {
	for _, glyph := range query {
		f.update(tea.KeyPressMsg{Code: glyph, Text: string(glyph)})
	}
}

// TestFilter verifies the list that narrows by typing.
func TestFilter(t *testing.T) {
	items := []string{
		"README.md",
		"internal/tui/model.go",
		"internal/tui/view.go",
		"cmd/main.go",
	}

	t.Run("shows every item in order before a query", func(t *testing.T) {
		f := filterOf(items)

		require.Equal(t, []int{0, 1, 2, 3}, f.shown)
		require.False(t, f.empty())
		require.Equal(t, 0, f.selected())
	})

	t.Run("highlights the best match as the query is typed", func(t *testing.T) {
		f := filterOf(items)

		typeQuery(&f, "view")

		require.Equal(t, []int{2}, f.shown)
		require.Equal(t, 2, f.selected())
	})

	t.Run("re-ranks when the query is edited", func(t *testing.T) {
		f := filterOf(items)

		typeQuery(&f, "mainz")
		require.True(t, f.empty(), "a query with no match leaves the list empty")

		f.update(tea.KeyPressMsg{Code: tea.KeyBackspace})

		require.Equal(t, []int{3}, f.shown)
		require.Equal(t, 3, f.selected())
	})

	t.Run("reports no selection while the query matches none", func(t *testing.T) {
		f := filterOf(items)

		typeQuery(&f, "zzzz")

		require.True(t, f.empty())
		require.Equal(t, -1, f.selected())
		require.Zero(t, f.cursor, "an empty list keeps the highlight at its first position")
	})

	t.Run("wraps the highlight around the matches", func(t *testing.T) {
		f := filterOf(items)

		f.move(-1)
		require.Equal(t, 3, f.cursor, "stepping up from the first match wraps to the last")

		f.move(1)
		require.Equal(t, 0, f.cursor, "stepping down from the last match wraps to the first")
	})

	t.Run("clears the query and shows every item again", func(t *testing.T) {
		f := filterOf(items)

		require.False(t, f.clear(), "there is no query to clear")

		typeQuery(&f, "model")
		require.True(t, f.clear())

		require.Equal(t, []int{0, 1, 2, 3}, f.shown)
		require.Equal(t, 0, f.selected())
	})

	t.Run("re-ranks the list when its items are replaced", func(t *testing.T) {
		items := []string{"one", "two"}
		f := filterOf(items)

		typeQuery(&f, "two")
		require.Equal(t, 1, f.selected())

		// The list grows with an entry that matches the query better than the
		// one it held, and loses another one.
		f.text = func(index int) string { return []string{"two", "two again"}[index] }
		f.setCount(2)

		require.Equal(t, []int{0, 1}, f.shown, "the new items are ranked again")
	})

	t.Run("keeps the list whole when the query is empty", func(t *testing.T) {
		f := filterOf(items)

		f.setCount(2)

		require.Equal(t, []int{0, 1}, f.shown)
		require.False(t, f.empty())
	})

	t.Run("windows the matches around the highlight", func(t *testing.T) {
		f := filterOf(items)
		f.setWindowRows(2)

		first, last := f.window()
		require.Equal(t, 0, first)
		require.Equal(t, 2, last)

		f.cursor = 3
		first, last = f.window()
		require.Equal(t, 2, first, "the window follows the highlight to the end")
		require.Equal(t, 4, last)
	})

	t.Run("keeps the margin below the highlight", func(t *testing.T) {
		f := filterOf(items)
		f.setWindowRows(2)
		f.setWindowMargin(1)

		f.cursor = 1
		first, last := f.window()
		require.Equal(t, 1, first, "the row after the highlight stays visible")
		require.Equal(t, 3, last)
	})
}
