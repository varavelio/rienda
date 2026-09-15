package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestError(t *testing.T) {
	t.Run("includes kind and status when both are present", func(t *testing.T) {
		err := &Error{
			Provider:   "acme",
			StatusCode: 429,
			Kind:       ErrorKindRateLimit,
			Type:       "rate_limit_error",
			Message:    "slow down",
		}

		require.Equal(t, "acme: rate_limit: status 429: slow down", err.Error())
	})

	t.Run("includes the kind when there is no status", func(t *testing.T) {
		err := &Error{Provider: "acme", Kind: ErrorKindAuthentication, Message: "bad key"}

		require.Equal(t, "acme: authentication: bad key", err.Error())
	})

	t.Run("omits the kind when it is unknown", func(t *testing.T) {
		err := &Error{Provider: "acme", StatusCode: 500, Message: "boom"}

		require.Equal(t, "acme: status 500: boom", err.Error())
	})

	t.Run("omits both kind and status when absent", func(t *testing.T) {
		err := &Error{Provider: "acme", Message: "connection refused"}

		require.Equal(t, "acme: connection refused", err.Error())
	})
}
