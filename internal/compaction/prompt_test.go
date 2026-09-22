package compaction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// overridePath returns the path of a prompt override inside a temporary
// directory.
func overridePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "COMPACTION.md")
}

// writeOverride stores contents at path.
func writeOverride(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

// TestPrompt verifies the summarization prompt and its override.
func TestPrompt(t *testing.T) {
	t.Run("embeds a non-empty general purpose prompt", func(t *testing.T) {
		prompt, err := Prompt("")

		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(prompt))
		require.Contains(t, prompt, "### Objective")
		require.Contains(t, prompt, "### Next steps")
		require.Contains(t, prompt, "previous-summary")
	})

	t.Run("replaces the prompt in full with the override", func(t *testing.T) {
		path := overridePath(t)
		writeOverride(t, path, "My own summary format.")

		prompt, err := Prompt(path)

		require.NoError(t, err)
		require.Equal(t, "My own summary format.", prompt)
	})

	t.Run("ignores a whitespace-only override", func(t *testing.T) {
		path := overridePath(t)
		writeOverride(t, path, "  \n\t \n")

		prompt, err := Prompt(path)

		require.NoError(t, err)
		require.Contains(t, prompt, "### Objective")
	})

	t.Run("leaves the embedded prompt alone when the override is missing", func(t *testing.T) {
		prompt, err := Prompt(overridePath(t))

		require.NoError(t, err)
		require.Contains(t, prompt, "### Objective")
	})

	t.Run("picks up an override written between two calls", func(t *testing.T) {
		path := overridePath(t)

		before, err := Prompt(path)
		require.NoError(t, err)
		require.Contains(t, before, "### Objective")

		writeOverride(t, path, "Written later.")

		after, err := Prompt(path)
		require.NoError(t, err)
		require.Equal(t, "Written later.", after)
	})

	t.Run("reports an override that cannot be read", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dir")
		require.NoError(t, os.MkdirAll(path, 0o700))

		_, err := Prompt(path)

		require.ErrorContains(t, err, "read prompt override")
	})
}
