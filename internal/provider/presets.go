package provider

import "sort"

// Preset is a connection template for a known provider service. It supplies the
// wire protocol, the endpoint root and, when the service requires it, the
// header carrying the harness session ID.
type Preset struct {
	// Protocol is the wire protocol the service speaks.
	Protocol Protocol

	// BaseURL is the service endpoint root. Each protocol client appends its
	// own request path.
	BaseURL string

	// SessionHeader is the header the service expects the harness session ID
	// in, or empty when the service does not use one.
	SessionHeader string
}

const (
	// sessionHeaderOpenCode is the session header required by OpenCode Go.
	sessionHeaderOpenCode = "x-opencode-session"
	opencodeGoBaseURL     = "https://opencode.ai/zen/go/v1"
	opencodeZenBaseURL    = "https://opencode.ai/zen/v1"
)

// presets holds the built-in connection templates.
//
// OpenCode Go and OpenCode Zen serve each model family through a single
// endpoint, so both need one preset per protocol:
//
//   - chat completions: GLM, Kimi, DeepSeek, MiMo, LongCat and Hy (Go); most
//     open models in Zen.
//   - responses: Grok, GPT and Muse Spark (Go); GPT, Grok and Muse Spark (Zen).
//   - messages: MiniMax and Qwen (Go); Claude and Qwen (Zen).
//
// The Gemini models of Zen use a protocol this package does not implement, so
// they are out of scope.
var presets = map[string]Preset{
	"anthropic": {
		Protocol: ProtocolAnthropic,
		BaseURL:  "https://api.anthropic.com",
	},
	"deepseek": {
		Protocol: ProtocolOpenAIChatCompletions,
		BaseURL:  "https://api.deepseek.com",
	},
	"lmstudio": {
		Protocol: ProtocolOpenAIChatCompletions,
		BaseURL:  "http://127.0.0.1:1234/v1",
	},
	"ollama": {
		Protocol: ProtocolOpenAIChatCompletions,
		BaseURL:  "http://127.0.0.1:11434/v1",
	},
	"openai": {
		Protocol: ProtocolOpenAIResponses,
		BaseURL:  "https://api.openai.com/v1",
	},
	"opencode": {
		Protocol: ProtocolOpenAIChatCompletions,
		BaseURL:  opencodeZenBaseURL,
	},
	"opencode-anthropic": {
		Protocol: ProtocolAnthropic,
		BaseURL:  opencodeZenBaseURL,
	},
	"opencode-responses": {
		Protocol: ProtocolOpenAIResponses,
		BaseURL:  opencodeZenBaseURL,
	},
	"opencode-go": {
		Protocol:      ProtocolOpenAIChatCompletions,
		BaseURL:       opencodeGoBaseURL,
		SessionHeader: sessionHeaderOpenCode,
	},
	"opencode-go-anthropic": {
		Protocol:      ProtocolAnthropic,
		BaseURL:       opencodeGoBaseURL,
		SessionHeader: sessionHeaderOpenCode,
	},
	"opencode-go-responses": {
		Protocol:      ProtocolOpenAIResponses,
		BaseURL:       opencodeGoBaseURL,
		SessionHeader: sessionHeaderOpenCode,
	},
	"openrouter": {
		Protocol: ProtocolOpenAIChatCompletions,
		BaseURL:  "https://openrouter.ai/api/v1",
	},
	"zai-coding-plan": {
		Protocol: ProtocolOpenAIChatCompletions,
		BaseURL:  "https://api.z.ai/api/coding/paas/v4",
	},
}

// PresetByName returns the connection template registered under name.
func PresetByName(name string) (Preset, bool) {
	preset, ok := presets[name]
	return preset, ok
}

// PresetNames returns the registered preset names in sorted order.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
