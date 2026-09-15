package llm

import "context"

// sessionIDKey is the context key carrying the harness session ID.
type sessionIDKey struct{}

// WithSessionID attaches the harness session ID to ctx so transports can
// forward it to providers that require per-session headers.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionIDFromContext returns the session ID stored in ctx, if any.
func SessionIDFromContext(ctx context.Context) (string, bool) {
	sessionID, ok := ctx.Value(sessionIDKey{}).(string)
	if !ok || sessionID == "" {
		return "", false
	}
	return sessionID, true
}
