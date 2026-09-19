package fuzzy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSearch verifies the fuzzy ranking.
func TestSearch(t *testing.T) {
	// paths stands for the candidates of a project, which is what the file
	// completion ranks.
	paths := []string{
		"internal/tui/model.go",
		"internal/tui/mention.go",
		"internal/files/files.go",
		"cmd/rienda/main.go",
	}

	// path reads the text of a candidate, which for a path is the path itself.
	path := func(candidate string) string { return candidate }

	t.Run("keeps every item in order for an empty query", func(t *testing.T) {
		require.Equal(t, paths, Search("", paths, path))
	})

	t.Run("keeps only the items whose characters appear in order", func(t *testing.T) {
		require.Equal(t, []string{"internal/files/files.go"}, Search("files", paths, path))
		require.Empty(t, Search("zzz", paths, path))
	})

	t.Run("ranks the best match first", func(t *testing.T) {
		require.Equal(t, "internal/tui/model.go", Search("tui/mo", paths, path)[0])
		require.Equal(t, "internal/tui/mention.go", Search("mention", paths, path)[0])
		require.Equal(t, "cmd/rienda/main.go", Search("renda", paths, path)[0])
	})

	t.Run("ignores case", func(t *testing.T) {
		require.Equal(t, "cmd/rienda/main.go", Search("MAIN", paths, path)[0])
	})

	t.Run("returns a new slice", func(t *testing.T) {
		ranked := Search("", paths, path)
		ranked[0] = "changed"

		require.Equal(t, "internal/tui/model.go", paths[0])
	})

	t.Run("ranks any item type through its text", func(t *testing.T) {
		type candidate struct{ name string }

		items := []candidate{{name: "render"}, {name: "reading"}, {name: "random"}}
		name := func(item candidate) string { return item.name }

		require.Equal(t, []candidate{{name: "random"}}, Search("rndm", items, name))
	})
}
