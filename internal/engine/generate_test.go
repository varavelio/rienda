package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// newGenerateTest builds an engine served by one scripted response.
func newGenerateTest(t *testing.T, events []llm.StreamEvent) (*Engine, *fakeClient) {
	t.Helper()

	client := &fakeClient{scripts: []script{{events: events}}}
	engine, _ := newTestEngine(t, Config{Client: client})
	return engine, client
}

// generateTurn runs one generation and returns the turn it assembled together
// with the events it emitted.
func generateTurn(t *testing.T, engine *Engine) (turn, []Event) {
	t.Helper()

	events := make(chan Event, 128)
	response, err := engine.generate(t.Context(), events)
	require.NoError(t, err)
	close(events)
	return response, collect(events)
}

// TestGenerate verifies streamed response assembly.
func TestGenerate(t *testing.T) {
	t.Run("assembles text, thinking, redacted reasoning and tool calls", func(t *testing.T) {
		engine, _ := newGenerateTest(t, []llm.StreamEvent{
			{Type: llm.StreamMessageStart, ID: "resp_1", Model: "wire-model"},
			{Type: llm.StreamThinkingDelta, Thinking: "Let me "},
			{Type: llm.StreamThinkingDelta, Thinking: "think"},
			{Type: llm.StreamThinkingDelta, ThinkingSignature: "sig-1"},
			{Type: llm.StreamThinkingRedacted, ThinkingRedactedData: "opaque"},
			{Type: llm.StreamTextDelta, Text: "Hello "},
			{Type: llm.StreamTextDelta, Text: "world"},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
			{
				Type:              llm.StreamToolCallArgsDelta,
				ToolCallID:        "call_1",
				ToolCallArgsDelta: `{"command":"ls`,
			},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: `"}`},
			{
				Type:       llm.StreamMessageEnd,
				StopReason: llm.StopReasonToolUse,
				Usage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
			},
		})

		response, events := generateTurn(t, engine)

		require.Equal(t, "wire-model", response.model)
		require.Equal(t, llm.StopReasonToolUse, response.stopReason)
		require.Equal(t, llm.Usage{InputTokens: 10, OutputTokens: 5}, response.usage)
		require.Empty(t, response.argumentErrors)
		require.Equal(t, []llm.Block{
			{Type: llm.BlockThinking, Thinking: "Let me think", ThinkingSignature: "sig-1"},
			{Type: llm.BlockRedactedThinking, ThinkingRedactedData: "opaque"},
			{Type: llm.BlockText, Text: "Hello world"},
			{
				Type:              llm.BlockToolCall,
				ToolCallID:        "call_1",
				ToolCallName:      "shell",
				ToolCallArguments: json.RawMessage(`{"command":"ls"}`),
			},
		}, response.blocks)
		require.Equal(t, []Event{
			{Type: EventThinkingDelta, Text: "Let me "},
			{Type: EventThinkingDelta, Text: "think"},
			{Type: EventTextDelta, Text: "Hello "},
			{Type: EventTextDelta, Text: "world"},
		}, events)
	})

	t.Run("falls back to the configured model", func(t *testing.T) {
		engine, _ := newGenerateTest(t, []llm.StreamEvent{
			{Type: llm.StreamTextDelta, Text: "hi"},
			{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
		})

		response, _ := generateTurn(t, engine)

		require.Equal(t, "test-model", response.model)
	})

	t.Run("normalizes tool arguments", func(t *testing.T) {
		engine, _ := newGenerateTest(t, []llm.StreamEvent{
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_2", ToolCallName: "shell"},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: "   "},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_3", ToolCallName: "shell"},
			{
				Type:              llm.StreamToolCallArgsDelta,
				ToolCallID:        "call_2",
				ToolCallArgsDelta: `{"command":`,
			},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_3", ToolCallArgsDelta: `[1,2]`},
			{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
		})

		response, _ := generateTurn(t, engine)

		require.Len(t, response.blocks, 3)
		for _, block := range response.blocks {
			require.JSONEq(t, "{}", string(block.ToolCallArguments))
		}
		require.Contains(t, response.argumentErrors, "call_2")
		require.Contains(t, response.argumentErrors, "call_3")
		require.NotContains(t, response.argumentErrors, "call_1")
		require.ErrorContains(t, response.argumentErrors["call_2"], "not valid JSON")
		require.ErrorContains(t, response.argumentErrors["call_3"], "not a JSON object")
	})

	t.Run("ignores malformed tool call fragments", func(t *testing.T) {
		engine, _ := newGenerateTest(t, []llm.StreamEvent{
			{Type: llm.StreamToolCallStart, ToolCallID: "", ToolCallName: "ghost"},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "missing", ToolCallArgsDelta: `{}`},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
			{Type: llm.StreamTextDelta, Text: "done"},
			{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
		})

		response, _ := generateTurn(t, engine)

		calls := response.toolCalls()
		require.Len(t, calls, 1)
		require.Equal(t, "call_1", calls[0].ToolCallID)
	})

	t.Run("reports empty responses", func(t *testing.T) {
		engine, _ := newGenerateTest(t, []llm.StreamEvent{
			{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
		})

		events := make(chan Event, 8)
		_, err := engine.generate(t.Context(), events)

		require.ErrorContains(t, err, "empty response")
	})

	t.Run("reports stream failures", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{nextErr: errors.New("boom")}}}
		engine, _ := newTestEngine(t, Config{Client: client})

		events := make(chan Event, 8)
		_, err := engine.generate(t.Context(), events)

		require.ErrorContains(t, err, "boom")
	})

	t.Run("reports streaming setup failures", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{openErr: errors.New("connect boom")}}}
		engine, _ := newTestEngine(t, Config{Client: client})

		events := make(chan Event, 8)
		_, err := engine.generate(t.Context(), events)

		require.ErrorContains(t, err, "connect boom")
	})

	t.Run("retries a transient failure before any output", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			{openErr: &llm.Error{
				Provider: "test",
				Kind:     llm.ErrorKindOverloaded,
				Message:  "overloaded",
			}},
			{events: []llm.StreamEvent{
				{Type: llm.StreamTextDelta, Text: "recovered"},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
			}},
		}}
		engine, _ := newTestEngine(t, Config{Client: client})

		response, events := generateTurn(t, engine)

		require.Equal(t, []llm.Block{{Type: llm.BlockText, Text: "recovered"}}, response.blocks)
		require.Len(t, client.requests, 2)
		require.Equal(t, []EventType{EventRetry, EventTextDelta}, eventTypes(events))
		require.Equal(t, 1, events[0].Attempt)
		require.Positive(t, events[0].RetryIn)
		require.Contains(t, events[0].Error, "overloaded")
	})

	t.Run("retries a stream truncated before its first delta", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			{nextErr: fmt.Errorf(
				"openai-chat-completions: stream ended before [DONE]: %w",
				io.ErrUnexpectedEOF,
			)},
			{events: []llm.StreamEvent{
				{Type: llm.StreamTextDelta, Text: "recovered"},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
			}},
		}}
		engine, _ := newTestEngine(t, Config{Client: client})

		response, events := generateTurn(t, engine)

		require.Equal(t, "recovered", response.blocks[0].Text)
		require.Len(t, client.requests, 2)
		require.Equal(t, []EventType{EventRetry, EventTextDelta}, eventTypes(events))
	})

	t.Run("restarts a response truncated after it reached the caller", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			{
				events: []llm.StreamEvent{{Type: llm.StreamTextDelta, Text: "partial"}},
				nextErr: fmt.Errorf(
					"openai-chat-completions: stream ended before [DONE]: %w",
					io.ErrUnexpectedEOF,
				),
			},
			{events: []llm.StreamEvent{
				{Type: llm.StreamTextDelta, Text: "recovered"},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
			}},
		}}
		engine, _ := newTestEngine(t, Config{Client: client})

		response, events := generateTurn(t, engine)

		require.Equal(t, "recovered", response.blocks[0].Text)
		require.Len(t, client.requests, 2)
		require.Equal(
			t,
			[]EventType{EventTextDelta, EventRetry, EventTextDelta},
			eventTypes(events),
		)
		require.True(t, events[1].Discard)
		require.Equal(t, "partial", events[0].Text)
		require.Contains(t, events[1].Error, "before [DONE]")
	})

	t.Run("marks a retry that streamed no output as no discard", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			{nextErr: &llm.Error{
				Provider: "test",
				Kind:     llm.ErrorKindOverloaded,
				Message:  "overloaded",
			}},
			{events: []llm.StreamEvent{
				{Type: llm.StreamTextDelta, Text: "recovered"},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
			}},
		}}
		engine, _ := newTestEngine(t, Config{Client: client})

		_, events := generateTurn(t, engine)

		require.Equal(t, []EventType{EventRetry, EventTextDelta}, eventTypes(events))
		require.False(t, events[0].Discard)
	})

	t.Run("does not restart a response that failed permanently", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{
			events: []llm.StreamEvent{{Type: llm.StreamTextDelta, Text: "partial"}},
			nextErr: &llm.Error{
				Provider: "test",
				Kind:     llm.ErrorKindInvalidRequest,
				Message:  "rejected",
			},
		}}}
		engine, _ := newTestEngine(t, Config{Client: client})

		events := make(chan Event, 8)
		_, err := engine.generate(t.Context(), events)
		close(events)
		collected := collect(events)

		require.ErrorContains(t, err, "rejected")
		require.Len(t, client.requests, 1)
		require.Equal(t, []EventType{EventTextDelta}, eventTypes(collected))
	})
}
