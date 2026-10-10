package native

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/providermod"
)

// builtinModule returns the embedded source of the built-in provider.
func builtinModule(t *testing.T) map[string]string {
	t.Helper()
	sources := Builtin()
	require.Contains(t, sources, "opencode-go")
	return sources
}

// modelsDevFixture serves the shape of the models.dev database that the
// built-in module reads, with the models the tests need.
func modelsDevFixture(t *testing.T, db map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(db)
	}))
	t.Cleanup(server.Close)
	return server
}

// withEndpoint replaces the models.dev URL the embedded module declares with
// the URL of a test server.
func withEndpoint(t *testing.T, endpoint string) string {
	t.Helper()
	source := builtinModule(t)["opencode-go"]
	require.Contains(t, source, "https://models.dev/api.json")
	return strings.ReplaceAll(source, "https://models.dev/api.json", endpoint)
}

// discoverBuiltIn discovers the embedded module, pointing its catalog fetch
// at the fixture server and its cache at a fresh directory.
func discoverBuiltIn(t *testing.T, db map[string]any) []providermod.Provider {
	t.Helper()
	server := modelsDevFixture(t, db)

	providers, err := providermod.Discover(t.Context(), providermod.Sources{
		Embedded: map[string]string{"opencode-go": withEndpoint(t, server.URL)},
		UserDir:  t.TempDir(),
	}, providermod.Options{CacheDir: t.TempDir()}, nil)
	require.NoError(t, err)
	return providers
}

// planDatabase is the models.dev database the tests work against: one chat
// model, one Anthropic-protocol model and one unmapped-adapter model.
func planDatabase(api map[string]any) map[string]any {
	provider, _ := api["opencode-go"].(map[string]any)
	provider["models"] = map[string]any{
		"kimi-k3": map[string]any{
			"name":      "Kimi K3",
			"reasoning": true,
			"reasoning_options": []any{
				map[string]any{"type": "effort", "values": []any{"none", "max"}},
			},
			"limit": map[string]any{"context": 262144, "output": 65536},
		},
		"claude-haiku-5-5": map[string]any{
			"name":      "Claude Haiku 5.5",
			"reasoning": true,
			"reasoning_options": []any{
				map[string]any{"type": "effort", "values": []any{"low", "high"}},
			},
			"provider": map[string]any{"npm": "@ai-sdk/anthropic"},
			"limit":    map[string]any{"context": 1000000, "output": 128000},
		},
		"gpt-5.6-luna": map[string]any{
			"name":      "GPT-5.6 Luna",
			"reasoning": true,
			"reasoning_options": []any{
				map[string]any{"type": "effort", "values": []any{"none", "low", "high"}},
			},
			"provider": map[string]any{"npm": "@ai-sdk/openai"},
			"limit":    map[string]any{"context": 1000000, "output": 128000},
		},
		"gemini-flash": map[string]any{
			"reasoning": true,
			"provider":  map[string]any{"npm": "@ai-sdk/gemini"},
			"limit":     map[string]any{"context": 100000, "output": 16000},
		},
		"untitled": map[string]any{
			"reasoning": true,
			"limit":     map[string]any{"context": 9000, "output": 900},
		},
	}
	return map[string]any{"opencode-go": provider}
}

// TestOpencodeGoDiscoversThePlan verifies the built-in module produces one
// provider with the plan connection and the mapped roster.
func TestOpencodeGoDiscoversThePlan(t *testing.T) {
	db := planDatabase(map[string]any{
		"opencode-go": map[string]any{
			"npm": "@ai-sdk/openai-compatible",
			"api": "https://opencode.ai/zen/go/v1",
		},
	})
	providers := discoverBuiltIn(t, db)
	require.Len(t, providers, 1)
	decl := providers[0].Decl

	require.Equal(t, "opencode-go", providers[0].Name)
	require.Equal(t, "https://opencode.ai/zen/go/v1", decl.BaseURL)
	require.Equal(t, "x-opencode-session", decl.SessionHeader)
	require.Equal(t, "api_key", decl.Auth)
	require.Equal(t, "openai_chat_completions", string(decl.Protocol))

	// The roster carries only the mapped adapters, each on the protocol its
	// adapter names.
	ids := map[string]string{}
	for _, model := range decl.Models {
		ids[model.ID] = string(model.Protocol)
	}
	require.Equal(t, map[string]string{
		"kimi-k3":          "openai_chat_completions",
		"claude-haiku-5-5": "anthropic",
		"gpt-5.6-luna":     "openai_responses",
		"untitled":         "openai_chat_completions",
	}, ids, "the unmapped gemini model is excluded")
	require.Equal(
		t,
		1000000,
		decl.Models[0].ContextWindow,
		"the first roster entry is claude (alphabetical id order)",
	)
}

// TestOpencodeGoCarriesTheDatabaseDetails verifies the roster fields the
// picker reads: the display name models.dev documents, the thinking levels
// its effort option documents, and the database shape left to the module.
func TestOpencodeGoCarriesTheDatabaseDetails(t *testing.T) {
	db := planDatabase(map[string]any{
		"opencode-go": map[string]any{
			"npm": "@ai-sdk/openai-compatible",
			"api": "https://opencode.ai/zen/go/v1",
		},
	})
	decl := discoverBuiltIn(t, db)[0].Decl

	byID := map[string]providermod.ModelDeclaration{}
	for _, model := range decl.Models {
		byID[model.ID] = model
	}
	require.Equal(t, "Claude Haiku 5.5", byID["claude-haiku-5-5"].Name)
	require.Equal(t, []providermod.ThinkingMode{
		{Level: "low"},
		{Level: "high"},
	}, byID["claude-haiku-5-5"].ThinkingModes, "the effort values become the modes, in order")

	// A model the database left unnamed falls back to nothing: the picker
	// then shows the id.
	require.Empty(t, byID["untitled"].Name)

	// The levels Rienda reserves are dropped, not refused: "none" selects
	// the picker entry that sends nothing.
	require.Equal(t, []providermod.ThinkingMode{{Level: "max"}}, byID["kimi-k3"].ThinkingModes)
}

// TestOpencodeGoServesTheCacheOnFailure verifies the catalog contract: a
// failed fetch falls back to whatever the cache holds, and neither cache nor
// fetch yields an empty roster.
func TestOpencodeGoServesTheCacheOnFailure(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)

	userDir, cacheDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(cacheDir, 0o750))
	stored, err := json.Marshal(map[string]any{
		"date": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"opencode-go": map[string]any{
				"npm": "@ai-sdk/openai-compatible",
				"models": map[string]any{
					"kimi-k3": map[string]any{
						"reasoning": true,
						"limit":     map[string]any{"context": 1000, "output": 100},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "models.dev.json"), stored, 0o600))

	providers, err := providermod.Discover(t.Context(), providermod.Sources{
		Embedded: map[string]string{"opencode-go": withEndpoint(t, dead.URL)},
		UserDir:  userDir,
	}, providermod.Options{CacheDir: cacheDir}, nil)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Len(t, providers[0].Decl.Models, 1)
	require.Equal(t, "kimi-k3", providers[0].Decl.Models[0].ID)
}

// TestOpencodeGoEmptyWithoutCacheOrFetch verifies the dead-end contract: no
// cache and a failed fetch leaves the provider loaded but starved, which is
// how the session reports no model available.
func TestOpencodeGoEmptyWithoutCacheOrFetch(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)

	userDir := t.TempDir()
	source := withEndpoint(t, dead.URL)

	providers, err := providermod.Discover(context.Background(), providermod.Sources{
		Embedded: map[string]string{"opencode-go": source},
		UserDir:  userDir,
	}, providermod.Options{CacheDir: t.TempDir()}, nil)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Empty(t, providers[0].Decl.Models)
}

// TestOpencodeGoRefetchesTheStaleCache verifies the refresh window: a young
// cache answers alone, an old one is refetched, and the refetch restamps the
// stored document.
func TestOpencodeGoRefetchesTheStaleCache(t *testing.T) {
	cached := map[string]any{
		"opencode-go": map[string]any{
			"npm": "@ai-sdk/openai-compatible",
			"models": map[string]any{
				"old-model": map[string]any{
					"reasoning": true,
					"limit":     map[string]any{"context": 500, "output": 50},
				},
			},
		},
	}
	store := func(t *testing.T, age time.Duration) string {
		t.Helper()
		dir := t.TempDir()
		stored, err := json.Marshal(map[string]any{
			"date": time.Now().Add(-age).UTC().Format(time.RFC3339),
			"data": cached,
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "models.dev.json"), stored, 0o600))
		return dir
	}
	db := planDatabase(map[string]any{
		"opencode-go": map[string]any{"npm": "@ai-sdk/openai-compatible"},
	})
	models := func(providers []providermod.Provider) []string {
		ids := make([]string, 0, len(providers[0].Decl.Models))
		for _, model := range providers[0].Decl.Models {
			ids = append(ids, model.ID)
		}
		return ids
	}

	// A young cache answers as it is: the roster carries only the served
	// model, whatever the live database holds.
	providers := discover(t, store(t, time.Hour), db)
	require.Equal(t, []string{"old-model"}, models(providers))

	// An old cache is refetched: the roster carries the live models, and
	// the refetch restamps the stored document.
	stale := store(t, 5*time.Hour)
	providers = discover(t, stale, db)
	require.Contains(t, models(providers), "kimi-k3")
	var refetched cachedDocument
	path := stale + "/models.dev.json"
	file, err := os.ReadFile(path) //nolint:gosec // the test stored the path itself.
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(file, &refetched))

	// The restamp carries the live database under a fresh date.
	restamp, ok := refetched.Data.(map[string]any)
	require.True(t, ok, "the stored data is the database: %T", refetched.Data)
	plan, ok := restamp["opencode-go"].(map[string]any)
	require.True(t, ok, "the database holds the plan: %T", restamp["opencode-go"])
	require.Contains(t, plan["models"], "kimi-k3")
	saved, parseErr := time.Parse(time.RFC3339, refetched.Date)
	require.NoError(t, parseErr)
	require.WithinDuration(t, time.Now(), saved, time.Minute)
}

// discover discovers the embedded module against a catalog endpoint whose
// cache lives in cacheDir: the share the module reaches through ctx.cache.
func discover(t *testing.T, cacheDir string, db map[string]any) []providermod.Provider {
	t.Helper()
	server := modelsDevFixture(t, db)

	providers, err := providermod.Discover(t.Context(), providermod.Sources{
		Embedded: map[string]string{"opencode-go": withEndpoint(t, server.URL)},
		UserDir:  t.TempDir(),
	}, providermod.Options{CacheDir: cacheDir}, nil)
	require.NoError(t, err)
	return providers
}

// cachedDocument mirrors the envelope the shared cache stores, so the tests
// read the stamped files the way the runtime writes them.
type cachedDocument struct {
	Date string `json:"date"`
	Data any    `json:"data"`
}
