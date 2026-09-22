//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestHelp verifies that every form of the help request prints the program
// overview, headed by the version, and succeeds.
func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			result := harness.Run(t, nil, args...)

			result.RequireSuccess(t)
			require.Regexp(t, `(?i)^rienda \S+\n`, result.Stdout)
			require.Contains(t, result.Stdout, "Usage:")
			require.Contains(t, result.Stdout, "rienda run -a <agent> -p <prompt>")
			require.Empty(t, result.Stderr)
		})
	}
}

// TestVersion verifies that every form of the version request prints the
// program name and its version and succeeds.
func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			result := harness.Run(t, nil, args...)

			result.RequireSuccess(t)
			require.Regexp(t, `(?i)^rienda \S+`, result.Stdout)
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
	require.Contains(t, result.Stderr, "rienda run -s <session> -p <prompt>")
	require.Contains(t, result.Stderr, "--prompt")
	require.Contains(t, result.Stderr, "--session")
}

// TestRunRequiresItsOptions verifies that the run command rejects the
// invocations that miss a required option before touching the configuration.
func TestRunRequiresItsOptions(t *testing.T) {
	t.Run("without arguments", func(t *testing.T) {
		result := harness.Run(t, nil, "run")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "an agent or a session is required")
	})

	t.Run("with an agent and a session together", func(t *testing.T) {
		result := harness.Run(t, nil, "run", "-a", "coder", "-s", "session-1", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "mutually exclusive")
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
