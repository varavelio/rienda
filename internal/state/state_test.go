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
