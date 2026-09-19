package filecomplete

import (
	"slices"
	"sync"

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

// Finder suggests the paths of a project that match a mention query.
type Finder struct {
	source Source

	once       sync.Once
	candidates []Suggestion
	err        error
}

// New returns a Finder that completes mentions with the entries of the given
// project.
func New(source Source) *Finder {
	return &Finder{source: source}
}

// Complete returns up to limit suggestions whose path best matches query, best
// match first. A limit of zero or less returns every match, and an empty query
// returns the suggestions in the order of the project, so the popup can open
// before the user narrows the query.
//
// The source is read once, on the first call; the error it reports is returned
// by that call and by every call that follows, so a project that cannot be
// listed never looks like a project without files.
func (f *Finder) Complete(query string, limit int) ([]Suggestion, error) {
	if err := f.load(); err != nil {
		return nil, err
	}

	ranked := f.candidates
	if query != "" {
		path := func(suggestion Suggestion) string { return suggestion.Path }
		ranked = fuzzy.Search(query, f.candidates, path)
	}
	return limitSuggestions(ranked, limit), nil
}

// load reads the source the first time it is called and keeps the suggestions
// it produces.
func (f *Finder) load() error {
	f.once.Do(func() {
		entries, err := f.source.Entries()
		if err != nil {
			f.err = err
			return
		}
		f.candidates = suggestions(entries)
	})
	return f.err
}

// suggestions turns the entries of a project into the suggestions of a
// completion, marking the directories with a trailing slash.
func suggestions(entries []files.Entry) []Suggestion {
	suggestions := make([]Suggestion, 0, len(entries))
	for _, entry := range entries {
		path := entry.Path
		if entry.IsDir {
			path += "/"
		}
		suggestions = append(suggestions, Suggestion{Path: path, IsDir: entry.IsDir})
	}
	return suggestions
}

// limitSuggestions returns the first limit suggestions, or every one of them
// when limit is not positive. It always returns a copy, so the callers of
// Complete never share the suggestions the Finder keeps.
func limitSuggestions(suggestions []Suggestion, limit int) []Suggestion {
	if limit > 0 && len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}
	return slices.Clone(suggestions)
}
