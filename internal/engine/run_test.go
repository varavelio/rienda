package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
			Agents:   []agent.Agent{{ID: "coder", SystemPrompt: "be brief"}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{
				EventRunStart,
				EventContext,
				EventTextDelta, EventMessageEnd, EventContext,
				EventRunEnd,
			},
			eventTypes(events),
		)
		require.Equal(t, store.ID(), events[0].SessionID)
		require.Equal(t, "coder", events[0].AgentID)
		require.Equal(t, "test-model", events[0].ModelID)
		require.NotEmpty(t, events[0].EntryID)
		require.Equal(t, "hi", events[2].Text)
		require.NotEmpty(t, events[3].EntryID)
		require.Equal(t, llm.StopReasonEndTurn, events[3].StopReason)
		require.Equal(t, &Usage{InputTokens: 3, OutputTokens: 2}, events[3].Usage)
		require.Equal(t, EndReasonTurn, events[5].Reason)

		require.Len(t, client.requests, 1)
		require.Equal(t, "test-model", client.requests[0].Model)
		require.Equal(t, "be brief", client.requests[0].System)
		require.Equal(t, []llm.Message{{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}},
		}}, client.requests[0].Messages)

		entries := store.Entries()
		require.Len(t, entries, 2)
		require.Equal(t, "wire-model", entries[1].ResponseModel)
		require.Equal(t, llm.StopReasonEndTurn, entries[1].ResponseStopReason)
		require.Equal(t, llm.Usage{InputTokens: 3, OutputTokens: 2}, entries[1].ResponseUsage)
	})

	t.Run("reports the context after every change to the conversation", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "echo", `{}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, &fakeTool{
				name:   "echo",
				output: strings.Repeat("ok", 500),
			}),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"echo"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model", ContextWindow: 100000}),
		})

		events := collect(engine.Run(t.Context(), "go"))

		contexts := make([]*ContextInfo, 0, 4)
		for _, event := range events {
			if event.Type == EventContext {
				contexts = append(contexts, event.Context)
			}
		}
		require.Len(t, contexts, 4, "the prompt, the answer and both tool results are measured")

		// Every measurement covers the request the next turn would send, so the
		// figure grows as the conversation does.
		for _, info := range contexts {
			require.NotNil(t, info)
			require.Equal(t, 100000, info.Window)
			require.Positive(t, info.Used)
			require.GreaterOrEqual(t, info.Percent, 0.0)
			require.LessOrEqual(t, info.Percent, 100.0)
		}
		for index := 1; index < len(contexts); index++ {
			require.Greater(
				t,
				contexts[index].Used,
				contexts[index-1].Used,
				"the figure follows the conversation as it grows",
			)
		}
	})

	t.Run("measures a model whose window was not resolved", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("hi")}}
		engine, _ := newTestEngine(t, Config{
			Resolver: &testResolver{
				client: client,
				models: map[string]Model{"test/model": {ID: "wire", ContextWindow: 0}},
			},
		})

		events := collect(engine.Run(t.Context(), "hello"))

		contexts := make([]*ContextInfo, 0, 2)
		for _, event := range events {
			if event.Type == EventContext {
				contexts = append(contexts, event.Context)
			}
		}
		require.Len(t, contexts, 2, "the raw estimate is reported even without a window")

		// The estimate stands, but there is no window to turn it into a
		// percentage, so the front end has nothing to render.
		for _, info := range contexts {
			require.Positive(t, info.Used)
			require.Zero(t, info.Window)
			require.Zero(t, info.Percent)
		}
	})

	t.Run("runs the agent the branch selected", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("one"), endTurn("two")}}
		planner := agent.Agent{ID: "coder", SystemPrompt: "plan", Tools: []string{"echo"}}
		implementer := agent.Agent{ID: "implementer", SystemPrompt: "build"}
		engine, store := newTestEngine(t, Config{
			Registry: newTestRegistry(t, &fakeTool{name: "echo"}),
			Agents:   []agent.Agent{planner, implementer},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "first"))
		require.NoError(t, store.SetAgent(t.Context(), "implementer"))
		collect(engine.Run(t.Context(), "second"))

		require.Len(t, client.requests, 2)
		require.Equal(t, "plan", client.requests[0].System)
		require.Len(t, client.requests[0].Tools, 1, "the planner declares a tool")
		require.Equal(t, "build", client.requests[1].System)
		require.Empty(t, client.requests[1].Tools, "the implementer declares none")
	})

	t.Run("reports the agent of the run that started", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("one"), endTurn("two")}}
		engine, store := newTestEngine(t, Config{
			Agents:   []agent.Agent{{ID: "coder"}, {ID: "reviewer"}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "first"))
		before := collect(engine.Run(t.Context(), "second"))
		require.NoError(t, store.SetAgent(t.Context(), "reviewer"))
		after := collect(engine.Run(t.Context(), "third"))

		require.Equal(t, "coder", before[0].AgentID)
		require.Equal(t, "reviewer", after[0].AgentID, "the branch selected another agent")
	})

	t.Run("fails before writing when the branch names a missing agent", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("one")}}
		engine, store := newTestEngine(t, Config{
			Agents:   []agent.Agent{{ID: "coder"}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})
		collect(engine.Run(t.Context(), "first"))
		requests, entries := len(client.requests), len(store.Entries())

		// The store accepts a selection Rienda cannot run: an agent whose
		// definition is gone. The run fails before anything is written, so the
		// conversation stays as it was.
		require.NoError(t, store.SetAgent(t.Context(), "ghost"))

		events := collect(engine.Run(t.Context(), "second"))

		require.Equal(t, []EventType{EventError, EventRunEnd}, eventTypes(events))
		require.Contains(t, events[0].Error, `unknown agent "ghost"`)
		require.Len(t, client.requests, requests, "no request reached the provider")
		require.Len(t, store.Entries(), entries+1, "only the selection was written")
	})

	t.Run("runs the model the branch selected", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("one"), endTurn("two")}}
		engine, store := newTestEngine(t, Config{
			Resolver: &testResolver{
				client: client,
				models: map[string]Model{
					"test/model":  {ID: "wire-one", ContextWindow: 1000},
					"other/model": {ID: "wire-two", ContextWindow: 2000},
				},
			},
		})

		first := collect(engine.Run(t.Context(), "first"))
		require.NoError(t, store.SetModel(t.Context(), "other/model"))
		second := collect(engine.Run(t.Context(), "second"))

		require.Equal(t, "wire-one", first[0].ModelID)
		require.Equal(t, "wire-two", second[0].ModelID, "the branch selected another model")
		require.Len(t, client.requests, 2)
		require.Equal(t, "wire-one", client.requests[0].Model)
		require.Equal(t, "wire-two", client.requests[1].Model)

		// The window of the branch is the one of the model it runs, so the
		// measurement the footer shows follows the selection.
		report, err := engine.Context()
		require.NoError(t, err)
		require.Equal(t, 2000, report.Window)
	})

	t.Run("fails before writing when the branch names a missing model", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("one")}}
		engine, store := newTestEngine(t, Config{
			Resolver: &testResolver{
				client: client,
				models: map[string]Model{"test/model": {ID: "wire-one"}},
			},
		})
		collect(engine.Run(t.Context(), "first"))
		requests, entries := len(client.requests), len(store.Entries())

		require.NoError(t, store.SetModel(t.Context(), "gone/model"))

		events := collect(engine.Run(t.Context(), "second"))

		require.Equal(t, []EventType{EventError, EventRunEnd}, eventTypes(events))
		require.Contains(t, events[0].Error, `unknown model "gone/model"`)
		require.Len(t, client.requests, requests, "no request reached the provider")
		require.Len(t, store.Entries(), entries+1, "only the selection was written")
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
			Registry: newTestRegistry(t, shellTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"shell"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "list files"))

		require.Equal(t, []EventType{
			EventRunStart,
			EventContext,
			EventTextDelta, EventMessageEnd, EventContext,
			EventToolCall, EventToolOutput, EventToolOutput, EventToolResult, EventContext,
			EventTextDelta, EventMessageEnd, EventContext,
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
			Registry: newTestRegistry(t, echoTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"echo"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))

		require.Len(t, echoTool.calls, 2)
		history := store.History()
		require.Len(t, history, 4)
		require.Len(t, history[2].Blocks, 2)
		require.Equal(t, "call_1", history[2].Blocks[0].ToolResultCallID)
		require.Equal(t, "call_2", history[2].Blocks[1].ToolResultCallID)
	})

	t.Run("persists and replays provider item identifiers", func(t *testing.T) {
		echoTool := &fakeTool{name: "echo", output: "ok"}
		client := &fakeClient{scripts: []script{
			{events: []llm.StreamEvent{
				{
					Type: llm.StreamThinkingDelta, Thinking: "plan",
					ThinkingSignature: "enc-1", ThinkingID: "rs_1",
				},
				{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "echo"},
				{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: `{}`},
				{
					Type: llm.StreamMessageEnd, ItemID: "msg_1",
					StopReason: llm.StopReasonToolUse,
				},
			}},
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Registry: newTestRegistry(t, echoTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"echo"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))

		history := store.History()
		require.Len(t, history, 4)
		require.Equal(t, "msg_1", history[1].ItemID)
		require.Equal(t, "rs_1", history[1].Blocks[0].ThinkingID)
		require.Equal(t, "enc-1", history[1].Blocks[0].ThinkingSignature)

		require.Len(t, client.requests, 2)
		replayed := client.requests[1].Messages[1]
		require.Equal(t, "msg_1", replayed.ItemID)
		require.Equal(t, "rs_1", replayed.Blocks[0].ThinkingID)
	})

	t.Run("reports unknown tools as results", func(t *testing.T) {
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "ghost", `{}`),
			endTurn("ok"),
		}}
		engine, store := newTestEngine(t, Config{
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

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
			Registry: newTestRegistry(t, brokenTool, failedTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"broken", "nonzero"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
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
			Registry: newTestRegistry(t, shellTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"shell"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
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
		engine, store := newTestEngine(t, Config{
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		ctx, cancel := context.WithCancel(t.Context())
		events := engine.Run(ctx, "hello")

		require.Equal(t, EventRunStart, (<-events).Type)
		require.Equal(t, EventContext, (<-events).Type)
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
			Registry: newTestRegistry(t, firstTool, secondTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"first", "second"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
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

	t.Run("resumes from the active leaf without a prompt", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("resumed")}}
		engine, store := newTestEngine(t, Config{
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})
		_, err := store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "previous"}},
		}})
		require.NoError(t, err)

		events := collect(engine.Run(t.Context(), ""))

		require.Equal(
			t,
			[]EventType{
				EventRunStart,
				EventContext,
				EventTextDelta, EventMessageEnd, EventContext,
				EventRunEnd,
			},
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
		engine, store := newTestEngine(t, Config{
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventContext, EventError, EventRunEnd},
			eventTypes(events),
		)
		require.Contains(t, events[2].Error, "connect boom")
		require.Equal(t, EndReasonError, events[3].Reason)
		require.Len(t, store.History(), 1)
	})

	t.Run("drops partial responses when the stream fails", func(t *testing.T) {
		client := &fakeClient{scripts: []script{{
			events:  []llm.StreamEvent{{Type: llm.StreamTextDelta, Text: "partial"}},
			nextErr: errors.New("boom"),
		}}}
		engine, store := newTestEngine(t, Config{
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventContext, EventTextDelta, EventError, EventRunEnd},
			eventTypes(events),
		)
		require.Equal(t, EndReasonError, events[4].Reason)
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
			Registry: newTestRegistry(t, echoTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"echo"}}},
			Workdir:  workdir,
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
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
			Registry: newTestRegistry(t, silentTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"silent"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
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
		engine, err := New(Config{
			Store:    store,
			Agents:   []agent.Agent{{ID: "coder"}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})
		require.NoError(t, err)

		events := collect(engine.Run(t.Context(), "hello"))

		require.Equal(
			t,
			[]EventType{EventRunStart, EventContext, EventTextDelta, EventError, EventRunEnd},
			eventTypes(events),
		)
		require.Contains(t, events[3].Error, "store is closed")
		require.Equal(t, EndReasonError, events[4].Reason)
	})

	t.Run("reports tool result persistence failures", func(t *testing.T) {
		echoTool := &fakeTool{name: "echo", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "echo", `{}`),
			endTurn("done"),
		}}
		engine, store := newTestEngine(t, Config{
			Registry: newTestRegistry(t, echoTool),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"echo"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})
		echoTool.before = func(context.Context) { require.NoError(t, store.Close()) }

		events := collect(engine.Run(t.Context(), "go"))

		require.Equal(t, EventRunEnd, events[len(events)-1].Type)
		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
		require.Contains(t, events[len(events)-2].Error, "store is closed")
		require.Len(t, store.Entries(), 2)
	})
}
