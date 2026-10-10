// Package provider implements llm.Client for the supported wire protocols:
// Anthropic Messages, OpenAI Chat Completions and OpenAI Responses.
//
// Protocol names a wire protocol, Config holds the connection settings, New
// builds the client for a protocol. The provider modules under
// internal/providermod declare the connections and the rosters; this package
// stays the single home for vendor
// knowledge: request and response translation, authentication conventions and
// error classification.
package provider
