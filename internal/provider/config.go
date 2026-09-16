package provider

import (
	"encoding/json"
	"maps"
	"net/http"
	"strings"

	"github.com/varavelio/rienda/internal/transport"
)

// Config holds the connection settings shared by every provider client.
type Config struct {
	// APIKey is the credential sent to the provider.
	APIKey string
	// BaseURL is the provider endpoint root, for example a preset endpoint or a
	// compatible proxy. It has no default; callers must supply it.
	BaseURL string
	// ExtraHeaders adds static headers to every request.
	ExtraHeaders map[string]string
	// SessionHeaderName, when set, forwards the harness session ID carried by
	// the request context into the named header.
	SessionHeaderName string
	// HTTPClient optionally supplies the underlying transport (for example a
	// proxy). Only its Transport is reused; auth and headers come from Config.
	HTTPClient *http.Client
}

// Wire literals shared by more than one provider client.
const (
	// wireRoleAssistant is the assistant role across provider payloads.
	wireRoleAssistant = "assistant"
	// wireRoleUser is the user role across provider payloads.
	wireRoleUser = "user"
	// wireJSONNull is the JSON null literal compared against raw payloads.
	wireJSONNull = "null"
	// wireToolFunction is the function tool type of the OpenAI APIs.
	wireToolFunction = "function"
	// wireFunctionCall is the function call item and stop reason of the OpenAI APIs.
	wireFunctionCall = "function_call"
	// wireToolChoiceAuto is the automatic tool choice shared by the APIs.
	wireToolChoiceAuto = "auto"
	// wireToolChoiceNone is the tool choice that disables tool use.
	wireToolChoiceNone = "none"
)

// emptyJSONObject replaces an absent tool schema or tool arguments payload, as
// the provider APIs require JSON objects.
var emptyJSONObject = json.RawMessage("{}")

// normalizeBaseURL drops any trailing slash so endpoint paths can be
// appended safely. It applies no default; callers must supply a base URL.
func normalizeBaseURL(base string) string {
	return strings.TrimRight(base, "/")
}

// httpClient builds the provider HTTP client wiring auth, static headers and
// session forwarding through transport.Transport.
func (c Config) httpClient(auth transport.AuthStrategy, headers map[string]string) *http.Client {
	mergedHeaders := make(map[string]string, len(headers)+len(c.ExtraHeaders))
	maps.Copy(mergedHeaders, headers)
	maps.Copy(mergedHeaders, c.ExtraHeaders)

	next := http.DefaultTransport
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		next = c.HTTPClient.Transport
	}

	return &http.Client{
		Transport: &transport.Transport{
			Auth:              auth,
			Headers:           mergedHeaders,
			SessionHeaderName: c.SessionHeaderName,
			Next:              next,
		},
	}
}
