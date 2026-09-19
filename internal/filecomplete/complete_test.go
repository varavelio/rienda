package filecomplete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/files"
)

// fakeSource is a scripted Source.
type fakeSource struct {
	entries []files.Entry
	err     error
}

// Entries returns the scripted entries.
func (s *fakeSource) Entries() ([]files.Entry, error) {
	return s.entries, s.err
}

// project is the listing of a small project, sorted by path.
func project() []files.Entry {
	return []files.Entry{
		{Path: "README.md"},
		{Path: "cmd", IsDir: true},
		{Path: "cmd/main.go"},
		{Path: "internal", IsDir: true},
		{Path: "internal/tui", IsDir: true},
		{Path: "internal/tui/model.go"},
		{Path: "internal/tui/view.go"},
	}
}

// suggestions is the project as the suggestions of a completion.
func suggestions() []Suggestion {
	return []Suggestion{
		{Path: "README.md"},
		{Path: "cmd/", IsDir: true},
		{Path: "cmd/main.go"},
		{Path: "internal/", IsDir: true},
		{Path: "internal/tui/", IsDir: true},
		{Path: "internal/tui/model.go"},
		{Path: "internal/tui/view.go"},
	}
}

// TestList verifies the suggestions of a project.
func TestList(t *testing.T) {
	t.Run("marks the directories with a trailing slash", func(t *testing.T) {
		listed, err := List(&fakeSource{entries: project()})

		require.NoError(t, err)
		require.Equal(t, suggestions(), listed)
	})

	t.Run("reports the failure of the source", func(t *testing.T) {
		source := &fakeSource{err: errors.New("files: list: permission denied")}

		_, err := List(source)

		require.ErrorContains(t, err, "permission denied")
	})
}

// TestRank verifies the ranking of the suggestions.
func TestRank(t *testing.T) {
	candidates := suggestions()

	t.Run("keeps the order of the project for an empty query", func(t *testing.T) {
		require.Equal(t, candidates, Rank(candidates, "", 0))
	})

	t.Run("ranks the best match first", func(t *testing.T) {
		ranked := Rank(candidates, "model", 0)

		require.Equal(t, []Suggestion{{Path: "internal/tui/model.go"}}, ranked)
	})

	t.Run("ranks a directory ahead of the files it holds", func(t *testing.T) {
		ranked := Rank(candidates, "tui", 0)

		require.Equal(t, "internal/tui/", ranked[0].Path)
		require.Len(t, ranked, 3)
	})

	t.Run("keeps only the suggestions the limit allows", func(t *testing.T) {
		ranked := Rank(candidates, "", 2)

		require.Equal(t, []Suggestion{{Path: "README.md"}, {Path: "cmd/", IsDir: true}}, ranked)
	})

	t.Run("returns a new slice", func(t *testing.T) {
		ranked := Rank(candidates, "", 0)
		ranked[0].Path = "changed"

		require.Equal(t, "README.md", candidates[0].Path)
	})
}
