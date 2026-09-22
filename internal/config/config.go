package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the content of the Rienda configuration file.
type Config struct {
	// Providers maps provider names to their connection and model settings.
	Providers map[string]Provider `yaml:"providers"`

	// Compaction configures how a conversation is summarized when it grows too
	// large for the context window of its model.
	Compaction Compaction `yaml:"compaction"`
}

// Compaction declares the settings of the conversation compaction. Parse fills
// the block with the documented defaults before it decodes the file, so an
// absent block, an empty one and a partial one all end up complete.
type Compaction struct {
	// Enabled switches the automatic compaction on and off. It is true by
	// default, because the point of the feature is that the user does not
	// manage the context. The manual compaction is always available.
	Enabled bool `yaml:"enabled"`

	// ReserveTokens is the room the threshold leaves in the window for the
	// answer, so the compaction arrives before the request would exceed it.
	ReserveTokens int `yaml:"reserve_tokens"`

	// KeepRecentTokens is the budget of the conversation tail kept verbatim
	// after a compaction.
	KeepRecentTokens int `yaml:"keep_recent_tokens"`

	// Model is the provider/model reference that produces the summary, empty
	// to summarize with the model of the session.
	Model string `yaml:"model"`
}

// defaultCompaction returns the compaction settings of a configuration that
// declares none: the feature on, with the documented reserve and tail.
func defaultCompaction() Compaction {
	return Compaction{
		Enabled:          DefaultCompactionEnabled,
		ReserveTokens:    DefaultCompactionReserveTokens,
		KeepRecentTokens: DefaultCompactionKeepRecentTokens,
	}
}

// Defaults of the compaction settings.
const (
	// DefaultCompactionEnabled is the state of the automatic compaction when
	// the configuration does not declare one.
	DefaultCompactionEnabled = true

	// DefaultCompactionReserveTokens is the room reserved for the answer.
	DefaultCompactionReserveTokens = 16384

	// DefaultCompactionKeepRecentTokens is the tail kept verbatim.
	DefaultCompactionKeepRecentTokens = 20000
)

// Provider declares the connection to a provider service and the models it
// offers.
type Provider struct {
	// Preset names a built-in connection template. It is mutually exclusive
	// with Protocol.
	Preset string `yaml:"preset"`

	// Protocol names the wire protocol of a custom endpoint. It requires
	// BaseURL and is mutually exclusive with Preset.
	Protocol string `yaml:"protocol"`

	// BaseURL overrides the endpoint root. It is required for custom
	// endpoints and replaces the preset endpoint when both are set.
	BaseURL string `yaml:"base_url"`

	// APIKey authenticates requests. It may be empty for services that do
	// not require credentials.
	APIKey string `yaml:"api_key"`

	// Headers adds static headers to every request against the provider.
	Headers map[string]string `yaml:"headers"`

	// SessionHeader overrides the header that carries the harness session
	// ID. When unset, a preset contributes its own header and a custom
	// endpoint has none. An explicit empty value disables the header.
	SessionHeader *string `yaml:"session_header"`

	// Models maps model aliases to their settings. Aliases are local names;
	// the wire identifier of a model is Model.ID.
	Models map[string]Model `yaml:"models"`
}

// ModelNames returns the aliases of the models the provider offers, in sorted
// order.
func (p Provider) ModelNames() []string {
	names := make([]string, 0, len(p.Models))
	for name := range p.Models {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Model declares a model offered by a provider. Every field except ID and
// ContextWindow is a generation setting that shapes how the model is invoked:
// the configuration is the single place where generation parameters live, and
// agents reference a model by alias to inherit them.
type Model struct {
	// ID is the provider model identifier sent on the wire, for example
	// "moonshotai/kimi-k2". It defaults to the alias of the model when empty.
	ID string `yaml:"id"`

	// ContextWindow declares the context window of the model in tokens. It is
	// optional: an undeclared window is resolved from the model catalog and
	// falls back to a conservative default.
	ContextWindow int `yaml:"context_window"`

	// MaxTokens caps the response token limit when greater than zero.
	MaxTokens int `yaml:"max_tokens"`

	// Temperature controls sampling randomness when set.
	Temperature *float64 `yaml:"temperature"`

	// TopP controls nucleus sampling when set.
	TopP *float64 `yaml:"top_p"`

	// ThinkingLevel selects the extended thinking level, empty when the model
	// uses its provider default.
	ThinkingLevel string `yaml:"thinking_level"`

	// ThinkingMaxTokens reserves a token budget for thinking when greater than
	// zero and the model supports it.
	ThinkingMaxTokens int `yaml:"thinking_max_tokens"`
}

// DefaultPath returns the path of the default configuration file.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate home directory: %w", err)
	}
	return filepath.Join(home, ".rienda", "config.yaml"), nil
}

// Parse validates and converts the raw contents of a configuration file.
func Parse(data []byte) (*Config, error) {
	// The defaults are in place before the file is decoded, so a key the file
	// omits keeps the documented value and a key it declares overrides it.
	cfg := Config{Compaction: defaultCompaction()}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config: file is empty")
		}
		return nil, fmt.Errorf("config: invalid configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("config: file must hold a single YAML document")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Load reads and parses the configuration file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user selects the path.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf(
				"config: %s does not exist, create it to declare your providers",
				path,
			)
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// ProviderNames returns the configured provider names in sorted order.
func (c *Config) ProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ModelRefs returns every configured model as a provider/model reference in
// sorted order, which is the roster a front end offers to switch the model of
// a session. The references are the ones Resolve accepts, so an offer a user
// picks is always a reference the configuration holds.
func (c *Config) ModelRefs() []string {
	refs := make([]string, 0, len(c.Providers))
	for _, providerName := range c.ProviderNames() {
		provider := c.Providers[providerName]
		for _, modelName := range provider.ModelNames() {
			refs = append(refs, providerName+"/"+modelName)
		}
	}
	slices.Sort(refs)
	return refs
}

// validate checks the configuration for structural problems and verifies that
// every provider declares a usable connection.
func (c *Config) validate() error {
	if len(c.Providers) == 0 {
		return errors.New("config: at least one provider is required")
	}

	for name, declaration := range c.Providers {
		if name == "" {
			return errors.New("config: provider names must not be empty")
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("config: provider name %q must not contain a slash", name)
		}
		if err := declaration.validate(); err != nil {
			return fmt.Errorf("config: provider %q: %w", name, err)
		}
	}
	return c.validateCompaction()
}

// validateCompaction checks the compaction settings.
func (c *Config) validateCompaction() error {
	switch {
	case c.Compaction.ReserveTokens < 0:
		return errors.New("config: compaction: reserve_tokens must not be negative")
	case c.Compaction.KeepRecentTokens < 0:
		return errors.New("config: compaction: keep_recent_tokens must not be negative")
	case strings.TrimSpace(c.Compaction.Model) != "":
		if _, err := c.Resolve(c.Compaction.Model); err != nil {
			return fmt.Errorf("config: compaction: %w", err)
		}
	}
	return nil
}

// validate checks the provider connection settings and its models.
func (p Provider) validate() error {
	if _, _, _, err := p.connection(); err != nil {
		return err
	}

	for alias, model := range p.Models {
		if alias == "" {
			return errors.New("model aliases must not be empty")
		}
		if err := model.validate(); err != nil {
			return fmt.Errorf("model %q: %w", alias, err)
		}
	}
	return nil
}

// validate checks the generation settings of a model.
func (m Model) validate() error {
	switch {
	case m.ContextWindow < 0:
		return errors.New("context_window must not be negative")
	case m.MaxTokens < 0:
		return errors.New("max_tokens must not be negative")
	case m.Temperature != nil && (*m.Temperature < 0 || *m.Temperature > 2):
		return fmt.Errorf("temperature %v must be between 0 and 2", *m.Temperature)
	case m.TopP != nil && (*m.TopP < 0 || *m.TopP > 1):
		return fmt.Errorf("top_p %v must be between 0 and 1", *m.TopP)
	case m.ThinkingMaxTokens < 0:
		return errors.New("thinking_max_tokens must not be negative")
	}
	return nil
}
