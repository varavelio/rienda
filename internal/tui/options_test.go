package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseOptions verifies the argument handling of the interactive
// interface.
func TestParseOptions(t *testing.T) {
	t.Run("applies the defaults", func(t *testing.T) {
		parsed, err := parseOptions(nil)

		require.NoError(t, err)
		require.Equal(t, options{}, parsed)
	})

	t.Run("parses the short flags", func(t *testing.T) {
		parsed, err := parseOptions(
			[]string{"-a", "coder", "-C", "/work", "--config", "/config.yaml"},
		)

		require.NoError(t, err)
		require.Equal(t, "coder", parsed.AgentID)
		require.Equal(t, "/work", parsed.Workdir)
		require.Equal(t, "/config.yaml", parsed.ConfigPath)
	})

	t.Run("parses the long flags", func(t *testing.T) {
		parsed, err := parseOptions([]string{"--agent", "coder", "--workdir", "/work"})

		require.NoError(t, err)
		require.Equal(t, "coder", parsed.AgentID)
		require.Equal(t, "/work", parsed.Workdir)
	})

	t.Run("trims the values", func(t *testing.T) {
		parsed, err := parseOptions([]string{"-a", "  coder  "})

		require.NoError(t, err)
		require.Equal(t, "coder", parsed.AgentID)
	})

	t.Run("reports help", func(t *testing.T) {
		_, err := parseOptions([]string{"-h"})

		require.ErrorIs(t, err, errHelp)
	})

	t.Run("rejects invalid arguments", func(t *testing.T) {
		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "unknown flag",
				args:    []string{"--bogus"},
				wantErr: "flag provided but not defined",
			},
			{
				name:    "positional argument",
				args:    []string{"coder"},
				wantErr: `unexpected argument "coder"`,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, err := parseOptions(test.args)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})

	t.Run("prints the usage", func(t *testing.T) {
		builder := &strings.Builder{}

		usage(builder)

		require.Contains(t, builder.String(), "Usage:")
	})
}
