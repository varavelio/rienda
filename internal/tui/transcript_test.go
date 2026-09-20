package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
)

// TestTranscript verifies event folding.
func TestTranscript(t *testing.T) {
	t.Run("folds a run with a tool call", func(t *testing.T) {
		conversation := transcript{}
		conversation.addUser("list the files")
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Let me "})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "check."})
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
			Arguments:  json.RawMessage(`{"command":"ls"}`),
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolOutput,
			ToolCallID: "call_1",
			Output:     "a.txt\n",
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "a.txt",
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Done."})

		require.Len(t, conversation.entries, 4)
		require.Equal(t, entryUser, conversation.entries[0].kind)
		require.Equal(t, "list the files", conversation.entries[0].text())
		require.Equal(t, entryAssistant, conversation.entries[1].kind)
		require.Equal(t, "Let me check.", conversation.entries[1].text())

		invocation := conversation.entries[2]
		require.Equal(t, entryTool, invocation.kind)
		require.Equal(t, "shell", invocation.toolName)
		require.Equal(t, `{"command":"ls"}`, invocation.toolArguments)
		require.Equal(t, "a.txt\n", invocation.text())
		require.True(t, invocation.toolDone)
		require.False(t, invocation.toolError)

		require.Equal(t, entryAssistant, conversation.entries[3].kind)
		require.Equal(t, "Done.", conversation.entries[3].text())
	})

	t.Run("separates thinking from the answer", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: " yes"})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "hello"})

		require.Len(t, conversation.entries, 2)
		require.Equal(t, entryThinking, conversation.entries[0].kind)
		require.Equal(t, "hmm yes", conversation.entries[0].text())
		require.Equal(t, entryAssistant, conversation.entries[1].kind)
	})

	t.Run("keeps a result that did not stream", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "boom",
			IsError:    true,
		})

		invocation := conversation.entries[0]
		require.True(t, invocation.toolDone)
		require.True(t, invocation.toolError)
		require.Equal(t, "boom", invocation.text())
	})

	t.Run("caps the tool output", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})

		chunk := strings.Repeat("x", maxToolOutput/2)
		for range 4 {
			conversation.apply(engine.Event{
				Type:       engine.EventToolOutput,
				ToolCallID: "call_1",
				Output:     chunk,
			})
		}
		conversation.apply(engine.Event{Type: engine.EventToolOutput, ToolCallID: "call_1"})

		invocation := conversation.entries[0]
		require.Len(t, invocation.text(), maxToolOutput)
		require.True(t, invocation.truncated)
	})

	t.Run("records errors and notices", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventError, Error: "boom"})
		conversation.addNotice("the run was interrupted")

		require.Len(t, conversation.entries, 2)
		require.Equal(t, entryError, conversation.entries[0].kind)
		require.Equal(t, "boom", conversation.entries[0].text())
		require.Equal(t, entryNotice, conversation.entries[1].kind)
	})

	t.Run("records retried model calls as notices", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: 250 * time.Millisecond,
			Error:   "engine: stream response: overloaded",
		})

		require.Len(t, conversation.entries, 1)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Contains(t, conversation.entries[0].text(), "retrying in 250ms")
		require.Contains(t, conversation.entries[0].text(), "overloaded")
	})

	t.Run("drops the partial response of a discarded attempt", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: "thinking"})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "partial"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: 250 * time.Millisecond,
			Error:   "stream ended before [DONE]",
			Discard: true,
		})

		require.Len(t, conversation.entries, 1)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Contains(t, conversation.entries[0].text(), "restarting the response")

		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "recovered"})
		require.Len(t, conversation.entries, 2)
		require.Equal(t, "recovered", conversation.entries[1].text())
	})

	t.Run("drops every partial response of consecutive attempts", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "first partial"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: time.Second,
			Error:   "truncated",
			Discard: true,
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "second partial"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 2,
			RetryIn: time.Second,
			Error:   "truncated",
			Discard: true,
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "recovered"})

		require.Len(t, conversation.entries, 3)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Equal(t, entryNotice, conversation.entries[1].kind)
		require.Equal(t, entryAssistant, conversation.entries[2].kind)
		require.Equal(t, "recovered", conversation.entries[2].text())
	})

	t.Run("keeps an earlier answer when an attempt is discarded", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "first"})
		conversation.apply(engine.Event{
			Type:       engine.EventMessageEnd,
			StopReason: llm.StopReasonToolUse,
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "ok",
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "second"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: 250 * time.Millisecond,
			Error:   "dropped",
			Discard: true,
		})

		require.Len(t, conversation.entries, 3)
		require.Equal(t, entryAssistant, conversation.entries[0].kind)
		require.Equal(t, "first", conversation.entries[0].text())
		require.Equal(t, entryTool, conversation.entries[1].kind)
		require.Equal(t, entryNotice, conversation.entries[2].kind)
	})

	t.Run("ignores output of unknown calls", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(
			engine.Event{Type: engine.EventToolOutput, ToolCallID: "ghost", Output: "x"},
		)
		conversation.apply(
			engine.Event{Type: engine.EventToolResult, ToolCallID: "ghost", Text: "x"},
		)

		require.Empty(t, conversation.entries)
	})

	t.Run("loads a stored conversation", func(t *testing.T) {
		conversation := transcript{}
		conversation.load([]llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{
				{Type: llm.BlockText, Text: "list the files"},
			}},
			{Role: llm.RoleAssistant, Blocks: []llm.Block{
				{Type: llm.BlockThinking, Thinking: "let me "},
				{Type: llm.BlockThinking, Thinking: "check"},
				{Type: llm.BlockText, Text: "sure"},
				{
					Type:              llm.BlockToolCall,
					ToolCallID:        "call_1",
					ToolCallName:      "shell",
					ToolCallArguments: json.RawMessage(`{"command":"ls"}`),
				},
			}},
			{Role: llm.RoleUser, Blocks: []llm.Block{
				{
					Type:             llm.BlockToolResult,
					ToolResultCallID: "call_1",
					ToolResult: []llm.Block{
						{Type: llm.BlockText, Text: "a.txt"},
					},
				},
			}},
		})

		require.Len(t, conversation.entries, 4)
		require.Equal(t, entryUser, conversation.entries[0].kind)
		require.Equal(t, "list the files", conversation.entries[0].text())
		require.Equal(t, entryThinking, conversation.entries[1].kind)
		require.Equal(t, "let me check", conversation.entries[1].text())
		require.Equal(t, entryAssistant, conversation.entries[2].kind)
		require.Equal(t, "sure", conversation.entries[2].text())

		invocation := conversation.entries[3]
		require.Equal(t, entryTool, invocation.kind)
		require.Equal(t, "shell", invocation.toolName)
		require.Equal(t, "a.txt", invocation.text())
		require.True(t, invocation.toolDone)
		require.False(t, invocation.toolError)
	})

	t.Run("loads failed invocations", func(t *testing.T) {
		conversation := transcript{}
		conversation.load([]llm.Message{
			{Role: llm.RoleAssistant, Blocks: []llm.Block{
				{Type: llm.BlockToolCall, ToolCallID: "call_1", ToolCallName: "shell"},
			}},
			{Role: llm.RoleUser, Blocks: []llm.Block{
				{
					Type:              llm.BlockToolResult,
					ToolResultCallID:  "call_1",
					ToolResultIsError: true,
					ToolResult: []llm.Block{
						{Type: llm.BlockText, Text: "boom"},
					},
				},
			}},
		})

		require.Len(t, conversation.entries, 1)
		require.True(t, conversation.entries[0].toolDone)
		require.True(t, conversation.entries[0].toolError)
		require.Equal(t, "boom", conversation.entries[0].text())
	})

	t.Run("tracks the entries that changed", func(t *testing.T) {
		conversation := transcript{}
		conversation.markRendered()

		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "one"})
		require.Equal(t, 0, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: " and two"})
		require.Equal(t, 0, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		require.Equal(t, 0, conversation.changedFrom(), "the entry before it changes too")

		conversation.markRendered()
		conversation.apply(engine.Event{
			Type:       engine.EventToolOutput,
			ToolCallID: "call_1",
			Output:     "a.txt",
		})
		require.Equal(t, 1, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "a.txt",
		})
		require.Equal(t, 1, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		require.Equal(t, 1, conversation.changedFrom(), "the entry before it changes too")
	})
}

// TestCutRunes verifies rune-safe truncation.
func TestCutRunes(t *testing.T) {
	t.Run("keeps short texts", func(t *testing.T) {
		require.Equal(t, "hello", cutRunes("hello", 10))
	})

	t.Run("never splits a rune", func(t *testing.T) {
		require.Equal(t, "añ", cutRunes("añejo", 3))
		require.Equal(t, "a", cutRunes("añejo", 2))
	})
}
