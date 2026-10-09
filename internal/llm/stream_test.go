package llm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIncompleteStream verifies the marker that tells a caller a stream broke
// before the response completed.
func TestIncompleteStream(t *testing.T) {
	t.Run("keeps the message and stays reachable", func(t *testing.T) {
		cause := errors.New("unexpected EOF")
		marked := IncompleteStream(fmt.Errorf("anthropic: read: %w", cause))

		require.EqualError(t, marked, "anthropic: read: unexpected EOF")
		require.ErrorIs(t, marked, ErrIncompleteStream)
		require.ErrorIs(t, marked, cause)
	})

	t.Run("marks nothing when there is no failure", func(t *testing.T) {
		require.NoError(t, IncompleteStream(nil))
	})
}
