package session

import "context"

// IDGenerator creates the identifiers used by a session store. The id package
// provides the shared implementation, and tests inject their own.
type IDGenerator interface {
	// NewID returns a new unique identifier.
	NewID(ctx context.Context) string
}
