package providermod

import (
	"context"

	"github.com/varavelio/rienda/internal/provider"
)

// Wire protocol names the canonical declaration accepts. They are the exact
// protocol names the provider clients implement.
const (
	// ProtocolOpenAIResponses is the OpenAI Responses protocol.
	ProtocolOpenAIResponses = provider.ProtocolOpenAIResponses
	// ProtocolOpenAIChatCompletions is the OpenAI Chat Completions protocol.
	ProtocolOpenAIChatCompletions = provider.ProtocolOpenAIChatCompletions
	// ProtocolAnthropic is the Anthropic Messages protocol.
	ProtocolAnthropic = provider.ProtocolAnthropic
)

// Auth names a provider needs. Only these two values are declared today; the
// field exists so future credential flows (OAuth and friends) arrive as a
// value, not a format change.
const (
	// AuthAPIKey names an Authorization-header credential.
	AuthAPIKey = "api_key"
	// AuthNone names a provider that carries no credential.
	AuthNone = "none"
)

// Declaration is the canonical export of a provider module: the connection
// every request of the provider makes and the roster of models the user may
// pick from. Models carry their own overrides of the connection fields.
type Declaration struct {
	// Protocol is the wire protocol models without their own protocol run.
	Protocol provider.Protocol `json:"protocol,omitempty"`

	// BaseURL is the provider endpoint root. Every protocol client appends
	// its own request path.
	BaseURL string `json:"base_url,omitempty"`

	// SessionHeader carries the harness session ID, empty for none.
	SessionHeader string `json:"session_header,omitempty"`

	// Headers are the static headers of every request. A model may replace
	// any key per model.
	Headers map[string]string `json:"headers"`

	// Auth names the credential the provider needs, "api_key" or "none".
	Auth string `json:"auth"`

	// Models is the roster of the provider. An empty roster is legal: it
	// declares a provider the user authenticates but cannot use yet.
	Models []ModelDeclaration `json:"models"`
}

// ModelDeclaration is one entry of a model roster: the wire model plus its
// overrides of the provider connection and its generation settings.
type ModelDeclaration struct {
	// ID is the provider model identifier sent on the wire. It is the second
	// half of every model reference of this model.
	ID string `json:"id"`

	// Protocol overrides the provider protocol for this model, empty to
	// inherit.
	Protocol provider.Protocol `json:"protocol,omitempty"`

	// BaseURL overrides the provider endpoint for this model, empty to
	// inherit.
	BaseURL string `json:"base_url,omitempty"`

	// SessionHeader overrides the provider session header for this model,
	// empty to inherit.
	SessionHeader string `json:"session_header,omitempty"`

	// Headers replaces same-named keys of the provider headers for this
	// model; other provider keys survive. Empty adds nothing.
	Headers map[string]string `json:"headers"`

	// ContextWindow is the context window of the model in tokens. It is
	// authoritative: nothing else supplies a value when it is absent.
	ContextWindow int `json:"context_window"`

	// MaxOutputTokens caps the response tokens when greater than zero.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	// Reasoning reports whether the model supports extended thinking.
	Reasoning bool `json:"reasoning"`

	// ThinkingModes are the levels the user may pick. Non-empty only when
	// Reasoning is true.
	ThinkingModes []ThinkingMode `json:"thinking_modes,omitempty"`

	// MaxTokens caps a generation field when greater than zero.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature controls sampling randomness when set.
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling when set.
	TopP *float64 `json:"top_p,omitempty"`
}

// ThinkingMode is one entry of the thinking modes of a model: the level a
// level-based wire API receives and the token budget a budget-based one
// receives.
type ThinkingMode struct {
	// Level is the wire level, for example "low", "high" or any string the
	// provider author declares.
	Level string `json:"level"`

	// MaxTokens is the thinking budget in tokens, zero when the model is
	// invoked on a level-based wire and the budget is not declared.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// Provider is one discovered provider: the module name and the declaration
// its code returned.
type Provider struct {
	// Name is the provider name, the first half of every model reference.
	Name string

	// Decl is the declaration the module returned.
	Decl Declaration
}

// Refs returns the model references of the provider in declaration order.
func (p Provider) Refs() []string {
	refs := make([]string, 0, len(p.Decl.Models))
	for _, model := range p.Decl.Models {
		refs = append(refs, p.Name+"/"+model.ID)
	}
	return refs
}

// Sources names where Discover finds the provider modules.
type Sources struct {
	// Embedded maps built-in provider names to their module source.
	Embedded map[string]string

	// UserDir is the user provider directory: each of its subdirectories
	// that holds an index.js is one user provider. A missing directory
	// yields no user providers and is not an error.
	UserDir string
}

// Options carries what Discover needs beyond the sources. It mirrors the
// options a provider module receives through its ctx.
type Options struct {
	// Config is the free config block of the configuration, as plain data:
	// ctx.config.
	Config any

	// MaxOutputBytes raises the payload cap the primitives of the module
	// honor, which a catalog large enough needs. Zero keeps the default of
	// the extension runtime.
	MaxOutputBytes int64
}

// Discover resolves the provider set: user modules shadow embedded ones by
// name, then every winner is compiled and executed once, sequentially in
// ascending name order, and every returned declaration is validated. A
// module that fails to load, run or validate is logged through logf and
// skipped; the rest continue. The returned providers are sorted by name.
func Discover(
	ctx context.Context,
	src Sources,
	opts Options,
	logf func(format string, args ...any),
) ([]Provider, error) {
	return runDiscovery(ctx, src, opts, logf)
}
