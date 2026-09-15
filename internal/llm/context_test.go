package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionIDFromContext(t *testing.T) {
	t.Run("returns the stored session id", func(t *testing.T) {
		ctx := WithSessionID(context.Background(), "session-123")

		sessionID, ok := SessionIDFromContext(ctx)

		require.True(t, ok)
		require.Equal(t, "session-123", sessionID)
	})

	t.Run("returns false when no session id is stored", func(t *testing.T) {
		sessionID, ok := SessionIDFromContext(context.Background())

		require.False(t, ok)
		require.Empty(t, sessionID)
	})

	t.Run("returns false for an empty session id", func(t *testing.T) {
		ctx := WithSessionID(context.Background(), "")

		sessionID, ok := SessionIDFromContext(ctx)

		require.False(t, ok)
		require.Empty(t, sessionID)
	})

	t.Run("child contexts inherit the session id", func(t *testing.T) {
		ctx, cancel := context.WithCancel(WithSessionID(context.Background(), "session-123"))
		defer cancel()

		sessionID, ok := SessionIDFromContext(ctx)

		require.True(t, ok)
		require.Equal(t, "session-123", sessionID)
	})
}
