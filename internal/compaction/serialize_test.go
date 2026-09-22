package compaction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// TestSerialize verifies the plain text rendering of a conversation.
func TestSerialize(t *testing.T) {
	t.Run("renders every role with its own label", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}},
			{Role: llm.RoleAssistant, Blocks: []llm.Block{
				{Type: llm.BlockThinking, Thinking: "reasoning"},
				{Type: llm.BlockText, Text: "answer"},
				{
					Type:              llm.BlockToolCall,
					ToolCallName:      "shell",
					ToolCallArguments: []byte(`{"command":"ls"}`),
				},
			}},
			{Role: llm.RoleUser, Blocks: []llm.Block{{
				Type:       llm.BlockToolResult,
				ToolResult: []llm.Block{{Type: llm.BlockText, Text: "listing"}},
			}}},
		}

		serialized := Serialize(messages)

		require.Contains(t, serialized, "[User]: hello")
		require.Contains(t, serialized, "[Assistant thinking]: reasoning")
		require.Contains(t, serialized, "[Assistant]: answer")
		require.Contains(t, serialized, `[Assistant tool calls]: shell {"command":"ls"}`)
		require.Contains(t, serialized, "[Tool result]: listing")
	})

	t.Run("renders an empty list as an empty string", func(t *testing.T) {
		require.Empty(t, Serialize(nil))
	})

	t.Run("skips empty content", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "  "}}},
		}

		require.Empty(t, Serialize(messages))
	})

	t.Run("bounds a large tool result regardless of its size", func(t *testing.T) {
		huge := strings.Repeat("x", 100000)
		messages := []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{
				Type:       llm.BlockToolResult,
				ToolResult: []llm.Block{{Type: llm.BlockText, Text: huge}},
			}}},
		}

		serialized := Serialize(messages)

		require.Less(t, len(serialized), 3000)
		require.Contains(t, serialized, "[truncated:")
		require.Contains(t, serialized, "characters dropped]")
	})

	t.Run("leaves a tool result within the limit whole", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{
				Type:       llm.BlockToolResult,
				ToolResult: []llm.Block{{Type: llm.BlockText, Text: "short"}},
			}}},
		}

		serialized := Serialize(messages)

		require.Contains(t, serialized, "[Tool result]: short")
		require.NotContains(t, serialized, "[truncated:")
	})
}
