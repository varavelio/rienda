//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// riendaDirName is the directory inside the home directory that holds the
// configuration, the agent definitions and the sessions of an instance.
const riendaDirName = ".rienda"

// providersEnv names the environment variable that points the binary at the
// provider modules of the instance, so no invocation reads the home of the
// developer running the suite.
const providersEnv = "RIENDA_PROVIDERS"

// Protocol names a provider speaks, the same values the provider modules
// declare in their canonical shape.
const (
	// ProtocolChat is the OpenAI Chat Completions protocol.
	ProtocolChat = "openai_chat_completions"
	// ProtocolAnthropic is the Anthropic Messages protocol.
	ProtocolAnthropic = "anthropic"
	// ProtocolResponses is the OpenAI Responses protocol.
	ProtocolResponses = "openai_responses"
)

// Values of the default provider of an instance.
const (
	// FakeProviderName is the provider the default instance declares.
	FakeProviderName = "fake"
	// DefaultModelID is the wire identifier the default model sends, and the
	// second half of the reference it answers to.
	DefaultModelID = "gpt-test"
	// TestAPIKey is the credential the default instance sends.
	TestAPIKey = "test-key"
)

// FakeModelRef is the model reference of the default instance, the model a
// run starts on when nothing names one.
const FakeModelRef = FakeProviderName + "/" + DefaultModelID

// Reasoning models default the window to this value, which is about what the
// suite is willing to fill.
const defaultFixtureWindow = 1_000_000

// Config describes the fixture of one instance: the provider modules written
// under ~/.rienda/providers and the compaction block of the configuration.
type Config struct {
	// Providers lists the provider modules of the instance.
	Providers []Provider

	// Compaction writes the compaction block of the configuration. A nil
	// value leaves the block absent, which the binary fills with its
	// defaults.
	Compaction *Compaction

	// ExtensionConfig writes the free config block of the configuration.
	ExtensionConfig map[string]map[string]any
}

// Compaction describes the compaction block of the configuration.
type Compaction struct {
	// Enabled switches the automatic compaction on and off. It is nil when
	// the test leaves the value to the default, which is on.
	Enabled *bool

	// ReserveTokens is the room the threshold leaves in the window.
	ReserveTokens int

	// KeepRecentTokens is the budget of the conversation tail kept verbatim.
	KeepRecentTokens int
}

// Provider declares one provider module: a connection and the models it
// offers, in the exact shape the provider declaration carries.
type Provider struct {
	// Name is the key of the provider, the first half of its model references.
	Name string

	// Protocol is the wire protocol models without their own protocol run.
	Protocol string

	// BaseURL is the endpoint root. An empty value points the provider at the
	// fake provider, whichever protocol it speaks.
	BaseURL string

	// APIKey authenticates every request against the provider. It travels
	// through the credentials file of the instance, where the product reads
	// it from.
	APIKey string

	// Headers adds static headers to every request.
	Headers map[string]string

	// SessionHeader carries the harness session ID, disabled when the
	// pointer names an empty string and inherited otherwise.
	SessionHeader *string

	// Models lists the roster of the provider.
	Models []Model
}

// Model declares one model of a roster: the wire identifier it sends and its
// generation settings. The reference of a model is provider/id.
type Model struct {
	// ID is the wire identifier and the second half of the reference.
	ID string

	// Protocol overrides the provider protocol for this model.
	Protocol string

	// ContextWindow declares the window of the model, which the compaction of
	// the conversation resolves. It defaults to a value large enough that the
	// suite never waits for a threshold unless a test asks for one.
	ContextWindow int

	// MaxTokens caps the response token limit when greater than zero.
	MaxTokens int

	// Temperature controls sampling randomness when set.
	Temperature *float64

	// TopP controls nucleus sampling when set.
	TopP *float64

	// Thinking declares the thinking modes of a reasoning model.
	Thinking []Thinking
}

// Thinking declares one thinking mode of a model.
type Thinking struct {
	// Level is the level the level-based protocols receive.
	Level string

	// MaxTokens is the budget the budget-based protocols receive.
	MaxTokens int
}

// DefaultConfig returns the fixture of a fresh instance: a single provider
// that talks to the fake provider and offers one chat model.
func DefaultConfig() Config {
	return Config{Providers: []Provider{{
		Name:     FakeProviderName,
		Protocol: ProtocolChat,
		APIKey:   TestAPIKey,
		Models:   []Model{{ID: DefaultModelID}},
	}}}
}

// DefaultCompaction returns the compaction block the fixtures declare: on,
// with the documented threshold and tail.
func DefaultCompaction() Compaction {
	enabled := true
	return Compaction{Enabled: &enabled}
}

// ConfigPath returns the path of the configuration file of the instance, the
// one the binary reads when nothing overrides it.
func (h *Harness) ConfigPath() string {
	return filepath.Join(h.home, riendaDirName, "config.yaml")
}

// AgentsDir returns the directory holding the agent definitions of the
// instance.
func (h *Harness) AgentsDir() string {
	return filepath.Join(h.home, riendaDirName, "agents")
}

// ProvidersDir returns the directory holding the provider modules of the
// instance.
func (h *Harness) ProvidersDir() string {
	if h.providers != "" {
		return h.providers
	}
	return filepath.Join(h.home, riendaDirName, "providers")
}

// WriteConfig writes the configuration of the provider modules that each test
// needs, with every provider that declares no BaseURL pointing at the fake
// provider, and returns the path of the resulting config.yaml.
func (h *Harness) WriteConfig(t *testing.T, dir string, cfg Config) string {
	t.Helper()
	h.install(t, cfg, h.ProvidersDir())
	path := filepath.Join(dir, "config.yaml")
	writeInstanceConfig(t, path, cfg)
	return path
}

// install writes one provider module per entry and the credentials of its
// key, into the directory the instance reads.
func (h *Harness) install(t *testing.T, cfg Config, dir string) {
	t.Helper()

	seen := make(map[string]bool, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		if provider.Name == "" {
			t.Fatal("harness: every provider declaration needs a name")
		}
		if seen[provider.Name] {
			t.Fatalf("harness: duplicate provider name %q", provider.Name)
		}
		seen[provider.Name] = true
		module := providerModule(t, provider, h.provider.BaseURL())
		providerDir := filepath.Join(dir, provider.Name)
		writeFile(t, filepath.Join(providerDir, "index.js"), module)
		if provider.APIKey != "" {
			h.writeCredential(t, provider.Name, provider.APIKey)
		}
	}
}

// writeCredential stores one api key into the credentials file of the
// instance, the exact file the product reads.
func (h *Harness) writeCredential(t *testing.T, name, apiKey string) {
	t.Helper()
	path := filepath.Join(h.home, riendaDirName, "credentials.json")
	document := map[string]map[string]string{}
	if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // the test owns the path.
		_ = json.Unmarshal(raw, &document)
	}
	document[name] = map[string]string{"api_key": apiKey}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("harness: encode credentials: %v", err)
	}
	writeFile(t, path, string(encoded)+"\n")
}

// modelEntry serializes one model into the canonical shape the module
// returns; a window of zero means the fixture default.
func modelEntry(model Model, indent string) string {
	window := model.ContextWindow
	if window == 0 {
		window = defaultFixtureWindow
	}
	var entry strings.Builder
	entry.WriteString(indent)
	entry.WriteString(`{ id: `)
	entry.WriteString(quote(model.ID))
	entry.WriteString(`, protocol: `)
	entry.WriteString(quote(model.Protocol))
	entry.WriteString(`, context_window: `)
	entry.WriteString(strconv.Itoa(window))
	entry.WriteString(`, reasoning: `)
	entry.WriteString(strconv.FormatBool(len(model.Thinking) > 0))
	entry.WriteString(`, thinking_modes: [`)
	for i, mode := range model.Thinking {
		if i > 0 {
			entry.WriteString(", ")
		}
		entry.WriteString(`{ level: `)
		entry.WriteString(quote(mode.Level))
		entry.WriteString(`, max_tokens: `)
		entry.WriteString(strconv.Itoa(mode.MaxTokens))
		entry.WriteString(" }")
	}
	entry.WriteString("]")
	if model.MaxTokens > 0 {
		entry.WriteString(`, max_tokens: `)
		entry.WriteString(strconv.Itoa(model.MaxTokens))
	}
	if model.Temperature != nil {
		fmt.Fprintf(&entry, ", temperature: %g", *model.Temperature)
	}
	if model.TopP != nil {
		fmt.Fprintf(&entry, ", top_p: %g", *model.TopP)
	}
	return entry.String() + " }"
}

// providerModule renders one provider declaration into a provider module: a
// function receiving ctx and returning the canonical shape.
func providerModule(t *testing.T, provider Provider, fakeURL string) string {
	t.Helper()

	baseURL := provider.BaseURL
	if baseURL == "" {
		baseURL = fakeURL
	}
	sessionHeader := ""
	if provider.SessionHeader != nil {
		sessionHeader = *provider.SessionHeader
	}
	roster := make([]string, 0, len(provider.Models))
	for _, model := range provider.Models {
		roster = append(roster, modelEntry(model, "\t\t\t"))
	}
	models := "[]"
	if len(roster) > 0 {
		models = "[\n" + strings.Join(roster, ",\n") + ",\n\t\t]"
	}
	headers := "{}"
	if len(provider.Headers) > 0 {
		A := make([]string, 0, len(provider.Headers))
		for key, value := range provider.Headers {
			A = append(A, quote(key)+": "+quote(value))
		}
		headers = "{ " + strings.Join(A, ", ") + " }"
	}
	return "\n" + strings.Join([]string{
		"module.exports = function (ctx) {",
		"\treturn {",
		"\t\tprotocol: " + quote(provider.Protocol) + ",",
		"\t\tbase_url: " + quote(baseURL) + ",",
		"\t\tsession_header: " + quote(sessionHeader) + ",",
		"\t\theaders: " + headers + ",",
		"\t\tauth: 'api_key',",
		"\t\tmodels: " + models + ",",
		"\t};",
		"};",
	}, "\n")
}

// WithConfig writes the given contents as a configuration file and returns
// its path, for tests that exercise configurations the binary must reject.
func (h *Harness) WithConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, contents)
	return path
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

// writeYAML writes a document as YAML, for the configuration file.
func writeYAML(t *testing.T, path string, document map[string]any) {
	t.Helper()
	encoded, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("harness: encode configuration: %v", err)
	}
	writeFile(t, path, string(encoded))
}

// writeInstanceConfig writes the configuration file of an instance: the
// compaction block and the free config block, nothing else, since providers
// live in the modules.
func writeInstanceConfig(t *testing.T, path string, cfg Config) {
	t.Helper()

	c := DefaultCompaction()
	if cfg.Compaction != nil && cfg.Compaction.Enabled != nil {
		c = *cfg.Compaction
	}
	// A block the test declares fully keeps every value it carries; one it
	// leaves to the defaults holds the documented ones.
	if cfg.Compaction == nil {
		c = Compaction{Enabled: &enabledTrue}
	}
	document := map[string]any{
		"compaction": map[string]any{
			"enabled":            c.Enabled != nil && *c.Enabled,
			"reserve_tokens":     c.ReserveTokens,
			"keep_recent_tokens": c.KeepRecentTokens,
		},
	}
	if cfg.ExtensionConfig != nil {
		document["config"] = cfg.ExtensionConfig
	}
	writeYAML(t, path, document)
}

// enabledTrue names the value a compaction block the fixture did not write
// carries.
var enabledTrue = true

// quote turns a Go string into a JavaScript single-quoted string literal.
func quote(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'") + "'"
}
