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
}

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

// Model declares a model offered by a provider.
type Model struct {
	// ID is the provider model identifier sent on the wire, for example
	// "moonshotai/kimi-k2". It defaults to the alias of the model when empty.
	ID string `yaml:"id"`

	// MaxTokens overrides the response token limit when greater than zero.
	MaxTokens int `yaml:"max_tokens"`

	// ReasoningEffort selects the reasoning effort level, empty when the
	// model uses its provider default.
	ReasoningEffort string `yaml:"reasoning_effort"`

	// ReasoningBudgetTokens reserves a token budget for reasoning when
	// greater than zero and the model supports it.
	ReasoningBudgetTokens int `yaml:"reasoning_budget_tokens"`
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
	var cfg Config
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
		if model.MaxTokens < 0 {
			return fmt.Errorf("model %q: max_tokens must not be negative", alias)
		}
		if model.ReasoningBudgetTokens < 0 {
			return fmt.Errorf("model %q: reasoning_budget_tokens must not be negative", alias)
		}
	}
	return nil
}
