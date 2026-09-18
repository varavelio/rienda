//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestHelp verifies that every form of the help request prints the program
// overview and succeeds.
func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			result := harness.Run(t, nil, args...)

			result.RequireSuccess(t)
			require.Contains(t, result.Stdout, "Usage:")
			require.Contains(t, result.Stdout, "rienda run -a <agent> -p <prompt>")
			require.Empty(t, result.Stderr)
		})
	}
}

// TestUnknownCommand verifies that an unknown command reports the overview and
// fails.
func TestUnknownCommand(t *testing.T) {
	result := harness.Run(t, nil, "frobnicate")

	require.Equal(t, 1, result.Code)
	require.Empty(t, result.Stdout)
	require.Contains(t, result.Stderr, `unknown command "frobnicate"`)
	require.Contains(t, result.Stderr, "Usage:")
}

// TestRunUsage verifies that asking for the usage of the run command succeeds
// and documents its flags.
func TestRunUsage(t *testing.T) {
	result := harness.Run(t, nil, "run", "-h")

	require.Equal(t, 0, result.Code)
	require.Contains(t, result.Stderr, "rienda run -a <agent> -p <prompt>")
	require.Contains(t, result.Stderr, "--prompt")
}

// TestRunRequiresItsOptions verifies that the run command rejects the
// invocations that miss a required option before touching the configuration.
func TestRunRequiresItsOptions(t *testing.T) {
	t.Run("without arguments", func(t *testing.T) {
		result := harness.Run(t, nil, "run")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "an agent is required")
	})

	t.Run("without a prompt", func(t *testing.T) {
		result := harness.Run(t, nil, "run", "-a", "coder")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "a prompt is required")
	})

	t.Run("with an unexpected argument", func(t *testing.T) {
		result := harness.Run(t, nil, "run", "-a", "coder", "-p", "hi", "extra")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `unexpected argument "extra"`)
	})
}
