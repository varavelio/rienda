package tokens

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// TestOfMessage verifies the token estimate of one message.
func TestOfMessage(t *testing.T) {
	tests := []struct {
		name    string
		message llm.Message
		want    int
	}{
		{name: "an empty message", message: llm.Message{}, want: 0},
		{
			name: "a text block",
			message: llm.Message{
				Role:   llm.RoleUser,
				Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello world"}},
			},
			want: 3,
		},
		{
			name: "a thinking block",
			message: llm.Message{
				Role:   llm.RoleAssistant,
				Blocks: []llm.Block{{Type: llm.BlockThinking, Thinking: "reasoning here"}},
			},
			want: 4,
		},
		{
			name: "a redacted thinking block",
			message: llm.Message{
				Role: llm.RoleAssistant,
				Blocks: []llm.Block{
					{Type: llm.BlockRedactedThinking, ThinkingRedactedData: "opaque!!"},
				},
			},
			want: 2,
		},
		{
			name: "a tool call block",
			message: llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{
				Type:              llm.BlockToolCall,
				ToolCallName:      "shell",
				ToolCallArguments: json.RawMessage(`{"command":"ls"}`),
			}}},
			want: 6,
		},
		{
			name: "a tool result block",
			message: llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{
				Type:       llm.BlockToolResult,
				ToolResult: []llm.Block{{Type: llm.BlockText, Text: "output"}},
			}}},
			want: 2,
		},
		{
			name: "a message mixing its blocks",
			message: llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
				{Type: llm.BlockThinking, Thinking: "hmm"},
				{Type: llm.BlockText, Text: "done"},
			}},
			want: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, OfMessage(test.message))
		})
	}
}

// TestOfRequest verifies the token estimate of a whole request.
func TestOfRequest(t *testing.T) {
	t.Run("counts nothing without a request", func(t *testing.T) {
		require.Equal(t, 0, OfRequest(nil))
	})

	t.Run("counts nothing without content", func(t *testing.T) {
		require.Equal(t, 0, OfRequest(&llm.Request{}))
	})

	t.Run("counts the system prompt", func(t *testing.T) {
		require.Equal(t, 2, OfRequest(&llm.Request{System: "be brief"}))
	})

	t.Run("counts the tool definitions", func(t *testing.T) {
		request := &llm.Request{Tools: []llm.Tool{{
			Name:        "shell",
			Description: "run a command",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}}}

		require.Equal(t, 9, OfRequest(request))
	})

	t.Run("counts every part the model reads", func(t *testing.T) {
		request := &llm.Request{
			System: "be brief",
			Messages: []llm.Message{
				{
					Role:   llm.RoleUser,
					Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello world"}},
				},
			},
			Tools: []llm.Tool{{
				Name:        "shell",
				Description: "run a command",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			}},
		}

		// 8 bytes of system, 11 of message and 35 of tool definition.
		require.Equal(t, 14, OfRequest(request))
	})
}

// TestMeasure verifies the context report of a measurement.
func TestMeasure(t *testing.T) {
	tests := []struct {
		name        string
		used        int
		window      int
		wantPercent int
	}{
		{name: "a normal measurement", used: 100, window: 200, wantPercent: 50},
		{name: "no use at all", used: 0, window: 200, wantPercent: 0},
		{name: "the whole window", used: 200, window: 200, wantPercent: 100},
		{name: "more than the window", used: 300, window: 200, wantPercent: 100},
		{name: "a window of zero", used: 50, window: 0, wantPercent: 0},
		{name: "a negative window", used: 50, window: -10, wantPercent: 0},
		{name: "no use and no window", used: 0, window: 0, wantPercent: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := Measure(test.used, test.window)

			require.Equal(t, test.used, report.Used)
			require.Equal(t, test.window, report.Window)
			require.Equal(t, test.wantPercent, report.Percent)
		})
	}
}
