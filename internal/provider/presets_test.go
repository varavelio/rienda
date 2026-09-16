package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPresetByName(t *testing.T) {
	t.Run("returns the registered preset", func(t *testing.T) {
		preset, ok := PresetByName("opencode-go")

		require.True(t, ok)
		require.Equal(t, Preset{
			Protocol: ProtocolOpenAIChat,
			BaseURL:  "https://opencode.ai/zen/go/v1",
		}, preset)
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
			"opencode-go",
			"openrouter",
			"zai",
		}, PresetNames())
	})
}

func TestPresets(t *testing.T) {
	t.Run("every preset declares a supported protocol and an endpoint", func(t *testing.T) {
		for name, preset := range presets {
			require.Contains(t, Protocols(), preset.Protocol, "preset %q", name)
			require.NotEmpty(t, preset.BaseURL, "preset %q", name)
			require.NotContains(t, preset.BaseURL, " ", "preset %q", name)
		}
	})

	t.Run("pins the built-in endpoints", func(t *testing.T) {
		expected := map[string]Preset{
			"opencode-go": {ProtocolOpenAIChat, "https://opencode.ai/zen/go/v1"},
			"opencode":    {ProtocolOpenAIChat, "https://opencode.ai/zen/v1"},
			"openai":      {ProtocolOpenAIResponses, "https://api.openai.com/v1"},
			"anthropic":   {ProtocolAnthropic, "https://api.anthropic.com"},
			"openrouter":  {ProtocolOpenAIChat, "https://openrouter.ai/api/v1"},
			"deepseek":    {ProtocolOpenAIChat, "https://api.deepseek.com"},
			"zai":         {ProtocolOpenAIChat, "https://api.z.ai/api/paas/v4"},
			"lmstudio":    {ProtocolOpenAIChat, "http://127.0.0.1:1234/v1"},
			"ollama":      {ProtocolOpenAIChat, "http://127.0.0.1:11434/v1"},
		}

		require.Equal(t, expected, presets)
	})
}
