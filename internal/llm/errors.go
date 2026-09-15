package llm

import "fmt"

// Error is a normalized provider failure.
type Error struct {
	// Provider identifies the backend that produced the failure.
	Provider string
	// StatusCode is the HTTP status of the failed call, or zero when the
	// failure happened before any response was received.
	StatusCode int
	// Type is the provider error code, when the provider supplied one.
	Type string
	// Message is the human readable failure description.
	Message string
}

// Error returns the failure description.
func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf(
			"%s: request failed with status %d: %s",
			e.Provider,
			e.StatusCode,
			e.Message,
		)
	}
	return fmt.Sprintf("%s: %s", e.Provider, e.Message)
}
