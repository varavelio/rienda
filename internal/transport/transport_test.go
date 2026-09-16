package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/varavelio/rienda/internal/llm"

	"github.com/stretchr/testify/require"
)

// errAuth is an AuthStrategy that always fails.
type errAuth struct{ err error }

func (a errAuth) Authorize(_ *http.Request) error { return a.err }

// roundTripFunc adapts a function into an http.RoundTripper for tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTransport(t *testing.T) {
	newServer := func(t *testing.T, seen map[string]string) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen["Authorization"] = r.Header.Get("Authorization")
			seen["X-Custom-Session"] = r.Header.Get("X-Custom-Session")
			seen["X-Static"] = r.Header.Get("X-Static")
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	t.Run("applies auth, static headers and session header", func(t *testing.T) {
		seen := map[string]string{}
		server := newServer(t, seen)
		defer server.Close()

		transport := &Transport{
			Auth:              BearerAuth{Token: "secret"},
			Headers:           map[string]string{"X-Static": "static"},
			SessionHeaderName: "X-Custom-Session",
		}
		req, err := http.NewRequestWithContext(
			llm.WithSessionID(t.Context(), "session-1"),
			http.MethodGet,
			server.URL,
			nil,
		)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "Bearer secret", seen["Authorization"])
		require.Equal(t, "session-1", seen["X-Custom-Session"])
		require.Equal(t, "static", seen["X-Static"])
	})

	t.Run("skips the session header when the context carries none", func(t *testing.T) {
		seen := map[string]string{}
		server := newServer(t, seen)
		defer server.Close()

		transport := &Transport{
			Auth:              BearerAuth{Token: "secret"},
			SessionHeaderName: "X-Custom-Session",
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Empty(t, seen["X-Custom-Session"])
	})

	t.Run("does not mutate the original request", func(t *testing.T) {
		seen := map[string]string{}
		server := newServer(t, seen)
		defer server.Close()

		transport := &Transport{Auth: BearerAuth{Token: "secret"}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Empty(t, req.Header.Get("Authorization"))
	})

	t.Run("zero value delegates to the default transport", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
		defer server.Close()

		transport := &Transport{}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)

		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusTeapot, resp.StatusCode)
	})

	t.Run("lets the auth strategy override a static header", func(t *testing.T) {
		seen := map[string]string{}
		server := newServer(t, seen)
		defer server.Close()

		transport := &Transport{
			Auth:    BearerAuth{Token: "real"},
			Headers: map[string]string{"Authorization": "static"},
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "Bearer real", seen["Authorization"])
	})

	t.Run("wraps delegated transport failures", func(t *testing.T) {
		transport := &Transport{
			Next: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("boom")
			}),
		}
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"https://example.com",
			nil,
		)

		_, err := transport.RoundTrip(req) //nolint:bodyclose // resp is always nil when Next fails.

		require.Error(t, err)
		require.ErrorContains(t, err, "round trip")
		require.ErrorContains(t, err, "boom")
	})

	t.Run("wraps auth failures", func(t *testing.T) {
		transport := &Transport{Auth: errAuth{err: errors.New("no credentials")}}
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"https://example.com",
			nil,
		)

		_, err := transport.RoundTrip(req) //nolint:bodyclose // resp is always nil on auth failure.

		require.Error(t, err)
		require.ErrorContains(t, err, "authorize request")
	})
}
