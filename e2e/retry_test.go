//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestRunRetriesTransientFailures verifies that a transient provider failure is
// retried with backoff, that the failure is reported, and that the answer of
// the recovered attempt reaches standard output exactly once.
func TestRunRetriesTransientFailures(t *testing.T) {
	app := newApp(t,
		harness.Turn{Status: http.StatusServiceUnavailable},
		harness.Text("recovered"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.Equal(t, "recovered\n", result.Stdout)
	require.Contains(t, result.Stderr, "retrying")

	requests := app.Provider().Requests()
	require.Len(t, requests, 2)
	require.Equal(t, requests[0].Path, requests[1].Path)
}

// TestRunGivesUpAfterRepeatedFailures verifies that a provider that never
// recovers fails the run instead of retrying forever.
func TestRunGivesUpAfterRepeatedFailures(t *testing.T) {
	turns := make([]harness.Turn, 8)
	for index := range turns {
		turns[index] = harness.Turn{Status: http.StatusServiceUnavailable}
	}
	app := newApp(t, turns...)

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	require.Equal(t, 1, result.Code)
	require.Contains(t, result.Stderr, "Service Unavailable")

	require.Len(t, app.Provider().Requests(), 5)
}
