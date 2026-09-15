package llm

import "strconv"

// ErrorKind classifies a provider failure in provider-neutral terms so callers
// can react to it without knowing vendor-specific error codes.
type ErrorKind string

const (
	// ErrorKindUnknown marks a failure that could not be classified. It is
	// the zero value.
	ErrorKindUnknown ErrorKind = ""
	// ErrorKindInvalidRequest marks a malformed or invalid request.
	ErrorKindInvalidRequest ErrorKind = "invalid_request"
	// ErrorKindAuthentication marks missing or invalid credentials.
	ErrorKindAuthentication ErrorKind = "authentication"
	// ErrorKindPermission marks valid credentials without access to the
	// requested resource.
	ErrorKindPermission ErrorKind = "permission"
	// ErrorKindNotFound marks a missing resource, such as an unknown model.
	ErrorKindNotFound ErrorKind = "not_found"
	// ErrorKindQuota marks exhausted billing quota.
	ErrorKindQuota ErrorKind = "quota"
	// ErrorKindRateLimit marks exceeded request or token rate limits.
	ErrorKindRateLimit ErrorKind = "rate_limit"
	// ErrorKindOverloaded marks a provider temporarily unable to serve traffic.
	ErrorKindOverloaded ErrorKind = "overloaded"
	// ErrorKindContextLength marks input exceeding the model context window.
	ErrorKindContextLength ErrorKind = "context_length"
	// ErrorKindServer marks an internal provider failure.
	ErrorKindServer ErrorKind = "server"
)

// Error is a normalized provider failure.
type Error struct {
	// Provider identifies the backend that produced the failure.
	Provider string

	// StatusCode is the HTTP status of the failed call, or zero when the
	// failure happened before any response was received.
	StatusCode int

	// Kind classifies the failure in provider-neutral terms. It is
	// ErrorKindUnknown when the failure could not be classified.
	Kind ErrorKind

	// Type is the raw provider error code, kept for diagnostics.
	Type string

	// Message is the human readable failure description.
	Message string
}

// Error returns the failure description as
// "provider: kind: status N: message", omitting the kind when it is unknown
// and the status when it is zero.
func (e *Error) Error() string {
	kind := ""
	if e.Kind != ErrorKindUnknown {
		kind = string(e.Kind) + ": "
	}

	status := ""
	if e.StatusCode > 0 {
		status = "status " + strconv.Itoa(e.StatusCode) + ": "
	}

	return e.Provider + ": " + kind + status + e.Message
}
