package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeDefinition writes an agent definition file for the test to consume.
func writeDefinition(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestValidID(t *testing.T) {
	t.Run("accepts plain file names", func(t *testing.T) {
		for _, id := range []string{"coder", "review-bot", "kimi_k2", "agent123"} {
			require.True(t, ValidID(id), id)
		}
	})

	t.Run("rejects names that cannot identify a definition", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "nested/agent", `nested\agent`, "agent.md"} {
			require.False(t, ValidID(id), id)
		}
	})
}

func TestDefaultDir(t *testing.T) {
	t.Run("appends the rienda agents directory to the home directory", func(t *testing.T) {
		home := filepath.Join("home", "tester")
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)

		dir, err := DefaultDir()

		require.NoError(t, err)
		require.Equal(t, filepath.Join(home, ".rienda", "agents"), dir)
	})

	t.Run("fails when the home directory is unknown", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")

		_, err := DefaultDir()

		require.Error(t, err)
	})
}

func TestLoad(t *testing.T) {
	t.Run("loads a definition from disk", func(t *testing.T) {
		dir := t.TempDir()
		writeDefinition(t, dir, "coder.md", "---\ndescription: Coder\nmodel: a/b\n---\nPrompt\n")

		loaded, err := Load(dir, "coder")

		require.NoError(t, err)
		require.Equal(t, "coder", loaded.ID)
		require.Equal(t, "Coder", loaded.Description)
		require.Equal(t, "Prompt", loaded.SystemPrompt)
		require.True(t, filepath.IsAbs(loaded.Path))
		require.Equal(t, filepath.Join(dir, "coder.md"), loaded.Path)
	})

	t.Run("reports a missing definition", func(t *testing.T) {
		dir := t.TempDir()

		_, err := Load(dir, "ghost")

		require.ErrorContains(t, err, filepath.Join(dir, "ghost.md"))
	})

	t.Run("rejects identifiers that escape the directory", func(t *testing.T) {
		_, err := Load(t.TempDir(), "../ghost")

		require.ErrorContains(t, err, "invalid agent id")
	})
}

func TestLoadAll(t *testing.T) {
	t.Run("loads definitions sorted by id and skips unrelated files", func(t *testing.T) {
		dir := t.TempDir()
		writeDefinition(t, dir, "zeta.md", "---\ndescription: Z\nmodel: a/b\n---\n")
		writeDefinition(t, dir, "alpha.md", "---\ndescription: A\nmodel: a/b\n---\n")
		writeDefinition(t, dir, "notes.txt", "not an agent")
		writeDefinition(t, dir, ".hidden.md", "---\ndescription: H\nmodel: a/b\n---\n")
		require.NoError(t, os.Mkdir(filepath.Join(dir, "nested.md"), 0o750))

		agents, err := LoadAll(dir)

		require.NoError(t, err)
		require.Len(t, agents, 2)
		require.Equal(t, "alpha", agents[0].ID)
		require.Equal(t, "zeta", agents[1].ID)
	})

	t.Run("returns no agents when the directory does not exist", func(t *testing.T) {
		agents, err := LoadAll(filepath.Join(t.TempDir(), "missing"))

		require.NoError(t, err)
		require.Empty(t, agents)
	})

	t.Run("keeps valid definitions and collects the invalid ones", func(t *testing.T) {
		dir := t.TempDir()
		writeDefinition(t, dir, "good.md", "---\ndescription: G\nmodel: a/b\n---\n")
		writeDefinition(t, dir, "broken.md", "---\nmodel: a/b\n---\n")

		agents, err := LoadAll(dir)

		require.ErrorContains(t, err, "broken.md")
		require.ErrorContains(t, err, "description is required")
		require.Len(t, agents, 1)
		require.Equal(t, "good", agents[0].ID)
	})

	t.Run("reports a path that is not a directory", func(t *testing.T) {
		dir := t.TempDir()
		writeDefinition(t, dir, "file.md", "not a directory")

		_, err := LoadAll(filepath.Join(dir, "file.md"))

		require.ErrorContains(t, err, "read directory")
	})
}
