// Package transport provides the HTTP plumbing shared by all provider clients:
// authentication strategies, header injection, session forwarding and
// server-sent event parsing.
//
// Transport is an http.RoundTripper that applies static headers, the harness
// session ID carried by the request context (see llm.WithSessionID) and the
// configured authentication strategy, in that order, before delegating to the
// underlying transport.
//
// Authorizing last lets the credentials take precedence over any static header
// with the same name.
package transport
