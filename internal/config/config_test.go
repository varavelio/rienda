package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustParse parses a configuration for the test to consume.
func mustParse(t *testing.T, data string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(data))
	require.NoError(t, err)
	return cfg
}

func TestParse(t *testing.T) {
	t.Run("parses presets, custom endpoints and models", func(t *testing.T) {
		cfg := mustParse(t, `
providers:
  openrouter:
    preset: openrouter
    api_key: sk-or-test
    headers:
      X-Title: rienda
    models:
      kimi-k2:
        id: moonshotai/kimi-k2
        context_window: 262144
        max_tokens: 8192
        temperature: 0.7
        top_p: 0.9
        thinking_level: Medium
        thinking_max_tokens: 2048
  local:
    protocol: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    session_header: X-Session-Id
    models:
      small: {}
`)

		require.Equal(t, map[string]Provider{
			"openrouter": {
				Preset:  "openrouter",
				APIKey:  "sk-or-test",
				Headers: map[string]string{"X-Title": "rienda"},
				Models: map[string]Model{
					"kimi-k2": {
						ID:                "moonshotai/kimi-k2",
						ContextWindow:     262144,
						MaxTokens:         8192,
						Temperature:       new(0.7),
						TopP:              new(0.9),
						ThinkingLevel:     "Medium",
						ThinkingMaxTokens: 2048,
					},
				},
			},
			"local": {
				Protocol:      "openai_chat_completions",
				BaseURL:       "http://127.0.0.1:8080/v1",
				SessionHeader: new("X-Session-Id"),
				Models:        map[string]Model{"small": {}},
			},
		}, cfg.Providers)
	})

	t.Run("accepts an empty api key for credential-less services", func(t *testing.T) {
		cfg := mustParse(
			t,
			"providers:\n  local:\n    preset: ollama\n    models:\n      llama: {}\n",
		)

		require.Empty(t, cfg.Providers["local"].APIKey)
	})

	t.Run("defaults the compaction settings", func(t *testing.T) {
		cfg := mustParse(t, "providers:\n  local:\n    preset: ollama\n")

		require.Equal(t, Compaction{
			Enabled:          true,
			ReserveTokens:    16384,
			KeepRecentTokens: 20000,
		}, cfg.Compaction)
	})

	t.Run("reads an explicit compaction block", func(t *testing.T) {
		cfg := mustParse(t, `
providers:
  openrouter:
    preset: openrouter
    models:
      kimi-k2: {}
compaction:
  enabled: false
  reserve_tokens: 4096
  keep_recent_tokens: 1000
  model: openrouter/kimi-k2
`)

		require.Equal(t, Compaction{
			Enabled:          false,
			ReserveTokens:    4096,
			KeepRecentTokens: 1000,
			Model:            "openrouter/kimi-k2",
		}, cfg.Compaction)
	})

	t.Run("accepts a provider without models", func(t *testing.T) {
		cfg := mustParse(t, "providers:\n  ollama:\n    preset: ollama\n")

		require.Empty(t, cfg.Providers["ollama"].Models)
	})

	t.Run("rejects invalid configurations", func(t *testing.T) {
		cases := []struct {
			name string
			data string
			want string
		}{
			{"empty file", "", "file is empty"},
			{"unknown top-level key", "providers: {}\nother: true\n", "invalid configuration"},
			{
				"unknown provider key",
				"providers:\n  p:\n    preset: openrouter\n    other: true\n",
				"invalid configuration",
			},
			{
				"unknown model key",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        other: true\n",
				"invalid configuration",
			},
			{
				"multiple documents",
				"providers:\n  p:\n    preset: openrouter\n---\nproviders: {}\n",
				"single YAML document",
			},
			{"no providers", "providers: {}\n", "at least one provider is required"},
			{
				"empty provider name",
				"providers:\n  '':\n    preset: openrouter\n",
				"provider names must not be empty",
			},
			{
				"provider name with slash",
				"providers:\n  'a/b':\n    preset: openrouter\n",
				"must not contain a slash",
			},
			{
				"preset and protocol together",
				"providers:\n  p:\n    preset: openrouter\n    protocol: openai_chat_completions\n    base_url: http://127.0.0.1:8080/v1\n",
				"mutually exclusive",
			},
			{
				"missing preset and protocol",
				"providers:\n  p:\n    base_url: http://127.0.0.1:8080/v1\n",
				"preset or protocol is required",
			},
			{"unknown preset", "providers:\n  p:\n    preset: nope\n", "unknown preset"},
			{
				"unknown protocol",
				"providers:\n  p:\n    protocol: gemini\n    base_url: http://127.0.0.1:8080/v1\n",
				"unknown protocol",
			},
			{
				"protocol without base url",
				"providers:\n  p:\n    protocol: openai_chat_completions\n",
				"base_url is required",
			},
			{
				"invalid session header",
				"providers:\n  p:\n    preset: openrouter\n    session_header: 'not a header'\n",
				"is not a valid HTTP header name",
			},
			{
				"empty model alias",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      '': {}\n",
				"model aliases must not be empty",
			},
			{
				"negative context window",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        context_window: -1\n",
				"context_window must not be negative",
			},
			{
				"negative max tokens",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        max_tokens: -1\n",
				"max_tokens must not be negative",
			},
			{
				"temperature below range",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        temperature: -0.1\n",
				"temperature -0.1 must be between 0 and 2",
			},
			{
				"temperature above range",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        temperature: 2.1\n",
				"temperature 2.1 must be between 0 and 2",
			},
			{
				"top_p below range",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        top_p: -0.1\n",
				"top_p -0.1 must be between 0 and 1",
			},
			{
				"top_p above range",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        top_p: 1.1\n",
				"top_p 1.1 must be between 0 and 1",
			},
			{
				"negative thinking max tokens",
				"providers:\n  p:\n    preset: openrouter\n    models:\n      m:\n        thinking_max_tokens: -1\n",
				"thinking_max_tokens must not be negative",
			},
			{
				"unknown compaction key",
				"providers:\n  p:\n    preset: openrouter\ncompaction:\n  other: true\n",
				"invalid configuration",
			},
			{
				"negative compaction reserve",
				"providers:\n  p:\n    preset: openrouter\ncompaction:\n  reserve_tokens: -1\n",
				"reserve_tokens must not be negative",
			},
			{
				"negative compaction tail",
				"providers:\n  p:\n    preset: openrouter\ncompaction:\n  keep_recent_tokens: -1\n",
				"keep_recent_tokens must not be negative",
			},
			{
				"unknown compaction model",
				"providers:\n  p:\n    preset: openrouter\ncompaction:\n  model: p/ghost\n",
				`compaction: unknown model "ghost" in provider "p"`,
			},
			{
				"malformed compaction model reference",
				"providers:\n  p:\n    preset: openrouter\ncompaction:\n  model: ghost\n",
				"must have the form provider/model",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := Parse([]byte(tc.data))

				require.ErrorContains(t, err, tc.want)
			})
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("reads and parses a configuration file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(
			t,
			os.WriteFile(path, []byte("providers:\n  local:\n    preset: ollama\n"), 0o600),
		)

		cfg, err := Load(path)

		require.NoError(t, err)
		require.Contains(t, cfg.Providers, "local")
	})

	t.Run("reports a missing file with a clear message", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")

		_, err := Load(path)

		require.ErrorContains(t, err, path)
		require.ErrorContains(t, err, "does not exist")
	})

	t.Run("reports an unreadable path", func(t *testing.T) {
		dir := t.TempDir()

		_, err := Load(dir)

		require.ErrorContains(t, err, "read")
		require.ErrorContains(t, err, dir)
	})

	t.Run("reports the path when the content is invalid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(
			t,
			os.WriteFile(path, []byte("providers:\n  p:\n    preset: nope\n"), 0o600),
		)

		_, err := Load(path)

		require.ErrorContains(t, err, path)
		require.ErrorContains(t, err, "unknown preset")
	})
}

func TestDefaultPath(t *testing.T) {
	t.Run("appends the rienda configuration file to the home directory", func(t *testing.T) {
		home := filepath.Join("home", "tester")
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)

		path, err := DefaultPath()

		require.NoError(t, err)
		require.Equal(t, filepath.Join(home, ".rienda", "config.yaml"), path)
	})

	t.Run("fails when the home directory is unknown", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")

		_, err := DefaultPath()

		require.Error(t, err)
	})
}

// TestModelRefs verifies the roster of models a front end offers.
func TestModelRefs(t *testing.T) {
	t.Run("returns every model as a sorted reference", func(t *testing.T) {
		cfg := mustParse(t, `
providers:
  zeta:
    preset: openrouter
    models:
      second: {}
      first: {}
  alpha:
    preset: openrouter
    models:
      only: {}
`)

		require.Equal(t, []string{"alpha/only", "zeta/first", "zeta/second"}, cfg.ModelRefs())
	})

	t.Run("returns every reference Resolve accepts", func(t *testing.T) {
		cfg := mustParse(t, `
providers:
  fake:
    preset: openrouter
    models:
      one: {}
      two: {}
`)

		refs := cfg.ModelRefs()
		require.Len(t, refs, 2)
		for _, ref := range refs {
			_, err := cfg.Resolve(ref)
			require.NoError(t, err, "the roster only offers references that resolve")
		}
	})

	t.Run("returns nothing for a configuration without models", func(t *testing.T) {
		cfg := mustParse(t, `
providers:
  fake:
    preset: openrouter
`)

		require.Empty(t, cfg.ModelRefs())
	})
}

func TestProviderNames(t *testing.T) {
	t.Run("returns the configured names in sorted order", func(t *testing.T) {
		cfg := mustParse(t, `
providers:
  zeta:
    preset: openrouter
  alpha:
    preset: openrouter
  middle:
    preset: openrouter
`)

		require.Equal(t, []string{"alpha", "middle", "zeta"}, cfg.ProviderNames())
	})
}
