package provider

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/varavelio/rienda/internal/transport"
	"github.com/varavelio/rienda/internal/version"

	"github.com/stretchr/testify/require"
)

// captureTransport records the request headers and replies with an empty 200.
type captureTransport struct {
	headers http.Header
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.headers = req.Header.Clone()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

// failingBody fails every read and closes cleanly.
type failingBody struct{ err error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }
func (failingBody) Close() error               { return nil }

// failingTransport replies with a response whose body always fails on read.
// A zero status defaults to 200.
type failingTransport struct {
	readErr    error
	statusCode int
}

func (t failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	status := t.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       failingBody{err: t.readErr},
	}, nil
}

// errorTransport always fails the round trip.
type errorTransport struct{ err error }

func (t errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func TestConfigHTTPClient(t *testing.T) {
	t.Run("reuses the injected transport and lets extra headers win", func(t *testing.T) {
		capture := &captureTransport{}
		cfg := Config{
			APIKey:       "key",
			BaseURL:      "https://example.com",
			ExtraHeaders: map[string]string{"anthropic-version": "custom", "X-Extra": "1"},
			HTTPClient:   &http.Client{Transport: capture},
		}
		client := cfg.httpClient(
			transport.HeaderAuth{Header: "x-api-key", Value: cfg.APIKey},
			map[string]string{"anthropic-version": anthropicVersion},
		)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cfg.BaseURL, nil)
		require.NoError(t, err)

		resp, err := client.Do(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "custom", capture.headers.Get("Anthropic-Version"))
		require.Equal(t, "1", capture.headers.Get("X-Extra"))
		require.Equal(t, "key", capture.headers.Get("X-Api-Key"))
	})

	t.Run("identifies the client with the rienda user agent", func(t *testing.T) {
		capture := &captureTransport{}
		cfg := Config{
			BaseURL:    "https://example.com",
			HTTPClient: &http.Client{Transport: capture},
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cfg.BaseURL, nil)
		require.NoError(t, err)

		resp, err := cfg.httpClient(nil, nil).Do(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, version.UserAgent(), capture.headers.Get("User-Agent"))
	})

	t.Run("lets extra headers override the user agent", func(t *testing.T) {
		capture := &captureTransport{}
		cfg := Config{
			BaseURL:      "https://example.com",
			ExtraHeaders: map[string]string{"User-Agent": "custom/1.0"},
			HTTPClient:   &http.Client{Transport: capture},
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cfg.BaseURL, nil)
		require.NoError(t, err)

		resp, err := cfg.httpClient(nil, nil).Do(req)

		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "custom/1.0", capture.headers.Get("User-Agent"))
	})

	t.Run("falls back to the default transport", func(t *testing.T) {
		client := Config{}.httpClient(nil, nil)

		wrapper, ok := client.Transport.(*transport.Transport)
		require.True(t, ok)
		require.Same(t, http.DefaultTransport, wrapper.Next)
	})
}
