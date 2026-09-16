// Package provider implements llm.Client for the supported wire protocols:
// Anthropic Messages, OpenAI Chat Completions and OpenAI Responses.
//
// Protocol names a wire protocol, Config holds the connection settings, New
// builds the client for a protocol and Preset supplies the protocol and the
// endpoint of well-known services. The package is the single home for vendor
// knowledge: request and response translation, authentication conventions and
// error classification.
package provider
