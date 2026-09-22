package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRun verifies the argument handling of the run command.
func TestRun(t *testing.T) {
	t.Run("validates the arguments", func(t *testing.T) {
		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "missing agent and session",
				wantErr: "an agent or a session is required",
			},
			{
				name:    "blank agent",
				args:    []string{"-a", "  "},
				wantErr: "an agent or a session is required",
			},
			{
				name:    "model without an agent or a session",
				args:    []string{"-m", "fake/model"},
				wantErr: "an agent or a session is required",
			},
			{
				name:    "missing prompt",
				args:    []string{"-a", "coder"},
				wantErr: "a prompt is required",
			},
			{
				name:    "blank prompt",
				args:    []string{"-a", "coder", "-p", "  "},
				wantErr: "a prompt is required",
			},
			{
				name:    "positional argument",
				args:    []string{"-a", "coder", "-p", "hi", "extra"},
				wantErr: `unexpected argument "extra"`,
			},
			{
				name:    "unknown flag",
				args:    []string{"--bogus"},
				wantErr: "flag provided but not defined",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				stdout, stderr := &strings.Builder{}, &strings.Builder{}

				err := Run(test.args, stdout, stderr)

				require.ErrorContains(t, err, test.wantErr)
				require.Empty(t, stdout.String())
			})
		}
	})

	t.Run("prints the run usage for help", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := Run([]string{"-h"}, stdout, stderr)

		require.NoError(t, err)
		require.Contains(t, stderr.String(), "Usage:")
	})

	t.Run("reports preparation failures", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}
		configPath := filepath.Join(t.TempDir(), "missing.yaml")

		err := Run(
			[]string{"-a", "coder", "-p", "hi", "--config", configPath},
			stdout,
			stderr,
		)

		require.ErrorContains(t, err, "does not exist")
	})
}
