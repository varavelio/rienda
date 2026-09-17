package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRun verifies command dispatch.
func TestRun(t *testing.T) {
	t.Run("prints usage for help", func(t *testing.T) {
		for _, arg := range []string{"help", "-h", "--help"} {
			stdout, stderr := &strings.Builder{}, &strings.Builder{}

			err := run([]string{arg}, strings.NewReader(""), stdout, stderr)

			require.NoError(t, err)
			require.Contains(t, stdout.String(), "Usage:", "argument %q", arg)
		}
	})

	t.Run("rejects unknown commands", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := run([]string{"bogus"}, strings.NewReader(""), stdout, stderr)

		require.ErrorContains(t, err, `unknown command "bogus"`)
		require.Empty(t, stdout.String())
		require.Contains(t, stderr.String(), "Usage:")
	})

	t.Run("delegates to the interactive interface", func(t *testing.T) {
		// An empty home has no agent definitions, so the interface fails
		// before it takes over the terminal.
		t.Setenv("HOME", t.TempDir())
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		for _, args := range [][]string{nil, {"-a", "coder"}} {
			err := run(args, strings.NewReader(""), stdout, stderr)

			require.ErrorContains(t, err, "no agent definitions found", "args %v", args)
		}
	})
}
