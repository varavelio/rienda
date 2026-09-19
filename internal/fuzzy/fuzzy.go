package fuzzy

import (
	"iter"
	"slices"

	"github.com/sahilm/fuzzy"
)

// Search returns the items whose text matches query, best match first. The
// query matches an item when its characters appear in the text in the same
// order, ignoring case; an empty query returns every item in its original
// order.
//
// The text accessor is called once per item, so a caller that ranks the same
// items repeatedly reads their text from memory instead of rebuilding it.
// Search never returns the slice it was given: callers keep ownership of items
// and are free to modify the result.
func Search[T any](query string, items []T, text func(T) string) []T {
	if query == "" {
		return slices.Clone(items)
	}

	matches := fuzzy.FindFromIter(query, texts(items, text))
	ranked := make([]T, 0, len(matches))
	for _, match := range matches {
		ranked = append(ranked, items[match.Index])
	}
	return ranked
}

// texts yields the text of every item in order, which lets the ranking read
// the candidates without building an intermediate slice of their texts.
func texts[T any](items []T, text func(T) string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, item := range items {
			if !yield(text(item)) {
				return
			}
		}
	}
}
