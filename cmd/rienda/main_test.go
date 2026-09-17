package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRun verifies command dispatch.
func TestRun(t *testing.T) {
	t.Run("prints usage without arguments", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := run(nil, stdout, stderr)

		require.NoError(t, err)
		require.Contains(t, stdout.String(), "Usage:")
		require.Empty(t, stderr.String())
	})

	t.Run("prints usage for help", func(t *testing.T) {
		for _, arg := range []string{"help", "-h", "--help"} {
			stdout, stderr := &strings.Builder{}, &strings.Builder{}

			err := run([]string{arg}, stdout, stderr)

			require.NoError(t, err)
			require.Contains(t, stdout.String(), "Usage:", "argument %q", arg)
		}
	})

	t.Run("rejects unknown commands", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := run([]string{"bogus"}, stdout, stderr)

		require.ErrorContains(t, err, `unknown command "bogus"`)
		require.Empty(t, stdout.String())
		require.Contains(t, stderr.String(), "Usage:")
	})
}
