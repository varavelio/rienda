package provider

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
)

// statusOverloaded is the Anthropic HTTP status for a temporarily overloaded
// service.
const statusOverloaded = 529

// wireError matches the nested error object of both OpenAI
// ({"error": {...}}) and Anthropic ({"type": "error", "error": {...}})
// failure payloads.
type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// errorKinds maps provider error codes and types to canonical kinds. Both are
// looked up in the same table because OpenAI and Anthropic reuse names across
// the type and code fields.
var errorKinds = map[string]llm.ErrorKind{
	"context_length_exceeded":    llm.ErrorKindContextLength,
	"insufficient_quota":         llm.ErrorKindQuota,
	"billing_hard_limit_reached": llm.ErrorKindQuota,
	"model_not_found":            llm.ErrorKindNotFound,
	"invalid_api_key":            llm.ErrorKindAuthentication,
	"invalid_request_error":      llm.ErrorKindInvalidRequest,
	"authentication_error":       llm.ErrorKindAuthentication,
	"permission_error":           llm.ErrorKindPermission,
	"not_found_error":            llm.ErrorKindNotFound,
	"rate_limit_error":           llm.ErrorKindRateLimit,
	"rate_limit_exceeded":        llm.ErrorKindRateLimit,
	"overloaded_error":           llm.ErrorKindOverloaded,
	"api_error":                  llm.ErrorKindServer,
	"server_error":               llm.ErrorKindServer,
	"request_too_large":          llm.ErrorKindInvalidRequest,
}

// contextLengthHints are message fragments that identify an oversized context
// when the provider reports no dedicated code for it.
var contextLengthHints = []string{
	"context length",
	"context_length",
	"prompt is too long",
	"too many tokens",
	"maximum context",
}

// newError builds a normalized provider failure, classifying it from its wire
// type, code and message. Type falls back to the code when the provider omits
// it.
func newError(provider string, status int, errType, code, message string) *llm.Error {
	typ := errType
	if typ == "" {
		typ = code
	}
	return &llm.Error{
		Provider:   provider,
		StatusCode: status,
		Kind:       errorKind(status, errType, code, message),
		Type:       typ,
		Message:    message,
	}
}

// errorKind classifies a failure. Codes win over types, message hints cover
// providers that only describe oversized contexts in prose, and the HTTP status
// is the final fallback.
func errorKind(status int, errType, code, message string) llm.ErrorKind {
	if kind, ok := errorKinds[code]; ok {
		return kind
	}
	if isContextLengthMessage(message) {
		return llm.ErrorKindContextLength
	}
	if kind, ok := errorKinds[errType]; ok {
		return kind
	}
	return errorKindByStatus(status)
}

// isContextLengthMessage reports whether a message describes an oversized
// context.
func isContextLengthMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, hint := range contextLengthHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// errorKindByStatus classifies a failure from its HTTP status alone.
func errorKindByStatus(status int) llm.ErrorKind {
	switch {
	case status == http.StatusUnauthorized:
		return llm.ErrorKindAuthentication
	case status == http.StatusForbidden:
		return llm.ErrorKindPermission
	case status == http.StatusNotFound:
		return llm.ErrorKindNotFound
	case status == http.StatusTooManyRequests:
		return llm.ErrorKindRateLimit
	case status == statusOverloaded:
		return llm.ErrorKindOverloaded
	case status >= 400 && status < 500:
		return llm.ErrorKindInvalidRequest
	case status >= 500:
		return llm.ErrorKindServer
	default:
		return llm.ErrorKindUnknown
	}
}

// apiError builds an *llm.Error from a failed HTTP response body.
func apiError(provider string, status int, body []byte) error {
	var shape struct {
		Error wireError `json:"error"`
	}
	if err := json.Unmarshal(body, &shape); err == nil && shape.Error.Message != "" {
		return newError(provider, status, shape.Error.Type, shape.Error.Code, shape.Error.Message)
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return newError(provider, status, "", "", text)
	}
	return newError(provider, status, "", "", http.StatusText(status))
}
