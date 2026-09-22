package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testCatalog builds a catalog over a temporary cache directory, or over the
// directory of the options when they declare one, and returns it together with
// the path of its cache file.
func testCatalog(t *testing.T, opts Options) (*Catalog, string) {
	t.Helper()

	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	built, err := New(opts)
	require.NoError(t, err)
	return built, filepath.Join(opts.Dir, cacheFileName)
}

// writeCache stores a reduced cache at path, mapping every identifier to the
// context window it declares.
func writeCache(t *testing.T, path string, refreshedAt time.Time, models map[string]int) {
	t.Helper()

	reduced := make(map[string]model, len(models))
	for id, window := range models {
		reduced[id] = model{ContextWindow: window}
	}

	data, err := json.Marshal(cache{RefreshedAt: refreshedAt, Models: reduced})
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// writeRawCache stores data at path, so a test can assert the exact shape the
// cache file holds.
func writeRawCache(t *testing.T, path string, data []byte) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// TestNew verifies the resolution of the cache directory and the endpoint.
func TestNew(t *testing.T) {
	t.Run("defaults the endpoint and the cache directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(endpointEnvVar, "")

		built, err := New(Options{})

		require.NoError(t, err)
		require.Equal(t, DefaultEndpoint, built.endpoint)
		require.Equal(t, filepath.Join(home, ".rienda", "cache", cacheFileName), built.path)
		require.Equal(t, refreshInterval, built.interval)
	})

	t.Run("reads the endpoint from the environment", func(t *testing.T) {
		t.Setenv(endpointEnvVar, " http://127.0.0.1:9/models.json ")

		built, err := New(Options{Dir: t.TempDir()})

		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:9/models.json", built.endpoint)
	})

	t.Run("prefers the endpoint of the options", func(t *testing.T) {
		t.Setenv(endpointEnvVar, "http://127.0.0.1:9/models.json")

		built, err := New(Options{Dir: t.TempDir(), Endpoint: "http://127.0.0.1:8/models.json"})

		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:8/models.json", built.endpoint)
	})
}

// TestResolve verifies the lookup of a model context window in the cache.
func TestResolve(t *testing.T) {
	base := time.Date(2026, time.September, 22, 3, 0, 0, 0, time.UTC)

	t.Run("resolves an exact model id", func(t *testing.T) {
		cat, path := testCatalog(t, Options{Now: func() time.Time { return base }})
		writeCache(t, path, base, map[string]int{"kimi-k2.6": 262144})

		window, found := cat.Resolve("kimi-k2.6")

		require.True(t, found)
		require.Equal(t, 262144, window)
	})

	t.Run("resolves a path-style id by its last segment", func(t *testing.T) {
		cat, path := testCatalog(t, Options{Now: func() time.Time { return base }})
		writeCache(t, path, base, map[string]int{"kimi-k2.6": 262144})

		window, found := cat.Resolve("moonshotai/kimi-k2.6")

		require.True(t, found)
		require.Equal(t, 262144, window)
	})

	t.Run("normalizes the case and the padding of both sides", func(t *testing.T) {
		cat, path := testCatalog(t, Options{Now: func() time.Time { return base }})
		// The database key is stored in the case it arrived in and the lookup
		// is written in another, so the two only meet once both are
		// normalized.
		writeCache(t, path, base, map[string]int{"Kimi-K2.6": 262144})

		for _, modelID := range []string{
			"kimi-k2.6",
			"KIMI-K2.6",
			" MoonshotAI/Kimi-K2.6 ",
			"moonshotai/kimi-k2.6",
		} {
			window, found := cat.Resolve(modelID)

			require.True(t, found, "model %q", modelID)
			require.Equal(t, 262144, window, "model %q", modelID)
		}
	})

	t.Run("ignores unknown and empty model ids", func(t *testing.T) {
		cat, path := testCatalog(t, Options{})
		writeCache(t, path, base, map[string]int{"kimi-k2.6": 262144})

		for _, modelID := range []string{"ghost", "", "  ", "moonshotai/"} {
			window, found := cat.Resolve(modelID)

			require.False(t, found, "model %q", modelID)
			require.Zero(t, window, "model %q", modelID)
		}
	})

	t.Run("ignores a window that is not positive", func(t *testing.T) {
		cat, path := testCatalog(t, Options{})
		writeCache(t, path, base, map[string]int{"kimi-k2.6": 0})

		_, found := cat.Resolve("kimi-k2.6")

		require.False(t, found)
	})

	t.Run("ignores a missing cache", func(t *testing.T) {
		cat, _ := testCatalog(t, Options{})

		_, found := cat.Resolve("kimi-k2.6")

		require.False(t, found)
	})

	t.Run("ignores a corrupt cache", func(t *testing.T) {
		cat, path := testCatalog(t, Options{})
		writeRawCache(t, path, []byte("not json"))

		_, found := cat.Resolve("kimi-k2.6")

		require.False(t, found)
	})

	t.Run("reads the shape the cache file holds", func(t *testing.T) {
		cat, path := testCatalog(t, Options{Now: func() time.Time { return base }})
		// The value of a model is a map of facts, not a bare figure, so a fact
		// added later never breaks a cache an older version wrote.
		writeRawCache(t, path, []byte(`{
			"refreshedAt": "2026-09-22T03:00:00Z",
			"models": {"kimi-k2.6": {"context_window": 262144}}
		}`))

		window, found := cat.Resolve("kimi-k2.6")

		require.True(t, found)
		require.Equal(t, 262144, window)
	})

	t.Run("ignores a value that carries no context window", func(t *testing.T) {
		cat, path := testCatalog(t, Options{})
		writeRawCache(t, path, []byte(
			`{"refreshedAt":"2026-09-22T03:00:00Z","models":{"kimi-k2.6":{"other":1}}}`,
		))

		_, found := cat.Resolve("kimi-k2.6")

		require.False(t, found)
	})

	t.Run("answers from an expired cache", func(t *testing.T) {
		cat, path := testCatalog(t, Options{Now: func() time.Time { return base }})
		writeCache(t, path, base.Add(-cacheTTL-time.Hour), map[string]int{"kimi-k2.6": 262144})

		window, found := cat.Resolve("kimi-k2.6")

		require.True(t, found)
		require.Equal(t, 262144, window)
	})
}

// TestWindow verifies the three levels the context window is resolved from.
func TestWindow(t *testing.T) {
	base := time.Date(2026, time.September, 22, 3, 0, 0, 0, time.UTC)

	t.Run("prefers the declared window", func(t *testing.T) {
		cat, path := testCatalog(t, Options{})
		writeCache(t, path, base, map[string]int{"kimi-k2.6": 262144})

		require.Equal(t, 8192, cat.Window("kimi-k2.6", 8192))
	})

	t.Run("answers from the catalog when nothing is declared", func(t *testing.T) {
		cat, path := testCatalog(t, Options{})
		writeCache(t, path, base, map[string]int{"kimi-k2.6": 262144})

		require.Equal(t, 262144, cat.Window("moonshotai/kimi-k2.6", 0))
	})

	t.Run("falls back for an unknown model", func(t *testing.T) {
		cat, _ := testCatalog(t, Options{})

		require.Equal(t, FallbackWindow, cat.Window("ghost", 0))
	})

	t.Run("falls back when a declared window is not usable", func(t *testing.T) {
		cat, _ := testCatalog(t, Options{})

		require.Equal(t, FallbackWindow, cat.Window("ghost", -1))
	})
}
