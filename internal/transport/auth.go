package transport

import "net/http"

// AuthStrategy authorizes an outgoing HTTP request.
type AuthStrategy interface {
	// Authorize mutates req in place adding the credentials the provider expects.
	Authorize(req *http.Request) error
}

// BearerAuth authorizes requests with an Authorization: Bearer header.
// It covers OpenAI and most OpenAI-compatible providers.
type BearerAuth struct {
	// Token is the credential sent as the bearer token.
	Token string
}

// Authorize sets the Authorization header on req.
func (a BearerAuth) Authorize(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+a.Token)
	return nil
}

// HeaderAuth authorizes requests with a custom header, for example x-api-key
// or X-Auth-Token.
type HeaderAuth struct {
	// Header is the name of the header carrying the credential.
	Header string

	// Value is the credential value.
	Value string
}

// Authorize sets the configured header on req.
func (a HeaderAuth) Authorize(req *http.Request) error {
	req.Header.Set(a.Header, a.Value)
	return nil
}
