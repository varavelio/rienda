package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunCarrier(t *testing.T) {
	t.Run("round trips through a context", func(t *testing.T) {
		run := Run{SessionID: "s1", ModelID: "m1", Agent: Agent{ID: "coder"}}
		got, ok := RunFromContext(WithRun(t.Context(), run))
		require.True(t, ok)
		require.Equal(t, run, got)
	})
	t.Run("reports absence", func(t *testing.T) {
		_, ok := RunFromContext(t.Context())
		require.False(t, ok)
	})
}
