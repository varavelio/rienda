package config

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/varavelio/rienda/internal/provider"
)

// Resolved is the outcome of resolving a model reference against the
// configuration. It carries everything needed to build a provider client.
type Resolved struct {
	// ProviderName is the configuration name of the provider.
	ProviderName string

	// ModelName is the configuration alias of the model.
	ModelName string

	// Protocol is the wire protocol of the provider.
	Protocol provider.Protocol

	// ProviderConfig holds the connection settings of the provider.
	ProviderConfig provider.Config

	// ModelID is the provider model identifier to send on the wire.
	ModelID string

	// MaxTokens caps the response token limit when greater than zero.
	MaxTokens int

	// Temperature controls sampling randomness when set.
	Temperature *float64

	// TopP controls nucleus sampling when set.
	TopP *float64

	// ThinkingLevel selects the extended thinking level, empty when the model
	// uses its provider default.
	ThinkingLevel string

	// ThinkingMaxTokens reserves a token budget for thinking when greater than
	// zero.
	ThinkingMaxTokens int
}

// Resolve looks up a model reference in provider/model form and returns the
// connection settings and model defaults needed to talk to it. The model name
// may contain slashes: only the first one separates the provider from the
// model.
func (c *Config) Resolve(ref string) (Resolved, error) {
	providerName, modelName, found := strings.Cut(ref, "/")
	providerName, modelName = strings.TrimSpace(providerName), strings.TrimSpace(modelName)
	if !found || providerName == "" || modelName == "" {
		return Resolved{}, fmt.Errorf("model reference %q must have the form provider/model", ref)
	}

	declaration, found := c.Providers[providerName]
	if !found {
		return Resolved{}, fmt.Errorf("unknown provider %q", providerName)
	}
	model, found := declaration.Models[modelName]
	if !found {
		return Resolved{}, fmt.Errorf("unknown model %q in provider %q", modelName, providerName)
	}
	protocol, baseURL, sessionHeader, err := declaration.connection()
	if err != nil {
		return Resolved{}, fmt.Errorf("provider %q: %w", providerName, err)
	}

	modelID := model.ID
	if modelID == "" {
		modelID = modelName
	}
	return Resolved{
		ProviderName: providerName,
		ModelName:    modelName,
		Protocol:     protocol,
		ProviderConfig: provider.Config{
			APIKey:            declaration.APIKey,
			BaseURL:           baseURL,
			ExtraHeaders:      maps.Clone(declaration.Headers),
			SessionHeaderName: sessionHeader,
		},
		ModelID:           modelID,
		MaxTokens:         model.MaxTokens,
		Temperature:       model.Temperature,
		TopP:              model.TopP,
		ThinkingLevel:     strings.ToLower(strings.TrimSpace(model.ThinkingLevel)),
		ThinkingMaxTokens: model.ThinkingMaxTokens,
	}, nil
}

// connection resolves the wire protocol, endpoint root and session header of
// a provider declaration.
func (p Provider) connection() (provider.Protocol, string, string, error) {
	sessionHeader, declared, err := p.sessionHeader()
	if err != nil {
		return "", "", "", err
	}

	switch {
	case p.Preset != "" && p.Protocol != "":
		return "", "", "", errors.New("preset and protocol are mutually exclusive")

	case p.Preset != "":
		preset, found := provider.PresetByName(p.Preset)
		if !found {
			return "", "", "", fmt.Errorf("unknown preset %q", p.Preset)
		}
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = preset.BaseURL
		}
		header := preset.SessionHeader
		if declared {
			header = sessionHeader
		}
		return preset.Protocol, baseURL, header, nil

	case p.Protocol != "":
		protocol, err := provider.ParseProtocol(p.Protocol)
		if err != nil {
			return "", "", "", fmt.Errorf("invalid protocol: %w", err)
		}
		if strings.TrimSpace(p.BaseURL) == "" {
			return "", "", "", errors.New("base_url is required when protocol is set")
		}
		header := ""
		if declared {
			header = sessionHeader
		}
		return protocol, p.BaseURL, header, nil

	default:
		return "", "", "", errors.New("preset or protocol is required")
	}
}

// sessionHeader returns the session header explicitly declared by the
// provider, trimmed, and whether it was declared. When it is not declared the
// provider inherits the header of its connection; when it is declared empty
// the header is disabled.
func (p Provider) sessionHeader() (string, bool, error) {
	if p.SessionHeader == nil {
		return "", false, nil
	}

	header := strings.TrimSpace(*p.SessionHeader)
	if header != "" {
		for _, r := range header {
			if !isHeaderNameRune(r) {
				return "", false, fmt.Errorf(
					"session_header %q is not a valid HTTP header name",
					*p.SessionHeader,
				)
			}
		}
	}
	return header, true, nil
}

// isHeaderNameRune reports whether r is valid in an HTTP header field name.
func isHeaderNameRune(r rune) bool {
	const separators = "()<>@,;:\\\"/[]?={}"
	return r > ' ' && r < 0x7f && !strings.ContainsRune(separators, r)
}
