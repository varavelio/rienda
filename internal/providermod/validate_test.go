package providermod

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/provider"
)

// okDeclaration is a declaration every rule accepts; each test bends one
// field of a copy of it.
func okDeclaration() Declaration {
	return Declaration{
		Protocol:      provider.ProtocolOpenAIChatCompletions,
		BaseURL:       "https://example.test/v1",
		SessionHeader: "x-session",
		Auth:          AuthAPIKey,
		Models: []ModelDeclaration{{
			ID:            "model-a",
			ContextWindow: 128_000,
			Reasoning:     true,
			ThinkingModes: []ThinkingMode{{Level: "low", MaxTokens: 2048}},
		}},
	}
}

// TestValidate gathers every shape rule the spec declares into one table:
// each case bends one field of a valid declaration and must be rejected.
func TestValidate(t *testing.T) {
	fn := func(t *testing.T, bend func(*Declaration)) bool {
		t.Helper()
		decl := okDeclaration()
		bend(&decl)
		return len(validate(decl)) > 0
	}

	t.Run("accepts a fully valid declaration", func(t *testing.T) {
		require.Empty(t, validate(okDeclaration()))
	})

	t.Run("accepts an empty roster and non-reasoning models", func(t *testing.T) {
		decl := okDeclaration()
		decl.Models = []ModelDeclaration{{ID: "m", ContextWindow: 1_000}}
		require.Empty(t, validate(decl))
	})

	t.Run("rejects", func(t *testing.T) {
		cases := []struct {
			name string
			bend func(*Declaration)
		}{
			{"unknown provider protocol", func(d *Declaration) { d.Protocol = "gemini" }},
			{"empty base url", func(d *Declaration) { d.BaseURL = "" }},
			{"unknown auth", func(d *Declaration) { d.Auth = "oauth" }},
			{"model without id", func(d *Declaration) {
				d.Models[0].ID = ""
			}},
			{"model without context window", func(d *Declaration) {
				d.Models[0].ContextWindow = 0
			}},
			{"negative context window", func(d *Declaration) {
				d.Models[0].ContextWindow = -1
			}},
			{"duplicate model ids", func(d *Declaration) {
				d.Models = append(d.Models, d.Models[0])
			}},
			{"unknown model protocol", func(d *Declaration) {
				d.Models[0].Protocol = "gemini"
			}},
			{"thinking modes on a non-reasoning model", func(d *Declaration) {
				d.Models[0].Reasoning = false
				d.Models[0].ThinkingModes = []ThinkingMode{{Level: "low"}}
			}},
			{"missing thinking modes on a reasoning model", func(d *Declaration) {
				d.Models[0].Reasoning = true
				d.Models[0].ThinkingModes = nil
			}},
			{"thinking mode without level", func(d *Declaration) {
				d.Models[0].ThinkingModes[0].Level = ""
			}},
			{"reserved thinking level", func(d *Declaration) {
				d.Models[0].ThinkingModes[0].Level = "off"
			}},
			{"negative thinking budget", func(d *Declaration) {
				d.Models[0].ThinkingModes[0].MaxTokens = -1
			}},
			{"duplicate thinking levels", func(d *Declaration) {
				d.Models[0].ThinkingModes = append(
					d.Models[0].ThinkingModes,
					ThinkingMode{Level: "low", MaxTokens: 1},
				)
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.True(t, fn(t, tc.bend))
			})
		}
	})

	t.Run("errors name the offending field", func(t *testing.T) {
		decl := okDeclaration()
		decl.Auth = "oauth"
		errs := validate(decl)
		require.Len(t, errs, 1)
		require.Contains(t, errs[0].Error(), "auth")
	})

	t.Run("empty thinking list on a reasoning model is a legal roster", func(t *testing.T) {
		decl := okDeclaration()
		decl.Models[0].ThinkingModes = []ThinkingMode{}
		errs := validate(decl)
		require.Empty(t, errs)
	})

	t.Run("every protocol name round-trips", func(t *testing.T) {
		for _, name := range Protocols {
			decl := okDeclaration()
			decl.Protocol = provider.Protocol(name)
			require.Empty(t, validate(decl), "protocol %q", name)
		}
	})

	t.Run("auth names round-trip", func(t *testing.T) {
		for _, name := range Auths {
			decl := okDeclaration()
			decl.Auth = name
			require.Empty(t, validate(decl), "auth %q", name)
		}
	})

	t.Run("error list is stable about the first violation", func(t *testing.T) {
		decl := okDeclaration()
		decl.Auth = "oauth"
		decl.Models[0].ContextWindow = 0
		errs := validate(decl)
		require.GreaterOrEqual(t, len(errs), 2)
		require.True(
			t,
			strings.Contains(errs[0].Error(), "auth") ||
				strings.Contains(errs[0].Error(), "base_url"),
		)
	})
}
