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
	userDir := t.TempDir()

	providers, err := providermod.Discover(t.Context(), providermod.Sources{
		Embedded: map[string]string{"opencode-go": withEndpoint(t, server.URL)},
		UserDir:  userDir,
	}, providermod.Options{}, nil)
	require.NoError(t, err)
	return providers
}

// planDatabase is the models.dev database the tests work against: one chat
// model, one Anthropic-protocol model and one unmapped-adapter model.
func planDatabase(api map[string]any) map[string]any {
	provider, _ := api["opencode-go"].(map[string]any)
	provider["models"] = map[string]any{
		"kimi-k3": map[string]any{
			"reasoning": true,
			"limit":     map[string]any{"context": 262144, "output": 65536},
		},
		"claude-haiku-5-5": map[string]any{
			"reasoning": true,
			"provider":  map[string]any{"npm": "@ai-sdk/anthropic"},
			"limit":     map[string]any{"context": 1000000, "output": 128000},
		},
		"gpt-5.6-luna": map[string]any{
			"reasoning": true,
			"provider":  map[string]any{"npm": "@ai-sdk/openai"},
			"limit":     map[string]any{"context": 1000000, "output": 128000},
		},
		"gemini-flash": map[string]any{
			"reasoning": true,
			"provider":  map[string]any{"npm": "@ai-sdk/gemini"},
			"limit":     map[string]any{"context": 100000, "output": 16000},
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
	}, ids, "the unmapped gemini model is excluded")
	require.Equal(
		t,
		1000000,
		decl.Models[0].ContextWindow,
		"the first roster entry is claude (alphabetical id order)",
	)
}

// TestOpencodeGoServesTheCacheOnFailure verifies the catalog contract: a
// failed fetch falls back to whatever the cache holds, and neither cache nor
// fetch yields an empty roster.
func TestOpencodeGoServesTheCacheOnFailure(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)

	userDir := t.TempDir()
	cacheDir := filepath.Join(userDir, "opencode-go", "cache")
	require.NoError(t, os.MkdirAll(cacheDir, 0o750))
	cached := []byte(
		`{"opencode-go":{"npm":"@ai-sdk/openai-compatible","models":{"kimi-k3":{"reasoning":true,"limit":{"context":1000,"output":100}}}}}`,
	)
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "models.json"), cached, 0o600))

	source := withEndpoint(t, dead.URL)

	providers, err := providermod.Discover(t.Context(), providermod.Sources{
		Embedded: map[string]string{"opencode-go": source},
		UserDir:  userDir,
	}, providermod.Options{}, nil)
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
	}, providermod.Options{}, nil)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Empty(t, providers[0].Decl.Models)
}
