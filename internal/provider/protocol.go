package provider

import (
	"fmt"
	"strings"
)

// Protocol identifies the wire protocol a provider client speaks.
type Protocol string

const (
	// ProtocolAnthropic is the Anthropic Messages API.
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolOpenAIChatCompletions is the OpenAI Chat Completions API and its compatible
	// implementations.
	ProtocolOpenAIChatCompletions Protocol = "openai_chat_completions"
	// ProtocolOpenAIResponses is the OpenAI Responses API.
	ProtocolOpenAIResponses Protocol = "openai_responses"
)

// Protocols returns the supported protocols in a stable order.
func Protocols() []Protocol {
	return []Protocol{
		ProtocolAnthropic,
		ProtocolOpenAIChatCompletions,
		ProtocolOpenAIResponses,
	}
}

// ParseProtocol converts a protocol name into a Protocol. It returns an error
// listing the supported protocols when the name is unknown.
func ParseProtocol(name string) (Protocol, error) {
	for _, protocol := range Protocols() {
		if string(protocol) == name {
			return protocol, nil
		}
	}
	names := make([]string, 0, len(Protocols()))
	for _, protocol := range Protocols() {
		names = append(names, string(protocol))
	}
	return "", fmt.Errorf(
		"provider: unknown protocol %q (supported protocols: %s)",
		name,
		strings.Join(names, ", "),
	)
}
