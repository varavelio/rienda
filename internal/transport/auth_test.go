package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBearerAuth(t *testing.T) {
	t.Run("sets the authorization header", func(t *testing.T) {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"https://example.com",
			nil,
		)
		auth := BearerAuth{Token: "secret"}

		require.NoError(t, auth.Authorize(req))
		require.Equal(t, "Bearer secret", req.Header.Get("Authorization"))
	})
}

func TestHeaderAuth(t *testing.T) {
	t.Run("sets the configured header", func(t *testing.T) {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"https://example.com",
			nil,
		)
		auth := HeaderAuth{Header: "x-api-key", Value: "secret"}

		require.NoError(t, auth.Authorize(req))
		require.Equal(t, "secret", req.Header.Get("X-Api-Key"))
	})
}
