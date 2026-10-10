package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadAndAuthenticate verifies the reading of a document every command
// performs: absent file, present file, and the auth check per provider.
func TestLoadAndAuthenticate(t *testing.T) {
	t.Run("missing file is an empty store", func(t *testing.T) {
		store, err := Load(filepath.Join(t.TempDir(), "credentials.json"))
		require.NoError(t, err)
		require.False(t, store.Authenticated("any"))
		require.Empty(t, store.APIKey("any"))
	})

	t.Run("stored keys answer the auth check", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials.json")
		store, err := Load(path)
		require.NoError(t, err)
		require.NoError(t, store.Set("opencode-go", "sk-test"))
		require.True(t, store.Authenticated("opencode-go"))
		require.Equal(t, "sk-test", store.APIKey("opencode-go"))
		require.False(t, store.Authenticated("other"))
	})

	t.Run("names come back sorted", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials.json")
		store, err := Load(path)
		require.NoError(t, err)
		require.NoError(t, store.Set("zeta", "1"))
		require.NoError(t, store.Set("alpha", "2"))
		require.NoError(t, store.Reload())
		require.Equal(t, []string{"alpha", "zeta"}, store.Names())
	})
}

// TestSetPreservesUnknownFields verifies the contract the future OAuth
// release relies on: fields this release does not know survive a rewrite.
func TestSetPreservesUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	original := `{
  "future": {"api_key": "old", "refresh_token": "r1", "expires": 42}
}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	store, err := Load(path)
	require.NoError(t, err)
	require.NoError(t, store.Set("future", "new"))

	data, err := os.ReadFile(path) //nolint:gosec // the test owns the path.
	require.NoError(t, err)
	var got map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &got))
	require.JSONEq(t, `"new"`, string(got["future"]["api_key"]))
	require.JSONEq(t, `"r1"`, string(got["future"]["refresh_token"]))
	require.JSONEq(t, `42`, string(got["future"]["expires"]))
}

// TestSetKeepsOtherProviders verifies one write never drops another
// provider's entry.
func TestSetKeepsOtherProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := Load(path)
	require.NoError(t, err)
	require.NoError(t, store.Set("a", "1"))
	require.NoError(t, store.Set("b", "2"))

	fresh, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "1", fresh.APIKey("a"))
	require.Equal(t, "2", fresh.APIKey("b"))
}

// TestConcurrentWriters verifies the fresh-read write: a second instance's
// entry lands beside the first one's, not over it.
func TestConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	first, err := Load(path)
	require.NoError(t, err)
	second, err := Load(path)
	require.NoError(t, err)

	require.NoError(t, first.Set("a", "1"))
	require.NoError(t, second.Set("b", "2"))

	fresh, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "1", fresh.APIKey("a"), "stale second write must not erase a")
	require.Equal(t, "2", fresh.APIKey("b"))
}

// TestLoadMalformed verifies the documented malformed handling: the error is
// the sentinel the caller degrades into a warning.
func TestLoadMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	require.NoError(t, os.WriteFile(path, []byte("[not an object]"), 0o600))
	_, err := Load(path)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestSetEmptyKeyRemovesAuthentication verifies a blank key does not count
// as a credential.
func TestSetEmptyKeyRemovesAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := Load(path)
	require.NoError(t, err)
	require.NoError(t, store.Set("a", "  "))
	require.False(t, store.Authenticated("a"))
	require.Empty(t, store.APIKey("a"))
}

// TestFileModeOnUnix verifies the credentials never get world-readable.
func TestFileModeOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no file modes on windows")
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	store, err := Load(path)
	require.NoError(t, err)
	require.NoError(t, store.Set("a", "1"))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
