package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// stubTool is a minimal Tool used to exercise the registry.
type stubTool struct {
	name       string
	parameters json.RawMessage
}

// Definition returns the stubbed tool definition.
func (s stubTool) Definition() llm.Tool {
	return llm.Tool{Name: s.name, Description: "stub", Parameters: s.parameters}
}

// Execute returns a fixed result.
func (s stubTool) Execute(_ context.Context, _ Call, _ Sink) (Result, error) {
	return TextResult("stub"), nil
}

// stub builds a stub tool with a valid definition.
func stub(name string) stubTool {
	return stubTool{name: name, parameters: json.RawMessage(`{"type":"object"}`)}
}

// TestNewRegistry verifies registry construction and lookups.
func TestNewRegistry(t *testing.T) {
	t.Run("registers and looks up tools", func(t *testing.T) {
		registry, err := NewRegistry(stub("beta"), stub("alpha"))
		require.NoError(t, err)

		require.Equal(t, []string{"alpha", "beta"}, registry.Names())

		tool, ok := registry.Lookup("alpha")
		require.True(t, ok)
		require.Equal(t, "alpha", tool.Definition().Name)

		_, ok = registry.Lookup("missing")
		require.False(t, ok)
	})

	t.Run("rejects invalid tools", func(t *testing.T) {
		tests := []struct {
			name    string
			tools   []Tool
			wantErr string
		}{
			{name: "nil tool", tools: []Tool{nil}, wantErr: "nil tool"},
			{name: "invalid name", tools: []Tool{stub("bad name")}, wantErr: "invalid tool name"},
			{
				name:    "empty schema",
				tools:   []Tool{stubTool{name: "empty", parameters: json.RawMessage(``)}},
				wantErr: "valid JSON Schema",
			},
			{
				name:    "invalid schema",
				tools:   []Tool{stubTool{name: "broken", parameters: json.RawMessage(`{`)}},
				wantErr: "valid JSON Schema",
			},
			{
				name:    "duplicate name",
				tools:   []Tool{stub("same"), stub("same")},
				wantErr: "already registered",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, err := NewRegistry(test.tools...)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})
}

// TestRegistryDefinitions verifies definition resolution for agents.
func TestRegistryDefinitions(t *testing.T) {
	registry, err := NewRegistry(stub("alpha"), stub("beta"))
	require.NoError(t, err)

	t.Run("keeps the requested order", func(t *testing.T) {
		definitions, err := registry.Definitions([]string{"beta", "alpha"})
		require.NoError(t, err)
		require.Len(t, definitions, 2)
		require.Equal(t, "beta", definitions[0].Name)
		require.Equal(t, "alpha", definitions[1].Name)
	})

	t.Run("collapses repeated names", func(t *testing.T) {
		definitions, err := registry.Definitions([]string{"alpha", "alpha"})
		require.NoError(t, err)
		require.Len(t, definitions, 1)
	})

	t.Run("reports unknown tools", func(t *testing.T) {
		_, err := registry.Definitions([]string{"missing"})

		require.ErrorContains(t, err, "unknown tool")
		require.ErrorContains(t, err, "alpha")
	})

	t.Run("returns nothing for no names", func(t *testing.T) {
		definitions, err := registry.Definitions(nil)

		require.NoError(t, err)
		require.Nil(t, definitions)
	})
}

// TestValidName verifies the tool name charset and length rules.
func TestValidName(t *testing.T) {
	t.Run("accepts valid names", func(t *testing.T) {
		for _, name := range []string{"shell", "dc-shell", "a", "A1_2-3", strings.Repeat("a", 64)} {
			require.True(t, ValidName(name), "expected %q to be valid", name)
		}
	})

	t.Run("rejects invalid names", func(t *testing.T) {
		for _, name := range []string{"", strings.Repeat("a", 65), "with space", "dot.name", "colon:name", "ñ"} {
			require.False(t, ValidName(name), "expected %q to be invalid", name)
		}
	})
}
