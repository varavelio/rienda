package providermod

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/varavelio/rienda/internal/provider"
)

// Protocols lists the wire protocols a declaration may name.
var Protocols = []string{
	string(provider.ProtocolOpenAIResponses),
	string(provider.ProtocolOpenAIChatCompletions),
	string(provider.ProtocolAnthropic),
}

// Auths lists the credential kinds a declaration may name.
var Auths = []string{AuthAPIKey, AuthNone}

// validate reports every declared-shape violation of a declaration. Only the
// schema violations the spec names are recorded; the absence of an optional
// field never is. An empty slice means the declaration is valid.
func validate(decl Declaration) []error {
	var errs []error

	if err := validateProtocol(decl.Protocol); err != nil {
		errs = append(errs, err)
	}
	if decl.BaseURL == "" {
		errs = append(errs, errors.New("base_url is required"))
	}
	if !slices.Contains(Auths, decl.Auth) {
		errs = append(
			errs,
			fmt.Errorf("unknown auth %q (accepted: %s)", decl.Auth, strings.Join(Auths, ", ")),
		)
	}

	ids := make(map[string]bool, len(decl.Models))
	for _, model := range decl.Models {
		errs = append(errs, validateModel(model, ids)...)
	}
	return errs
}

// validateModel reports the violations of one model declaration, tracking the
// ids seen so far to reject duplicates inside one provider.
func validateModel(model ModelDeclaration, seen map[string]bool) []error {
	var errs []error

	if model.ID == "" {
		errs = append(errs, errors.New("models[].id is required"))
	} else if seen[model.ID] {
		errs = append(errs, fmt.Errorf("duplicate model id %q", model.ID))
	}
	seen[model.ID] = true

	if model.Protocol != "" {
		if err := validateProtocol(model.Protocol); err != nil {
			errs = append(errs, fmt.Errorf("model %q: %w", model.ID, err))
		}
	}
	if model.ContextWindow <= 0 {
		errs = append(
			errs,
			fmt.Errorf("model %q: context_window must be a positive number of tokens", model.ID),
		)
	}
	if thinking := validateThinking(model); thinking != nil {
		errs = append(errs, fmt.Errorf("model %q: %w", model.ID, thinking))
	}
	return errs
}

// validateThinking reports the violations of the thinking declaration of a
// model: the modes exist exactly when the model reasons, every level is a
// usable string, budgets are non-negative and no level repeats.
func validateThinking(model ModelDeclaration) error {
	if !model.Reasoning {
		if len(model.ThinkingModes) > 0 {
			return errors.New("thinking_modes are declared on a model that does not reason")
		}
		return nil
	}
	if model.ThinkingModes == nil {
		return errors.New(
			"thinking_modes are required on a reasoning model (an empty list is legal)",
		)
	}
	levels := make(map[string]bool, len(model.ThinkingModes))
	for _, mode := range model.ThinkingModes {
		switch {
		case mode.Level == "":
			return errors.New("a thinking mode declares no level")
		case mode.Level == "off" || mode.Level == "none":
			return fmt.Errorf("thinking level %q is reserved", mode.Level)
		case mode.MaxTokens < 0:
			return fmt.Errorf("thinking level %q: max_tokens must not be negative", mode.Level)
		case levels[mode.Level]:
			return fmt.Errorf("duplicate thinking level %q", mode.Level)
		}
		levels[mode.Level] = true
	}
	return nil
}

// validateProtocol reports whether a protocol name is one Rienda speaks.
func validateProtocol(name provider.Protocol) error {
	if !slices.Contains(Protocols, string(name)) {
		return fmt.Errorf("unknown protocol %q (accepted: %s)", name, strings.Join(Protocols, ", "))
	}
	return nil
}
