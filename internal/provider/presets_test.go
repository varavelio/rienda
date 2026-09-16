package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// newPreset builds the expected connection template of a built-in preset.
func newPreset(protocol Protocol, baseURL, sessionHeader string) Preset {
	return Preset{Protocol: protocol, BaseURL: baseURL, SessionHeader: sessionHeader}
}

func TestPresetByName(t *testing.T) {
	t.Run("returns the registered preset", func(t *testing.T) {
		preset, ok := PresetByName("opencode-go")

		require.True(t, ok)
		require.Equal(t, newPreset(
			ProtocolOpenAIChatCompletions,
			"https://opencode.ai/zen/go/v1",
			"x-opencode-session",
		), preset)
	})

	t.Run("reports an unknown preset", func(t *testing.T) {
		preset, ok := PresetByName("nope")

		require.False(t, ok)
		require.Equal(t, Preset{}, preset)
	})
}

func TestPresetNames(t *testing.T) {
	t.Run("returns the names in sorted order", func(t *testing.T) {
		require.Equal(t, []string{
			"anthropic",
			"deepseek",
			"lmstudio",
			"ollama",
			"openai",
			"opencode",
			"opencode-anthropic",
			"opencode-go",
			"opencode-go-anthropic",
			"opencode-go-responses",
			"opencode-responses",
			"openrouter",
			"zai-coding-plan",
		}, PresetNames())
	})
}

func TestPresets(t *testing.T) {
	t.Run("every preset declares a supported protocol and a clean endpoint", func(t *testing.T) {
		for name, preset := range presets {
			require.Contains(t, Protocols(), preset.Protocol, "preset %q", name)
			require.NotEmpty(t, preset.BaseURL, "preset %q", name)
			require.NotContains(t, preset.BaseURL, " ", "preset %q", name)
			require.NotContains(t, preset.SessionHeader, " ", "preset %q", name)
		}
	})

	t.Run("pins the built-in endpoints", func(t *testing.T) {
		expected := map[string]Preset{
			"anthropic": newPreset(
				ProtocolAnthropic,
				"https://api.anthropic.com",
				"",
			),
			"deepseek": newPreset(
				ProtocolOpenAIChatCompletions,
				"https://api.deepseek.com",
				"",
			),
			"lmstudio": newPreset(
				ProtocolOpenAIChatCompletions,
				"http://127.0.0.1:1234/v1",
				"",
			),
			"ollama": newPreset(
				ProtocolOpenAIChatCompletions,
				"http://127.0.0.1:11434/v1",
				"",
			),
			"openai": newPreset(
				ProtocolOpenAIResponses,
				"https://api.openai.com/v1",
				"",
			),
			"opencode": newPreset(
				ProtocolOpenAIChatCompletions,
				"https://opencode.ai/zen/v1",
				"",
			),
			"opencode-anthropic": newPreset(
				ProtocolAnthropic,
				"https://opencode.ai/zen/v1",
				"",
			),
			"opencode-responses": newPreset(
				ProtocolOpenAIResponses,
				"https://opencode.ai/zen/v1",
				"",
			),
			"opencode-go": newPreset(
				ProtocolOpenAIChatCompletions,
				"https://opencode.ai/zen/go/v1",
				"x-opencode-session",
			),
			"opencode-go-anthropic": newPreset(
				ProtocolAnthropic,
				"https://opencode.ai/zen/go/v1",
				"x-opencode-session",
			),
			"opencode-go-responses": newPreset(
				ProtocolOpenAIResponses,
				"https://opencode.ai/zen/go/v1",
				"x-opencode-session",
			),
			"openrouter": newPreset(
				ProtocolOpenAIChatCompletions,
				"https://openrouter.ai/api/v1",
				"",
			),
			"zai-coding-plan": newPreset(
				ProtocolOpenAIChatCompletions,
				"https://api.z.ai/api/coding/paas/v4",
				"",
			),
		}

		require.Equal(t, expected, presets)
	})
}
