package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestError(t *testing.T) {
	t.Run("includes the status code when present", func(t *testing.T) {
		err := &Error{Provider: "acme", StatusCode: 429, Type: "rate_limit", Message: "slow down"}

		require.Equal(t, "acme: request failed with status 429: slow down", err.Error())
	})

	t.Run("omits the status code when zero", func(t *testing.T) {
		err := &Error{Provider: "acme", Message: "connection refused"}

		require.Equal(t, "acme: connection refused", err.Error())
	})
}
