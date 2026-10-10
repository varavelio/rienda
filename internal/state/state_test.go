package state

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadAndWrite verifies the round trip of the document through the file.
func TestLoadAndWrite(t *testing.T) {
	t.Run("roundtrip of a full document", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		doc := Doc{
			LastModel:    "opencode-go/kimi-k2",
			LastThinking: map[string]string{"opencode-go/kimi-k2": "high", "local/gpt": "low"},
			PinnedModels: []string{"opencode-go/kimi-k2", "local/gpt"},
		}
		require.NoError(t, Write(path, doc))
		got, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, doc, got)
	})

	t.Run("empty document roundtrip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		require.NoError(t, Write(path, Doc{}))
		got, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, Doc{}, got)
	})

	t.Run("missing file is a zero document", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		got, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, Doc{}, got)
	})

	t.Run("pinned order survives the roundtrip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		pins := []string{"zai-coding-plan/glm-4.6", "opencode-go/kimi-k2", "local/gpt"}
		require.NoError(t, Write(path, Doc{PinnedModels: pins}))
		got, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, pins, got.PinnedModels)
	})

	t.Run("no pins omit the field", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		require.NoError(t, Write(path, Doc{LastModel: "a/1"}))
		data, err := os.ReadFile(path) //nolint:gosec // the test selected the path.
		require.NoError(t, err)
		require.NotContains(t, string(data), "pinned_models")
	})
}

// TestLoadMalformed verifies a document that exists but is not a document is
// an error naming the path, not a silent zero.
func TestLoadMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("{{{"), 0o600))
	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), path)
}

// TestWriteAtomicity verifies the file is replaced whole: a concurrent
// reader never sees part of a document, and the content that survives is the
// new document.
func TestWriteAtomicity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, Write(path, Doc{LastModel: "a/1"}))
	require.NoError(t, Write(path, Doc{LastModel: "b/2"}))
	got, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "b/2", got.LastModel)
}

// TestWriteLeavesNoTempFiles verifies the staging file is gone after a
// normal write.
func TestWriteLeavesNoTempFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, Write(path, Doc{LastModel: "a/1"}))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	for _, entry := range entries {
		require.True(t, entry.Name() == "state.json", "leftover file %q", entry.Name())
	}
}

// TestWriteModeOnUnix verifies the document is only readable by its owner.
// Windows has no equivalent; the test is skipped there.
func TestWriteModeOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no file modes on windows")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, Write(path, Doc{}))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestTogglePin verifies the pin action the model list runs: it adds a
// reference that is absent, removes one that is present, and never leaves a
// duplicate or a corrupt document behind.
func TestTogglePin(t *testing.T) {
	t.Run("pinning an absent reference appends it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		pinned, err := TogglePin(path, "a/1")
		require.NoError(t, err)
		require.True(t, pinned)

		pinned, err = TogglePin(path, "b/2")
		require.NoError(t, err)
		require.True(t, pinned)

		doc, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, []string{"a/1", "b/2"}, doc.PinnedModels)
	})

	t.Run("pinning a missing file creates it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "state.json")
		pinned, err := TogglePin(path, "a/1")
		require.NoError(t, err)
		require.True(t, pinned)
		doc, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, []string{"a/1"}, doc.PinnedModels)
	})

	t.Run("unpinning removes the reference", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		require.NoError(t, Write(path, Doc{PinnedModels: []string{"a/1", "b/2", "c/3"}}))

		pinned, err := TogglePin(path, "b/2")
		require.NoError(t, err)
		require.False(t, pinned)

		doc, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, []string{"a/1", "c/3"}, doc.PinnedModels)
	})

	t.Run("unpinning normalizes duplicates", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		require.NoError(t, Write(path, Doc{PinnedModels: []string{"a/1", "a/1", "b/2"}}))

		_, err := TogglePin(path, "a/1")
		require.NoError(t, err)

		doc, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, []string{"b/2"}, doc.PinnedModels)
	})

	t.Run("a malformed file is an error and stays untouched", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		original := []byte("{{{")
		require.NoError(t, os.WriteFile(path, original, 0o600))

		_, err := TogglePin(path, "a/1")
		require.Error(t, err)

		data, err := os.ReadFile(path) //nolint:gosec // the test selected the path.
		require.NoError(t, err)
		require.Equal(t, original, data)
	})

	t.Run("a toggle keeps the other fields", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		require.NoError(t, Write(path, Doc{
			LastModel:    "a/1",
			LastThinking: map[string]string{"a/1": "high"},
		}))

		_, err := TogglePin(path, "a/1")
		require.NoError(t, err)

		doc, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, "a/1", doc.LastModel)
		require.Equal(t, map[string]string{"a/1": "high"}, doc.LastThinking)
	})
}
