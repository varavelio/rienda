package slash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidID(t *testing.T) {
	t.Run("accepts plain names", func(t *testing.T) {
		for _, id := range []string{"test", "my-command", "doc_2", "A"} {
			require.True(t, ValidID(id), "id %q", id)
		}
	})
	t.Run("rejects special names", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "a/b", `a\b`, "test.md", "a.md"} {
			require.False(t, ValidID(id), "id %q", id)
		}
	})
}

func TestDefaultDir(t *testing.T) {
	t.Run("joins home commands dir", func(t *testing.T) {
		t.Setenv("HOME", "/tmp/fakehome")
		dir, err := DefaultDir()
		require.NoError(t, err)
		require.Equal(t, filepath.Join("/tmp/fakehome", ".rienda", "commands"), dir)
	})
}

func TestLoad(t *testing.T) {
	t.Run("rejects invalid id", func(t *testing.T) {
		_, err := Load(t.TempDir(), "../evil")
		require.Error(t, err)
	})
	t.Run("reads missing file", func(t *testing.T) {
		_, err := Load(t.TempDir(), "missing")
		require.Error(t, err)
	})
	t.Run("loads valid file with absolute path", func(t *testing.T) {
		dir := t.TempDir()
		writeCommand(t, dir, "test", "Run tests", "Run tests: $*")
		loaded, err := Load(dir, "test")
		require.NoError(t, err)
		require.Equal(t, "test", loaded.ID)
		require.Equal(t, "Run tests", loaded.Description)
		require.Equal(t, "Run tests: $*", loaded.Template)
		require.True(t, filepath.IsAbs(loaded.Path))
	})
	t.Run("wraps parse failure with path", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter\n"), 0o600),
		)
		_, err := Load(dir, "bad")
		require.Error(t, err)
		require.Contains(t, err.Error(), "bad.md")
	})
}

func TestLoadAll(t *testing.T) {
	t.Run("missing dir yields nothing", func(t *testing.T) {
		commands, err := LoadAll(filepath.Join(t.TempDir(), "missing"))
		require.NoError(t, err)
		require.Empty(t, commands)
	})
	t.Run("sorts and skips problems", func(t *testing.T) {
		dir := t.TempDir()
		writeCommand(t, dir, "zeta", "Z", "Z $*")
		writeCommand(t, dir, "alpha", "A", "A $*")
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".hidden.md"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.md"), []byte("broken"), 0o600))
		commands, err := LoadAll(dir)
		require.Error(t, err)
		require.Len(t, commands, 2)
		require.Equal(t, "alpha", commands[0].ID)
		require.Equal(t, "zeta", commands[1].ID)
	})
}

func writeCommand(t *testing.T, dir, id, description, template string) {
	t.Helper()
	contents := "---\ndescription: " + description + "\n---\n" + template + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(contents), 0o600))
}
