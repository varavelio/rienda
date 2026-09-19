package filecomplete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/files"
)

// fakeSource is a scripted Source that counts how often the project is read.
type fakeSource struct {
	entries []files.Entry
	err     error
	reads   int
}

// Entries returns the scripted entries and counts the read.
func (s *fakeSource) Entries() ([]files.Entry, error) {
	s.reads++
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

// TestFinder verifies the suggestions of a completion.
func TestFinder(t *testing.T) {
	t.Run("marks the directories with a trailing slash", func(t *testing.T) {
		finder := New(&fakeSource{entries: project()})

		suggestions, err := finder.Complete("", 0)

		require.NoError(t, err)
		require.Equal(t, []Suggestion{
			{Path: "README.md"},
			{Path: "cmd/", IsDir: true},
			{Path: "cmd/main.go"},
			{Path: "internal/", IsDir: true},
			{Path: "internal/tui/", IsDir: true},
			{Path: "internal/tui/model.go"},
			{Path: "internal/tui/view.go"},
		}, suggestions)
	})

	t.Run("ranks the suggestions that match the query", func(t *testing.T) {
		finder := New(&fakeSource{entries: project()})

		ranked, err := finder.Complete("model", 0)

		require.NoError(t, err)
		require.Equal(t, []Suggestion{{Path: "internal/tui/model.go"}}, ranked)
	})

	t.Run("ranks a directory ahead of the files it holds", func(t *testing.T) {
		finder := New(&fakeSource{entries: project()})

		ranked, err := finder.Complete("tui", 0)

		require.NoError(t, err)
		require.Equal(t, "internal/tui/", ranked[0].Path)
		require.Len(t, ranked, 3)
	})

	t.Run("keeps only the suggestions the limit allows", func(t *testing.T) {
		finder := New(&fakeSource{entries: project()})

		ranked, err := finder.Complete("", 2)

		require.NoError(t, err)
		require.Equal(t, []Suggestion{{Path: "README.md"}, {Path: "cmd/", IsDir: true}}, ranked)
	})

	t.Run("reads the project once", func(t *testing.T) {
		source := &fakeSource{entries: project()}
		finder := New(source)

		_, err := finder.Complete("mo", 0)
		require.NoError(t, err)
		_, err = finder.Complete("tui", 0)
		require.NoError(t, err)

		require.Equal(t, 1, source.reads)
	})

	t.Run("reports the failure of the source on every call", func(t *testing.T) {
		source := &fakeSource{err: errors.New("files: list: permission denied")}
		finder := New(source)

		_, first := finder.Complete("", 0)
		_, second := finder.Complete("mo", 0)

		require.ErrorContains(t, first, "permission denied")
		require.ErrorContains(t, second, "permission denied")
		require.Equal(t, 1, source.reads)
	})

	t.Run("does not share the suggestions it keeps", func(t *testing.T) {
		finder := New(&fakeSource{entries: project()})

		ranked, err := finder.Complete("", 0)
		require.NoError(t, err)
		ranked[0].Path = "changed"

		again, err := finder.Complete("", 0)
		require.NoError(t, err)
		require.Equal(t, "README.md", again[0].Path)
	})
}
