package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"go.yaml.in/yaml/v3"
)

// Config is the content of the Rienda configuration file.
//
// Providers and models live in JavaScript modules under
// "~/.rienda/providers"; the configuration only holds the settings that are
// Rienda's own, like the compaction of a conversation and the free config
// block the extensions read as ctx.config.
type Config struct {
	// Compaction configures how a conversation is summarized when it grows
	// too large for the context window of its model.
	Compaction Compaction

	// Config holds free-form extension settings, keyed by extension name. It
	// is nil when the file declares none.
	Config map[string]map[string]any

	// raw holds the file as declared, for Data.
	raw map[string]any
}

// strictConfig decodes the file with strict keys, accepting any value under
// config for later validation.
type strictConfig struct {
	Compaction Compaction `yaml:"compaction"`
	Config     any        `yaml:"config"`
}

// providersPresent mirrors the shape the removed providers block had, so an
// old file naming it gets the error that explains where providers live now.
type providersPresent struct {
	Providers map[string]any `yaml:"providers"`
}

// Compaction declares the settings of the conversation compaction. Parse
// fills the block with the documented defaults before it decodes the file,
// so an absent block, an empty one and a partial one all end up complete.
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

// DefaultPath returns the path of the default configuration file.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate home directory: %w", err)
	}
	return home + "/.rienda/config.yaml", nil
}

// Load reads and parses the configuration file at path. The file is
// optional: a user who runs only on built-in products needs none.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller selects the path.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{Compaction: defaultCompaction()}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse validates and converts the raw contents of a configuration file.
// The providers block no longer exists: providers live in JavaScript
// modules, so a file that declares one is refused with the pointer to the
// module mechanism.
func Parse(data []byte) (*Config, error) {
	removed := &providersPresent{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(removed); err == nil && len(removed.Providers) > 0 {
		return nil, errors.New(
			"config: providers are no longer declared here; define them as provider modules under ~/.rienda/providers/<name>/index.js",
		)
	}

	// The defaults are in place before the file is decoded, so a key the file
	// omits keeps the documented value and a key it declares overrides it.
	strict := strictConfig{Compaction: defaultCompaction()}
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&strict); err != nil {
		if errors.Is(err, io.EOF) {
			// An absent file and an empty one are the same thing now that the
			// providers do not live here: the defaults hold.
			return &Config{Compaction: defaultCompaction()}, nil
		}
		return nil, fmt.Errorf("config: invalid configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(
		&extra,
	); err != nil && !errors.Is(err, io.EOF) &&
		!errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, errors.New("config: file must hold a single YAML document")
	}

	free, err := normalizeConfig(strict.Config)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config: invalid configuration: %w", err)
	}
	cfg := &Config{
		Compaction: strict.Compaction,
		Config:     free,
		raw:        raw,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// normalizeConfig validates the free config block: absent stays absent, and
// a present one must be a map of maps.
func normalizeConfig(raw any) (map[string]map[string]any, error) {
	if raw == nil {
		//nolint:nilnil // an absent block stays absent by contract.
		return nil, nil
	}
	outer, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("config: config must be an object of objects")
	}
	config := make(map[string]map[string]any, len(outer))
	for name, values := range outer {
		inner, ok := values.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config: config %q must be an object", name)
		}
		config[name] = inner
	}
	return config, nil
}

// validate checks the compaction settings.
func (c *Config) validate() error {
	errs := []error{}
	if c.Compaction.ReserveTokens < 0 {
		errs = append(errs, errors.New("config: compaction: reserve_tokens must not be negative"))
	}
	if c.Compaction.KeepRecentTokens < 0 {
		errs = append(
			errs,
			errors.New("config: compaction: keep_recent_tokens must not be negative"),
		)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Data returns the whole file as plain data, with the snake_case keys of the
// file and the config block included, for readers that treat the
// configuration as a document. It reflects what the file declared, not the
// resolved defaults, and the returned map is independent of the Config.
func (c *Config) Data() map[string]any {
	raw := c.deepCopyRaw()
	if raw == nil {
		return map[string]any{}
	}
	return raw
}

// deepCopyRaw clones the raw document into plain data.
func (c *Config) deepCopyRaw() map[string]any {
	if c.raw == nil {
		return nil
	}
	cloned, _ := deepCopy(c.raw).(map[string]any)
	return cloned
}

// deepCopy clones plain YAML data so a caller cannot corrupt the parsed
// document. It recurses into the two nested collections YAML produces and
// passes everything else through; a configuration of four levels deep is
// already an extension block that scripts read, not a document to walk.
func deepCopy(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, inner := range typed {
			cloned[key] = deepCopy(inner)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, inner := range typed {
			cloned[i] = deepCopy(inner)
		}
		return cloned
	default:
		return value
	}
}
