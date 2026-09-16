package provider

import (
	"fmt"

	"github.com/varavelio/rienda/internal/llm"
)

// New returns an llm.Client speaking the given protocol. Config must carry the
// credentials and the endpoint root of the target service.
func New(protocol Protocol, cfg Config) (llm.Client, error) {
	switch protocol {
	case ProtocolAnthropic:
		return NewAnthropic(cfg), nil
	case ProtocolOpenAIChatCompletions:
		return NewOpenAIChatCompletions(cfg), nil
	case ProtocolOpenAIResponses:
		return NewOpenAIResponses(cfg), nil
	default:
		return nil, fmt.Errorf("provider: unknown protocol %q", protocol)
	}
}
