//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// writeCommand stores one custom command definition in the commands directory
// of an instance, in the exact Markdown format the binary reads.
func writeCommand(t *testing.T, home, id, description, template string) {
	t.Helper()
	contents := "---\ndescription: " + description + "\n---\n" + template + "\n"
	path := filepath.Join(home, ".rienda", "commands", id+".md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

// TestCustomCommandExpands verifies the prompt shortcut: the model receives
// the expanded template, never the raw slash text.
func TestCustomCommandExpands(t *testing.T) {
	t.Run("expands placeholders", func(t *testing.T) {
		app := newApp(t, harness.Text("done"))
		writeCommand(t, app.Home(), "test", "Run tests", "Run tests: $*")
		result := app.Run(t, "run", "-a", "coder", "-p", "/test --coverage")
		result.RequireSuccess(t)
		chat := app.Provider().LastRequest(t).Chat(t)
		require.Equal(t, "Run tests: --coverage", chat.Messages[1].Text())
		session := app.Session(t, result.SessionID(t))
		require.Equal(t, "Run tests: --coverage", session.Entries[0].Blocks[0].Text)
	})
	t.Run("appends args without a placeholder", func(t *testing.T) {
		app := newApp(t, harness.Text("done"))
		writeCommand(t, app.Home(), "fixed", "Fixed", "Fixed text")
		result := app.Run(t, "run", "-a", "coder", "-p", "/fixed extra words")
		result.RequireSuccess(t)
		chat := app.Provider().LastRequest(t).Chat(t)
		require.Equal(t, "Fixed text\n\nextra words", chat.Messages[1].Text())
	})
	t.Run("fails unknown without a run", func(t *testing.T) {
		app := newApp(t, harness.Text("done"))
		result := app.Run(t, "run", "-a", "coder", "-p", "/nope x")
		require.NotEqual(t, 0, result.Code)
		require.Contains(t, result.Stderr, "unknown command /nope")
	})
}
