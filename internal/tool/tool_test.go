package tool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWithWorkdir verifies working directory propagation through contexts.
func TestWithWorkdir(t *testing.T) {
	t.Run("returns the attached directory", func(t *testing.T) {
		ctx := WithWorkdir(t.Context(), "/workspace")

		dir, ok := WorkdirFromContext(ctx)

		require.True(t, ok)
		require.Equal(t, "/workspace", dir)
	})

	t.Run("reports no directory when absent", func(t *testing.T) {
		_, ok := WorkdirFromContext(t.Context())

		require.False(t, ok)
	})

	t.Run("reports no directory when empty", func(t *testing.T) {
		_, ok := WorkdirFromContext(WithWorkdir(t.Context(), ""))

		require.False(t, ok)
	})
}

// TestTextResult verifies the successful result constructor.
func TestTextResult(t *testing.T) {
	result := TextResult("done")

	require.False(t, result.IsError)
	require.Len(t, result.Blocks, 1)
	require.Equal(t, "done", result.Blocks[0].Text)
}

// TestErrorResult verifies the failed result constructor.
func TestErrorResult(t *testing.T) {
	result := ErrorResult("boom")

	require.True(t, result.IsError)
	require.Len(t, result.Blocks, 1)
	require.Equal(t, "boom", result.Blocks[0].Text)
}
