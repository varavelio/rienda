package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/provider"
)

// resolveFixture parses the configuration consumed by the resolution tests.
func resolveFixture(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(`
providers:
  openrouter:
    preset: openrouter
    api_key: sk-or-test
    headers:
      X-Title: rienda
    models:
      kimi-k2:
        id: moonshotai/kimi-k2
        max_tokens: 8192
        temperature: 0.7
        top_p: 0.9
        thinking_level: Medium
        thinking_max_tokens: 2048
      moonshotai/kimi-k2: {}
  zen-go:
    preset: opencode-go
    api_key: zen-test
    models:
      glm-5.3: {}
  local:
    protocol: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1/
    models:
      small: {}
  proxied-zen:
    preset: opencode
    base_url: https://proxy.example.com/zen/v1
    models:
      any: {}
  zen-go-custom-header:
    preset: opencode-go
    session_header: X-Proxy-Session
    models:
      glm-5.3: {}
  zen-go-no-session:
    preset: opencode-go
    session_header: ''
    models:
      glm-5.3: {}
  local-session:
    protocol: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    session_header: ' x-local-session '
    models:
      small: {}
`))
	require.NoError(t, err)
	return cfg
}

func TestResolve(t *testing.T) {
	t.Run("resolves a preset provider", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("zen-go/glm-5.3")

		require.NoError(t, err)
		require.Equal(t, Resolved{
			ProviderName: "zen-go",
			ModelName:    "glm-5.3",
			Protocol:     provider.ProtocolOpenAIChatCompletions,
			ProviderConfig: provider.Config{
				APIKey:            "zen-test",
				BaseURL:           "https://opencode.ai/zen/go/v1",
				SessionHeaderName: "x-opencode-session",
			},
			ModelID: "glm-5.3",
		}, resolved)
	})

	t.Run("resolves a custom endpoint", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("local/small")

		require.NoError(t, err)
		require.Equal(t, provider.ProtocolOpenAIChatCompletions, resolved.Protocol)
		require.Equal(t, "http://127.0.0.1:8080/v1/", resolved.ProviderConfig.BaseURL)
		require.Empty(t, resolved.ProviderConfig.SessionHeaderName)
		require.Empty(t, resolved.ProviderConfig.APIKey)
		require.Equal(t, "small", resolved.ModelID)
	})

	t.Run("uses the declared wire identifier and model defaults", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("openrouter/kimi-k2")

		require.NoError(t, err)
		require.Equal(t, provider.ProtocolOpenAIChatCompletions, resolved.Protocol)
		require.Equal(t, "https://openrouter.ai/api/v1", resolved.ProviderConfig.BaseURL)
		require.Equal(t, "sk-or-test", resolved.ProviderConfig.APIKey)
		require.Equal(
			t,
			map[string]string{"X-Title": "rienda"},
			resolved.ProviderConfig.ExtraHeaders,
		)
		require.Equal(t, "moonshotai/kimi-k2", resolved.ModelID)
		require.Equal(t, 8192, resolved.MaxTokens)
		require.Equal(t, new(0.7), resolved.Temperature)
		require.Equal(t, new(0.9), resolved.TopP)
		require.Equal(t, "medium", resolved.ThinkingLevel)
		require.Equal(t, 2048, resolved.ThinkingMaxTokens)
	})

	t.Run("supports model aliases containing slashes", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("openrouter/moonshotai/kimi-k2")

		require.NoError(t, err)
		require.Equal(t, "moonshotai/kimi-k2", resolved.ModelName)
		require.Equal(t, "moonshotai/kimi-k2", resolved.ModelID)
	})

	t.Run("lets the provider override the preset endpoint", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("proxied-zen/any")

		require.NoError(t, err)
		require.Equal(t, "https://proxy.example.com/zen/v1", resolved.ProviderConfig.BaseURL)
		require.Equal(t, provider.ProtocolOpenAIChatCompletions, resolved.Protocol)
	})

	t.Run("overrides the session header of a preset", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("zen-go-custom-header/glm-5.3")

		require.NoError(t, err)
		require.Equal(t, "X-Proxy-Session", resolved.ProviderConfig.SessionHeaderName)
	})

	t.Run("disables the session header of a preset", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("zen-go-no-session/glm-5.3")

		require.NoError(t, err)
		require.Empty(t, resolved.ProviderConfig.SessionHeaderName)
	})

	t.Run("declares and trims a session header for a custom endpoint", func(t *testing.T) {
		resolved, err := resolveFixture(t).Resolve("local-session/small")

		require.NoError(t, err)
		require.Equal(t, "x-local-session", resolved.ProviderConfig.SessionHeaderName)
	})

	t.Run("rejects malformed references", func(t *testing.T) {
		for _, ref := range []string{"", "kimi-k2", "/kimi-k2", "openrouter/"} {
			t.Run("ref "+ref, func(t *testing.T) {
				_, err := resolveFixture(t).Resolve(ref)

				require.ErrorContains(t, err, "must have the form provider/model")
			})
		}
	})

	t.Run("rejects unknown providers and models", func(t *testing.T) {
		cfg := resolveFixture(t)

		_, err := cfg.Resolve("ghost/model")
		require.ErrorContains(t, err, `unknown provider "ghost"`)

		_, err = cfg.Resolve("openrouter/ghost")
		require.ErrorContains(t, err, `unknown model "ghost" in provider "openrouter"`)
	})

	t.Run("reports an invalid provider declaration", func(t *testing.T) {
		cfg := &Config{Providers: map[string]Provider{
			"broken": {Preset: "nope", Models: map[string]Model{"m": {}}},
		}}

		_, err := cfg.Resolve("broken/m")

		require.ErrorContains(t, err, `provider "broken": unknown preset "nope"`)
	})
}
