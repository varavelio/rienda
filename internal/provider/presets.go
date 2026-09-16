package provider

import "sort"

// Preset is a connection template for a known provider service. It supplies the
// wire protocol and the endpoint root, so a provider entry only needs its own
// name and credential.
type Preset struct {
	// Protocol is the wire protocol the service speaks.
	Protocol Protocol

	// BaseURL is the service endpoint root. Each protocol client appends its
	// own request path.
	BaseURL string
}

// presets holds the built-in connection templates.
var presets = map[string]Preset{
	"opencode-go": {Protocol: ProtocolOpenAIChat, BaseURL: "https://opencode.ai/zen/go/v1"},
	"opencode":    {Protocol: ProtocolOpenAIChat, BaseURL: "https://opencode.ai/zen/v1"},
	"openai":      {Protocol: ProtocolOpenAIResponses, BaseURL: "https://api.openai.com/v1"},
	"anthropic":   {Protocol: ProtocolAnthropic, BaseURL: "https://api.anthropic.com"},
	"openrouter":  {Protocol: ProtocolOpenAIChat, BaseURL: "https://openrouter.ai/api/v1"},
	"deepseek":    {Protocol: ProtocolOpenAIChat, BaseURL: "https://api.deepseek.com"},
	"zai":         {Protocol: ProtocolOpenAIChat, BaseURL: "https://api.z.ai/api/paas/v4"},
	"lmstudio":    {Protocol: ProtocolOpenAIChat, BaseURL: "http://127.0.0.1:1234/v1"},
	"ollama":      {Protocol: ProtocolOpenAIChat, BaseURL: "http://127.0.0.1:11434/v1"},
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
