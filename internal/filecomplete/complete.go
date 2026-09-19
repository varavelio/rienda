package filecomplete

import (
	"fmt"
	"slices"

	"github.com/varavelio/rienda/internal/files"
	"github.com/varavelio/rienda/internal/fuzzy"
)

// Source lists the entries of a project.
type Source interface {
	// Entries returns the entries of the project, sorted by path.
	Entries() ([]files.Entry, error)
}

// Suggestion is a project path that completes a mention.
type Suggestion struct {
	// Path is the location of the entry relative to the root of the project.
	// The path of a directory ends with a slash, so a suggestion reads the way
	// the mention that completes it is written.
	Path string

	// IsDir reports whether the suggestion is a directory.
	IsDir bool
}

// List returns the suggestions of a project: its entries with the directories
// marked by a trailing slash, in the order of the project. It reads the source
// on every call, so a caller that completes repeatedly lists the project once
// and keeps its suggestions.
func List(source Source) ([]Suggestion, error) {
	entries, err := source.Entries()
	if err != nil {
		return nil, fmt.Errorf("filecomplete: list the project: %w", err)
	}

	suggestions := make([]Suggestion, 0, len(entries))
	for _, entry := range entries {
		path := entry.Path
		if entry.IsDir {
			path += "/"
		}
		suggestions = append(suggestions, Suggestion{Path: path, IsDir: entry.IsDir})
	}
	return suggestions, nil
}

// Rank returns up to limit suggestions whose path best matches query, best
// match first. A limit that is not positive returns every match, and an empty
// query returns the suggestions in the order of the project, so a completion
// can open before the user narrows the query.
//
// Rank always returns a new slice, so callers own the result and never share
// the suggestions they keep with the ones they show.
func Rank(candidates []Suggestion, query string, limit int) []Suggestion {
	if query == "" {
		return first(candidates, limit)
	}

	path := func(suggestion Suggestion) string { return suggestion.Path }
	return first(fuzzy.Search(query, candidates, path), limit)
}

// first returns a copy of the leading limit suggestions, or of every one of
// them when limit is not positive.
func first(suggestions []Suggestion, limit int) []Suggestion {
	if limit > 0 && len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}
	return slices.Clone(suggestions)
}
