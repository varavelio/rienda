package transport

import (
	"fmt"
	"net/http"

	"github.com/varavelio/rienda/internal/llm"
)

// Transport is an http.RoundTripper that prepares provider requests. It applies,
// in order:
//
//  1. static Headers (for example version or user-agent headers),
//  2. the harness session ID read from the request context into SessionHeaderName,
//  3. the Auth strategy.
//
// The request is cloned before mutation, so concurrent in-flight requests never
// share headers.
//
// A zero Transport with no Auth, Headers or SessionHeaderName simply delegates
// to http.DefaultTransport.
type Transport struct {
	// Auth authorizes each request; nil skips authorization.
	Auth AuthStrategy

	// Headers holds static headers applied to every request.
	Headers map[string]string

	// SessionHeaderName, when set, receives the harness session ID read from
	// the request context.
	SessionHeaderName string

	// Next is the wrapped transport; nil falls back to http.DefaultTransport.
	Next http.RoundTripper
}

// RoundTrip clones req, applies headers and auth, then delegates to Next.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())

	for name, value := range t.Headers {
		clone.Header.Set(name, value)
	}

	if t.SessionHeaderName != "" {
		if sessionID, ok := llm.SessionIDFromContext(clone.Context()); ok {
			clone.Header.Set(t.SessionHeaderName, sessionID)
		}
	}

	if t.Auth != nil {
		if err := t.Auth.Authorize(clone); err != nil {
			return nil, fmt.Errorf("transport: authorize request: %w", err)
		}
	}

	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(clone)
	if err != nil {
		return nil, fmt.Errorf("transport: round trip: %w", err)
	}
	return resp, nil
}
