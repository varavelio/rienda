package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtocols(t *testing.T) {
	t.Run("returns the supported protocols in a stable order", func(t *testing.T) {
		require.Equal(t, []Protocol{
			ProtocolAnthropic,
			ProtocolOpenAIChat,
			ProtocolOpenAIResponses,
		}, Protocols())
	})
}

func TestParseProtocol(t *testing.T) {
	t.Run("parses every supported protocol", func(t *testing.T) {
		for _, protocol := range Protocols() {
			parsed, err := ParseProtocol(string(protocol))

			require.NoError(t, err, "protocol %q", protocol)
			require.Equal(t, protocol, parsed)
		}
	})

	t.Run("rejects unknown names listing the valid ones", func(t *testing.T) {
		_, err := ParseProtocol("gemini")

		require.Error(t, err)
		require.ErrorContains(t, err, `unknown protocol "gemini"`)
		require.ErrorContains(t, err, "anthropic, openai_chat, openai_responses")
	})

	t.Run("rejects an empty name", func(t *testing.T) {
		_, err := ParseProtocol("")

		require.Error(t, err)
	})
}
