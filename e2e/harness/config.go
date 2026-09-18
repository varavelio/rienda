//go:build e2e

package harness

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// riendaDirName is the directory inside the home directory that holds the
// configuration, the agent definitions and the sessions of an instance.
const riendaDirName = ".rienda"

// Wire protocol names accepted by the protocol field of a provider.
const (
	// ProtocolChat is the OpenAI Chat Completions protocol.
	ProtocolChat = "openai_chat_completions"
	// ProtocolAnthropic is the Anthropic Messages protocol.
	ProtocolAnthropic = "anthropic"
	// ProtocolResponses is the OpenAI Responses protocol.
	ProtocolResponses = "openai_responses"
)

// Values of the default configuration of an instance.
const (
	// FakeProviderName is the provider the default configuration declares.
	FakeProviderName = "fake"
	// DefaultModelAlias is the model alias the default configuration declares.
	DefaultModelAlias = "model"
	// DefaultModelID is the wire identifier the default model maps to.
	DefaultModelID = "gpt-test"
	// TestAPIKey is the credential the default configuration sends.
	TestAPIKey = "test-key"
)

// FakeModelRef is the model reference of the default configuration, the value
// the model field of an agent definition declares.
const FakeModelRef = FakeProviderName + "/" + DefaultModelAlias

// Config describes the rienda configuration file of one instance, written in
// the exact YAML format the binary reads.
type Config struct {
	// Providers lists the provider connections available to the agents.
	Providers []Provider
}

// Provider declares one entry of the providers map of the configuration.
type Provider struct {
	// Name is the key of the provider, the first half of the model references
	// of the agents that use it.
	Name string

	// Preset names a built-in connection template. It is mutually exclusive
	// with Protocol.
	Preset string

	// Protocol names the wire protocol of a custom endpoint. It requires
	// BaseURL and is mutually exclusive with Preset.
	Protocol string

	// BaseURL overrides the endpoint root. An empty value points the provider
	// at the fake provider of the harness, whichever connection it declares.
	BaseURL string

	// APIKey authenticates every request against the provider.
	APIKey string

	// Headers adds static headers to every request against the provider.
	Headers map[string]string

	// SessionHeader overrides the header that carries the harness session id.
	// When nil the provider inherits the header of its connection, and a
	// pointer to an empty string disables it.
	SessionHeader *string

	// Models maps the model aliases of the provider to their settings.
	Models []Model
}

// Model declares one entry of the models map of a provider.
type Model struct {
	// Alias is the key of the model, the second half of a model reference.
	Alias string

	// ID is the provider model identifier sent on the wire. It defaults to
	// the alias when empty.
	ID string

	// MaxTokens caps the response token limit when greater than zero.
	MaxTokens int

	// Temperature controls sampling randomness when set.
	Temperature *float64

	// TopP controls nucleus sampling when set.
	TopP *float64

	// ThinkingLevel selects the extended thinking level of the model.
	ThinkingLevel string

	// ThinkingMaxTokens reserves a token budget for thinking.
	ThinkingMaxTokens int
}

// DefaultConfig returns the configuration of a fresh instance: a single
// provider that talks to the fake provider of the harness and offers one
// model.
func DefaultConfig() Config {
	return Config{Providers: []Provider{{
		Name:     FakeProviderName,
		Protocol: ProtocolChat,
		APIKey:   TestAPIKey,
		Models:   []Model{{Alias: DefaultModelAlias, ID: DefaultModelID}},
	}}}
}

// ConfigPath returns the path of the default configuration file of the
// instance, the one the binary reads when nothing overrides it.
func (h *Harness) ConfigPath() string {
	return filepath.Join(h.home, riendaDirName, configFileName)
}

// AgentsDir returns the directory holding the agent definitions of the
// instance.
func (h *Harness) AgentsDir() string {
	return filepath.Join(h.home, riendaDirName, agentsDirName)
}

// WriteConfig writes another configuration file of the instance, with every
// fake provider pointing at the fake provider of the harness, and returns its
// path. Tests use it to point an invocation at a configuration of their own
// through --config or RIENDA_CONFIG.
func (h *Harness) WriteConfig(t *testing.T, dir string, cfg Config) string {
	t.Helper()
	path := filepath.Join(dir, configFileName)
	cfg.write(t, path, h.provider.BaseURL())
	return path
}

// write stores the configuration document at path, with every provider that
// declares no base URL pointing at the given fake endpoint.
func (c Config) write(t *testing.T, path, fakeBaseURL string) {
	t.Helper()

	document := configDocument{Providers: make(map[string]providerDocument, len(c.Providers))}
	for _, provider := range c.Providers {
		if provider.Name == "" {
			t.Fatal("harness: every provider declaration needs a name")
		}
		if _, taken := document.Providers[provider.Name]; taken {
			t.Fatalf("harness: duplicate provider name %q", provider.Name)
		}
		document.Providers[provider.Name] = provider.document(t, fakeBaseURL)
	}

	contents, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("harness: encode configuration: %v", err)
	}
	writeFile(t, path, string(contents))
}

// document converts a provider declaration into its YAML form.
func (p Provider) document(t *testing.T, fakeBaseURL string) providerDocument {
	t.Helper()

	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = fakeBaseURL
	}

	models := make(map[string]modelDocument, len(p.Models))
	for _, model := range p.Models {
		if model.Alias == "" {
			t.Fatalf("harness: provider %q declares a model without an alias", p.Name)
		}
		models[model.Alias] = modelDocument{
			ID:                model.ID,
			MaxTokens:         model.MaxTokens,
			Temperature:       model.Temperature,
			TopP:              model.TopP,
			ThinkingLevel:     model.ThinkingLevel,
			ThinkingMaxTokens: model.ThinkingMaxTokens,
		}
	}

	return providerDocument{
		Preset:        p.Preset,
		Protocol:      p.Protocol,
		BaseURL:       baseURL,
		APIKey:        p.APIKey,
		Headers:       p.Headers,
		SessionHeader: p.SessionHeader,
		Models:        models,
	}
}

// configDocument mirrors the YAML document of a configuration file.
type configDocument struct {
	Providers map[string]providerDocument `yaml:"providers"`
}

// providerDocument mirrors one entry of the providers map.
type providerDocument struct {
	Preset        string                   `yaml:"preset,omitempty"`
	Protocol      string                   `yaml:"protocol,omitempty"`
	BaseURL       string                   `yaml:"base_url,omitempty"`
	APIKey        string                   `yaml:"api_key,omitempty"`
	Headers       map[string]string        `yaml:"headers,omitempty"`
	SessionHeader *string                  `yaml:"session_header,omitempty"`
	Models        map[string]modelDocument `yaml:"models,omitempty"`
}

// modelDocument mirrors one entry of the models map.
type modelDocument struct {
	ID                string   `yaml:"id,omitempty"`
	MaxTokens         int      `yaml:"max_tokens,omitempty"`
	Temperature       *float64 `yaml:"temperature,omitempty"`
	TopP              *float64 `yaml:"top_p,omitempty"`
	ThinkingLevel     string   `yaml:"thinking_level,omitempty"`
	ThinkingMaxTokens int      `yaml:"thinking_max_tokens,omitempty"`
}

// writeFile writes contents into path, creating its parent directory, and
// fails the test when the file cannot be written.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("harness: create directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("harness: write %s: %v", path, err)
	}
}
