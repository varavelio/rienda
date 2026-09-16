package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tool"
)

// fakeTool is a scripted tool.Tool implementation.
type fakeTool struct {
	name    string
	output  string
	isError bool
	empty   bool
	err     error
	before  func(ctx context.Context)
	emitted []string
	calls   []tool.Call
	workdir string
}

// Definition describes the fake tool to the model.
func (t *fakeTool) Definition() llm.Tool {
	return llm.Tool{
		Name:        t.name,
		Description: "A scripted test tool.",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}
}

// Execute returns the scripted result of the tool.
func (t *fakeTool) Execute(
	ctx context.Context,
	call tool.Call,
	out tool.Sink,
) (tool.Result, error) {
	t.calls = append(t.calls, call)
	if t.before != nil {
		t.before(ctx)
	}
	if dir, ok := tool.WorkdirFromContext(ctx); ok {
		t.workdir = dir
	}
	for _, chunk := range t.emitted {
		out.Emit(tool.StreamStdout, []byte(chunk))
	}

	switch {
	case t.err != nil:
		return tool.Result{}, t.err
	case t.empty:
		return tool.Result{}, nil
	case t.isError:
		return tool.ErrorResult(t.output), nil
	default:
		return tool.TextResult(t.output), nil
	}
}

// closingClient closes the session when the stream opens, simulating a session
// that disappears while a run is in flight.
type closingClient struct {
	fakeClient
	store *session.Store
}

// Stream closes the store and serves the next script.
func (c *closingClient) Stream(ctx context.Context, request *llm.Request) (llm.Stream, error) {
	_ = c.store.Close()
	return c.fakeClient.Stream(ctx, request)
}

// TestRun verifies the conversation loop.
func TestRun(t *testing.T) {
	t.Run("answers a prompt", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{events: []llm.StreamEvent{
			{Type: llm.StreamMessageStart, ID: "resp_1", Model: "wire-model"},
			{Type: llm.StreamTextDelta, Text: "hi"},
			{
				Type:       llm.StreamMessageEnd,
				StopReason: llm.StopReasonEndTurn,
				Usage:      llm.Usage{InputTokens: 3, OutputTokens: 2},
			},
		}}}}
		engine, store := newTestEngine(t, Config{
			Client: client,
			Agent:  agent.Agent{ID: "coder", SystemPrompt: "be brief"},
		})

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventTextDelta, EventMessageEnd, EventRunEnd},
			eventTypes(events),
		)
		require.Equal(t, store.ID(), events[0].SessionID)
		require.Equal(t, "coder", events[0].AgentID)
		require.Equal(t, "test-model", events[0].ModelID)
		require.NotEmpty(t, events[0].EntryID)
		require.Equal(t, "hi", events[1].Text)
		require.NotEmpty(t, events[2].EntryID)
		require.Equal(t, llm.StopReasonEndTurn, events[2].StopReason)
		require.Equal(t, &Usage{InputTokens: 3, OutputTokens: 2}, events[2].Usage)
		require.Equal(t, EndReasonTurn, events[3].Reason)

		require.Len(t, client.requests, 1)
		require.Equal(t, "test-model", client.requests[0].Model)
		require.Equal(t, "be brief", client.requests[0].System)
		require.Equal(t, []llm.Message{{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}},
		}}, client.requests[0].Messages)
		require.Equal(t, []string{store.ID()}, client.sessionIDs)

		entries := store.Entries()
		require.Len(t, entries, 2)
		require.Equal(t, "wire-model", entries[1].ResponseModel)
		require.Equal(t, llm.StopReasonEndTurn, entries[1].ResponseStopReason)
		require.Equal(t, llm.Usage{InputTokens: 3, OutputTokens: 2}, entries[1].ResponseUsage)
	})

	t.Run("runs tools and continues the conversation", func(t *testing.T) {
		shellTool := &fakeTool{name: "shell", output: "file.txt", emitted: []string{"file", ".txt"}}
		client := &fakeClient{scripts: []script{
			{
				events: []llm.StreamEvent{
					{Type: llm.StreamTextDelta, Text: "checking"},
					{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
					{
						Type:              llm.StreamToolCallArgsDelta,
						ToolCallID:        "call_1",
						ToolCallArgsDelta: `{"command":"ls"}`,
					},
					{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
				},
			},
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, shellTool),
			Agent:    agent.Agent{ID: "coder", Tools: []string{"shell"}},
		})

		events := collect(engine.Run(t.Context(), "list files"))

		require.Equal(t, []EventType{
			EventRunStart,
			EventTextDelta, EventMessageEnd,
			EventToolCall, EventToolOutput, EventToolOutput, EventToolResult,
			EventTextDelta, EventMessageEnd,
			EventRunEnd,
		}, eventTypes(events))
		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)

		require.Len(t, shellTool.calls, 1)
		require.Equal(t, "call_1", shellTool.calls[0].ID)
		require.Equal(t, "shell", shellTool.calls[0].Name)
		require.JSONEq(t, `{"command":"ls"}`, string(shellTool.calls[0].Arguments))

		history := store.History()
		require.Len(t, history, 4)
		require.Equal(t, llm.RoleUser, history[2].Role)
		result := history[2].Blocks[0]
		require.Equal(t, llm.BlockToolResult, result.Type)
		require.Equal(t, "call_1", result.ToolResultCallID)
		require.False(t, result.ToolResultIsError)
		require.Equal(t, []llm.Block{{Type: llm.BlockText, Text: "file.txt"}}, result.ToolResult)

		require.Len(t, client.requests, 2)
		require.Len(t, client.requests[1].Messages, 3)
	})

	t.Run("batches the results of a turn into one message", func(t *testing.T) {
		echoTool := &fakeTool{name: "echo", output: "ok"}
		client := &fakeClient{scripts: []script{
			{events: []llm.StreamEvent{
				{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "echo"},
				{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: `{}`},
				{Type: llm.StreamToolCallStart, ToolCallID: "call_2", ToolCallName: "echo"},
				{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_2", ToolCallArgsDelta: `{}`},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
			}},
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, echoTool),
			Agent:    agent.Agent{Tools: []string{"echo"}},
		})

		collect(engine.Run(t.Context(), "go"))

		require.Len(t, echoTool.calls, 2)
		history := store.History()
		require.Len(t, history, 4)
		require.Len(t, history[2].Blocks, 2)
		require.Equal(t, "call_1", history[2].Blocks[0].ToolResultCallID)
		require.Equal(t, "call_2", history[2].Blocks[1].ToolResultCallID)
	})

	t.Run("reports unknown tools as results", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "ghost", `{}`),
			endTurn("ok"),
		}}
		engine, store := newTestEngine(t, Config{Client: client})

		events := collect(engine.Run(t.Context(), "go"))

		result := store.History()[2].Blocks[0]
		require.True(t, result.ToolResultIsError)
		require.Contains(t, result.ToolResult[0].Text, `unknown tool "ghost"`)

		tools := eventTypes(events)
		require.Contains(t, tools, EventToolCall)
		require.Contains(t, tools, EventToolResult)
	})

	t.Run("reports tool failures as results", func(t *testing.T) {
		brokenTool := &fakeTool{name: "broken", err: errors.New("tool: kaput")}
		failedTool := &fakeTool{name: "nonzero", output: "exit status 1", isError: true}
		client := &fakeClient{scripts: []script{
			{events: []llm.StreamEvent{
				{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "broken"},
				{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: `{}`},
				{Type: llm.StreamToolCallStart, ToolCallID: "call_2", ToolCallName: "nonzero"},
				{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_2", ToolCallArgsDelta: `{}`},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
			}},
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, brokenTool, failedTool),
			Agent:    agent.Agent{Tools: []string{"broken", "nonzero"}},
		})

		collect(engine.Run(t.Context(), "go"))

		require.Len(t, brokenTool.calls, 1)
		require.Len(t, failedTool.calls, 1)
		results := store.History()[2].Blocks
		require.Len(t, results, 2)
		require.True(t, results[0].ToolResultIsError)
		require.Equal(t, "tool: kaput", results[0].ToolResult[0].Text)
		require.True(t, results[1].ToolResultIsError)
		require.Equal(t, "exit status 1", results[1].ToolResult[0].Text)
	})

	t.Run("does not run calls with malformed arguments", func(t *testing.T) {
		shellTool := &fakeTool{name: "shell", output: "ok"}
		client := &fakeClient{scripts: []script{
			{events: []llm.StreamEvent{
				{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
				{
					Type:              llm.StreamToolCallArgsDelta,
					ToolCallID:        "call_1",
					ToolCallArgsDelta: `{"command":`,
				},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
			}},
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, shellTool),
			Agent:    agent.Agent{Tools: []string{"shell"}},
		})

		collect(engine.Run(t.Context(), "go"))

		require.Empty(t, shellTool.calls)
		result := store.History()[2].Blocks[0]
		require.True(t, result.ToolResultIsError)
		require.Contains(t, result.ToolResult[0].Text, "not executed")
	})

	t.Run("interrupts a streaming response", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{
			events: []llm.StreamEvent{{Type: llm.StreamTextDelta, Text: "partial"}},
			block:  true,
		}}}
		engine, store := newTestEngine(t, Config{Client: client})

		ctx, cancel := context.WithCancel(t.Context())
		events := engine.Run(ctx, "hello")

		require.Equal(t, EventRunStart, (<-events).Type)
		require.Equal(t, EventTextDelta, (<-events).Type)
		cancel()

		remaining := collect(events)
		require.Equal(t, []EventType{EventRunEnd}, eventTypes(remaining))
		require.Equal(t, EndReasonInterrupted, remaining[0].Reason)

		require.Len(t, store.History(), 1)
	})

	t.Run("persists interrupted results when canceled during tools", func(t *testing.T) {
		firstTool := &fakeTool{
			name:   "first",
			err:    context.Canceled,
			before: func(ctx context.Context) { <-ctx.Done() },
		}
		secondTool := &fakeTool{name: "second", output: "never"}
		client := &fakeClient{scripts: []script{{events: []llm.StreamEvent{
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "first"},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: `{}`},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_2", ToolCallName: "second"},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_2", ToolCallArgsDelta: `{}`},
			{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
		}}}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, firstTool, secondTool),
			Agent:    agent.Agent{Tools: []string{"first", "second"}},
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var events []Event
		for event := range engine.Run(ctx, "go") {
			if event.Type == EventToolCall && event.ToolName == "first" {
				cancel()
			}
			events = append(events, event)
		}

		last := events[len(events)-1]
		require.Equal(t, EventRunEnd, last.Type)
		require.Equal(t, EndReasonInterrupted, last.Reason)

		require.Len(t, firstTool.calls, 1)
		require.Empty(t, secondTool.calls)

		history := store.History()
		require.Len(t, history, 3)
		results := history[2].Blocks
		require.Len(t, results, 2)
		require.Equal(t, "call_1", results[0].ToolResultCallID)
		require.True(t, results[0].ToolResultIsError)
		require.Equal(t, interruptedDuringTool, results[0].ToolResult[0].Text)
		require.Equal(t, "call_2", results[1].ToolResultCallID)
		require.True(t, results[1].ToolResultIsError)
		require.Equal(t, interruptedBeforeTool, results[1].ToolResult[0].Text)
	})

	t.Run("stops at the turn limit", func(t *testing.T) {
		echoTool := &fakeTool{name: "echo", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "echo", `{}`),
			toolTurn("call_2", "echo", `{}`),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, echoTool),
			Agent:    agent.Agent{Tools: []string{"echo"}},
			MaxTurns: 2,
		})

		events := collect(engine.Run(t.Context(), "go"))

		last := events[len(events)-1]
		require.Equal(t, EndReasonMaxTurns, last.Reason)
		require.Len(t, client.requests, 2)
		require.Len(t, store.History(), 5)
	})

	t.Run("resumes from the active leaf without a prompt", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("resumed")}}
		engine, store := newTestEngine(t, Config{Client: client})
		_, err := store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "previous"}},
		}})
		require.NoError(t, err)

		events := collect(engine.Run(t.Context(), ""))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventTextDelta, EventMessageEnd, EventRunEnd},
			eventTypes(events),
		)
		require.Empty(t, events[0].EntryID)
		require.Len(t, client.requests[0].Messages, 1)
		require.Len(t, store.History(), 2)
	})

	t.Run("rejects empty continuations", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{})

		events := collect(engine.Run(t.Context(), ""))

		require.Equal(t, []EventType{EventError, EventRunEnd}, eventTypes(events))
		require.Contains(t, events[0].Error, "no conversation to continue")
		require.Equal(t, EndReasonError, events[1].Reason)
	})

	t.Run("reports stream failures", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{openErr: errors.New("connect boom")}}}
		engine, store := newTestEngine(t, Config{Client: client})

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(t, []EventType{EventRunStart, EventError, EventRunEnd}, eventTypes(events))
		require.Contains(t, events[1].Error, "connect boom")
		require.Equal(t, EndReasonError, events[2].Reason)
		require.Len(t, store.History(), 1)
	})

	t.Run("drops partial responses when the stream fails", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{
			events:  []llm.StreamEvent{{Type: llm.StreamTextDelta, Text: "partial"}},
			nextErr: errors.New("boom"),
		}}}
		engine, store := newTestEngine(t, Config{Client: client})

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventTextDelta, EventError, EventRunEnd},
			eventTypes(events),
		)
		require.Equal(t, EndReasonError, events[3].Reason)
		require.Len(t, store.History(), 1)
	})

	t.Run("passes the workdir to tools", func(t *testing.T) {
		workdir := t.TempDir()
		echoTool := &fakeTool{name: "echo", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "echo", `{}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, echoTool),
			Agent:    agent.Agent{Tools: []string{"echo"}},
			Workdir:  workdir,
		})

		collect(engine.Run(t.Context(), "go"))

		require.Equal(t, workdir, echoTool.workdir)
	})

	t.Run("reports tools without output", func(t *testing.T) {
		silentTool := &fakeTool{name: "silent", empty: true}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "silent", `{}`),
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, silentTool),
			Agent:    agent.Agent{Tools: []string{"silent"}},
		})

		collect(engine.Run(t.Context(), "go"))

		result := store.History()[2].Blocks[0]
		require.False(t, result.ToolResultIsError)
		require.Equal(t, []llm.Block{{Type: llm.BlockText, Text: "(no output)"}}, result.ToolResult)
	})

	t.Run("reports prompt persistence failures", func(t *testing.T) {
		engine, store := newTestEngine(t, Config{})
		require.NoError(t, store.Close())

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(t, []EventType{EventError, EventRunEnd}, eventTypes(events))
		require.Contains(t, events[0].Error, "store is closed")
		require.Equal(t, EndReasonError, events[1].Reason)
	})

	t.Run("reports response persistence failures", func(t *testing.T) {
		store := newTestStore(t)
		client := &closingClient{
			scripts: []script{endTurn("hi")},
			store:   store,
		}
		engine, err := New(Config{Client: client, Store: store, Model: Model{ID: "test-model"}})
		require.NoError(t, err)

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventTextDelta, EventError, EventRunEnd},
			eventTypes(events),
		)
		require.Contains(t, events[2].Error, "store is closed")
		require.Equal(t, EndReasonError, events[3].Reason)
	})

	t.Run("reports tool result persistence failures", func(t *testing.T) {
		echoTool := &fakeTool{name: "echo", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "echo", `{}`),
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Client:   client,
			Registry: newTestRegistry(t, echoTool),
			Agent:    agent.Agent{Tools: []string{"echo"}},
		})
		echoTool.before = func(context.Context) { require.NoError(t, store.Close()) }

		events := collect(engine.Run(t.Context(), "go"))

		require.Equal(t, EventRunEnd, events[len(events)-1].Type)
		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
		require.Contains(t, events[len(events)-2].Error, "store is closed")
		require.Len(t, store.Entries(), 2)
	})
}
