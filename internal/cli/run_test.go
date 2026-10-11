package cli

import (
	"os"
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

	t.Run("accepts auto approve with its alias", func(t *testing.T) {
		for _, flag := range []string{"--auto-approve", "-y"} {
			stdout, stderr := &strings.Builder{}, &strings.Builder{}
			configPath := filepath.Join(t.TempDir(), "missing.yaml")

			err := Run(
				[]string{"-a", "coder", "-p", "hi", "--config", configPath, flag},
				stdout,
				stderr,
			)

			require.ErrorContains(t, err, "is not defined", flag)
		}
	})

	t.Run("documents auto approve in the usage", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		require.NoError(t, Run([]string{"-h"}, stdout, stderr))
		require.Contains(t, stderr.String(), "--auto-approve")
	})

	t.Run("reports preparation failures", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}
		configPath := filepath.Join(t.TempDir(), "missing.yaml")

		err := Run(
			[]string{"-a", "coder", "-p", "hi", "--config", configPath},
			stdout,
			stderr,
		)

		require.ErrorContains(t, err, "is not defined")
	})
}

// TestResolveSlash verifies slash expansion of non-interactive prompts.
func TestResolveSlash(t *testing.T) {
	t.Run("expands a known command", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeSlash(t, home, "test", "Run tests", "Run tests: $*")
		got, err := resolveSlash("/test --coverage", &strings.Builder{})
		require.NoError(t, err)
		require.Equal(t, "Run tests: --coverage", got)
	})
	t.Run("fails unknown without a run", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		_, err := resolveSlash("/nope x", &strings.Builder{})
		require.ErrorContains(t, err, "unknown command /nope")
	})
	t.Run("rejects a lone slash", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		_, err := resolveSlash("/", &strings.Builder{})
		require.ErrorContains(t, err, "unknown command")
	})
	t.Run("warns on broken files and still expands", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeSlash(t, home, "good", "Good", "Good $*")
		writeBrokenSlash(t, home, "bad")
		var stderr strings.Builder
		got, err := resolveSlash("/good x", &stderr)
		require.NoError(t, err)
		require.Equal(t, "Good x", got)
		require.Contains(t, stderr.String(), "bad.md")
	})
}

func writeSlash(t *testing.T, home, id, description, template string) {
	t.Helper()
	dir := filepath.Join(home, ".rienda", "commands")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	contents := "---\ndescription: " + description + "\n---\n" + template + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(contents), 0o600))
}

func writeBrokenSlash(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, ".rienda", "commands")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte("broken\n"), 0o600))
}
