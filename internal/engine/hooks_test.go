package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/llm"
)

// stubHooks records the order hooks fire and scripts their results.
type stubHooks struct {
	mu    sync.Mutex
	order []string

	beforeRequest func(BeforeModelRequestHook) BeforeModelRequestResult
	afterResponse func(AfterModelResponseHook) AfterModelResponseResult
	beforeTool    func(BeforeToolExecuteHook) BeforeToolExecuteResult
	afterTool     func(AfterToolExecuteHook) AfterToolExecuteResult
}

func (s *stubHooks) record(point string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, point)
}

func (s *stubHooks) Order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.order...)
}

func (s *stubHooks) BeforeRun(context.Context, BeforeRunHook) { s.record("beforeRun") }

func (s *stubHooks) AfterRun(context.Context, AfterRunHook) { s.record("afterRun") }

func (s *stubHooks) BeforeModelRequest(
	_ context.Context,
	ev BeforeModelRequestHook,
) BeforeModelRequestResult {
	s.record("beforeModelRequest")
	if s.beforeRequest != nil {
		return s.beforeRequest(ev)
	}
	return BeforeModelRequestResult{}
}

func (s *stubHooks) AfterModelResponse(
	_ context.Context,
	ev AfterModelResponseHook,
) AfterModelResponseResult {
	s.record("afterModelResponse")
	if s.afterResponse != nil {
		return s.afterResponse(ev)
	}
	return AfterModelResponseResult{}
}

func (s *stubHooks) BeforeToolExecute(
	_ context.Context,
	ev BeforeToolExecuteHook,
) BeforeToolExecuteResult {
	s.record("beforeToolExecute")
	if s.beforeTool != nil {
		return s.beforeTool(ev)
	}
	return BeforeToolExecuteResult{}
}

func (s *stubHooks) AfterToolExecute(
	_ context.Context,
	ev AfterToolExecuteHook,
) AfterToolExecuteResult {
	s.record("afterToolExecute")
	if s.afterTool != nil {
		return s.afterTool(ev)
	}
	return AfterToolExecuteResult{}
}

// stubResolver resolves any name to the same stub.
type stubResolver struct {
	stub Hooks
}

func (r stubResolver) Resolve(names []string) (Hooks, error) {
	return r.stub, nil
}

// deny returns a refusal pointer.
func deny() *bool {
	allow := false
	return &allow
}

// runIdentityOf reads the run identity of a hook context.
func runIdentityOf(ctx context.Context) (session, agentID, model string) {
	run, ok := agentRunOf(ctx)
	if !ok {
		return "", "", ""
	}
	return run.SessionID, run.Agent.ID, run.ModelID
}

func TestHooks(t *testing.T) {
	t.Run("fires in the order of the run", func(t *testing.T) {
		stub := &stubHooks{}
		shellTool := &fakeTool{name: "shell", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "shell", `{"command":"ls"}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, shellTool),
			Hooks:    stubResolver{stub: stub},
			Agents: []agent.Agent{
				{ID: "coder", Tools: []string{"shell"}, Hooks: []string{"probe"}},
			},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "go"))
		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)
		require.Equal(t, []string{
			"beforeRun",
			"beforeModelRequest", "afterModelResponse",
			"beforeToolExecute", "afterToolExecute",
			"beforeModelRequest", "afterModelResponse",
			"afterRun",
		}, stub.Order())
	})

	t.Run("refuses a call with the reason", func(t *testing.T) {
		stub := &stubHooks{}
		stub.beforeTool = func(ev BeforeToolExecuteHook) BeforeToolExecuteResult {
			return BeforeToolExecuteResult{Allow: deny(), Reason: "nope"}
		}
		shellTool := &fakeTool{name: "shell", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "shell", `{"command":"ls"}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, shellTool),
			Hooks:    stubResolver{stub: stub},
			Agents: []agent.Agent{
				{ID: "coder", Tools: []string{"shell"}, Hooks: []string{"probe"}},
			},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "go"))
		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)
		require.Empty(t, shellTool.calls, "the tool never runs")
		require.Contains(t, stub.Order(), "afterToolExecute")
		var result Event
		for _, event := range events {
			if event.Type == EventToolResult {
				result = event
			}
		}
		require.True(t, result.IsError)
		require.Equal(t, "nope", result.Text)
	})

	t.Run("rewrites arguments for the tool", func(t *testing.T) {
		stub := &stubHooks{}
		stub.beforeTool = func(ev BeforeToolExecuteHook) BeforeToolExecuteResult {
			return BeforeToolExecuteResult{Arguments: json.RawMessage(`{"command":"rewritten"}`)}
		}
		shellTool := &fakeTool{name: "shell", output: "ok"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "shell", `{"command":"original"}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, shellTool),
			Hooks:    stubResolver{stub: stub},
			Agents: []agent.Agent{
				{ID: "coder", Tools: []string{"shell"}, Hooks: []string{"probe"}},
			},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))
		require.Len(t, shellTool.calls, 1)
		require.JSONEq(t, `{"command":"rewritten"}`, string(shellTool.calls[0].Arguments))
	})

	t.Run("repairs malformed arguments", func(t *testing.T) {
		stub := &stubHooks{}
		stub.beforeTool = func(ev BeforeToolExecuteHook) BeforeToolExecuteResult {
			return BeforeToolExecuteResult{Arguments: json.RawMessage(`{"command":"fixed"}`)}
		}
		shellTool := &fakeTool{name: "shell", output: "ok"}
		client := &fakeClient{scripts: []script{{
			events: []llm.StreamEvent{
				{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "shell"},
				{
					Type:              llm.StreamToolCallArgsDelta,
					ToolCallID:        "call_1",
					ToolCallArgsDelta: `nope`,
				},
				{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
			},
		}, endTurn("done")}}
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, shellTool),
			Hooks:    stubResolver{stub: stub},
			Agents: []agent.Agent{
				{ID: "coder", Tools: []string{"shell"}, Hooks: []string{"probe"}},
			},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))
		require.Len(t, shellTool.calls, 1, "the repaired call runs")
	})

	t.Run("fires for an unknown tool", func(t *testing.T) {
		stub := &stubHooks{}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "ghost", `{}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Hooks:    stubResolver{stub: stub},
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"probe"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "go"))
		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)
		require.Contains(t, stub.Order(), "beforeToolExecute")
		require.Contains(t, stub.Order(), "afterToolExecute")
	})

	t.Run("applies system to one turn only", func(t *testing.T) {
		stub := &stubHooks{}
		calls := 0
		stub.beforeRequest = func(ev BeforeModelRequestHook) BeforeModelRequestResult {
			calls++
			require.NotEmpty(t, ev.Messages)
			if calls == 1 {
				return BeforeModelRequestResult{System: "injected", Set: true}
			}
			return BeforeModelRequestResult{}
		}
		client := &fakeClient{scripts: []script{endTurn("one"), endTurn("two")}}
		engine, _ := newTestEngine(t, Config{
			Hooks:    stubResolver{stub: stub},
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"probe"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))
		require.Len(t, client.requests, 1)
		require.Equal(t, "injected", client.requests[0].System)
	})

	t.Run("rewrites the assistant prose", func(t *testing.T) {
		stub := &stubHooks{}
		stub.afterResponse = func(ev AfterModelResponseHook) AfterModelResponseResult {
			require.Equal(t, "secret 123", ev.Text)
			return AfterModelResponseResult{Text: "redacted", TextSet: true}
		}
		client := &fakeClient{scripts: []script{endTurn("secret 123")}}
		engine, store := newTestEngine(t, Config{
			Hooks:    stubResolver{stub: stub},
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"probe"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))
		history := store.History()
		require.Equal(t, "redacted", history[len(history)-1].Blocks[0].Text)
	})

	t.Run("refuses an emptied message", func(t *testing.T) {
		stub := &stubHooks{}
		stub.afterResponse = func(ev AfterModelResponseHook) AfterModelResponseResult {
			return AfterModelResponseResult{Text: "", TextSet: true}
		}
		client := &fakeClient{scripts: []script{endTurn("hi")}}
		engine, _ := newTestEngine(t, Config{
			Hooks:    stubResolver{stub: stub},
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"probe"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "go"))
		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
	})

	t.Run("rewrites the tool result", func(t *testing.T) {
		stub := &stubHooks{}
		stub.afterTool = func(ev AfterToolExecuteHook) AfterToolExecuteResult {
			require.Equal(t, "raw", ev.Result.Text)
			return AfterToolExecuteResult{Text: "cooked", TextSet: true}
		}
		shellTool := &fakeTool{name: "shell", output: "raw"}
		client := &fakeClient{scripts: []script{
			toolTurn("call_1", "shell", `{}`),
			endTurn("done"),
		}}
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, shellTool),
			Hooks:    stubResolver{stub: stub},
			Agents: []agent.Agent{
				{ID: "coder", Tools: []string{"shell"}, Hooks: []string{"probe"}},
			},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "go"))
		var result Event
		for _, event := range events {
			if event.Type == EventToolResult {
				result = event
			}
		}
		require.Equal(t, "cooked", result.Text)
	})

	t.Run("resolves per turn and collapses duplicates", func(t *testing.T) {
		stub := &stubHooks{}
		resolver := &resolveCounter{stub: stub}
		client := &fakeClient{scripts: []script{endTurn("done")}}
		engine, _ := newTestEngine(t, Config{
			Hooks:    resolver,
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"a", "a", "b"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "go"))
		require.NotEmpty(t, resolver.seen)
		for _, names := range resolver.seen {
			require.Equal(t, []string{"a", "b"}, names)
		}
	})

	t.Run("fails a run on an unknown hook", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("done")}}
		engine, _ := newTestEngine(t, Config{
			Hooks:    errResolver{},
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"ghost"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "go"))
		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
		require.Contains(t, events[len(events)-2].Error, "ghost")
	})

	t.Run("a nil resolver changes nothing", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("hi")}}
		engine, _ := newTestEngine(t, Config{
			Agents:   []agent.Agent{{ID: "coder"}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		events := collect(engine.Run(t.Context(), "hi"))
		require.Equal(t, []EventType{
			EventRunStart,
			EventContext,
			EventTextDelta, EventMessageEnd, EventContext,
			EventRunEnd,
		}, eventTypes(events))
	})

	t.Run("emits notices only with hooks", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("hi")}}
		engine, _ := newTestEngine(t, Config{
			Agents:   []agent.Agent{{ID: "coder"}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		for _, event := range collect(engine.Run(t.Context(), "hi")) {
			require.NotEqual(t, EventNotice, event.Type)
		}
	})

	t.Run("attaches the run identity", func(t *testing.T) {
		stub := &identityHooks{}
		client := &fakeClient{scripts: []script{endTurn("hi")}}
		engine, store := newTestEngine(t, Config{
			Hooks:    stubResolver{stub: stub},
			Agents:   []agent.Agent{{ID: "coder", Hooks: []string{"probe"}}},
			Resolver: newTestResolver(client, Model{ID: "test-model"}),
		})

		collect(engine.Run(t.Context(), "hi"))
		require.Equal(t, store.ID(), stub.session)
		require.Equal(t, "coder", stub.agent)
		require.Equal(t, "test-model", stub.model)
	})
}

// errResolver fails every resolution naming the hook.
type errResolver struct{}

func (errResolver) Resolve(names []string) (Hooks, error) {
	return nil, &hookUnknownError{names: names}
}

// hookUnknownError names an unknown hook like the registry does.
type hookUnknownError struct {
	names []string
}

func (e *hookUnknownError) Error() string {
	return "unknown hook " + e.names[0]
}

// resolveCounter records the names Resolve received.
type resolveCounter struct {
	stub Hooks
	seen [][]string
}

func (r *resolveCounter) Resolve(names []string) (Hooks, error) {
	r.seen = append(r.seen, append([]string{}, names...))
	return r.stub, nil
}

// identityHooks captures the run identity of a hook call.
type identityHooks struct {
	stubHooks
	session string
	agent   string
	model   string
}

func (s *identityHooks) BeforeRun(ctx context.Context, ev BeforeRunHook) {
	s.session, s.agent, s.model = runIdentityOf(ctx)
}

func TestResolveHooks(t *testing.T) {
	t.Run("yields a no-op without names", func(t *testing.T) {
		hooks, err := resolveHooks(nil, nil)
		require.NoError(t, err)
		require.NotNil(t, hooks)
	})

	t.Run("requires a resolver for declared hooks", func(t *testing.T) {
		_, err := resolveHooks(nil, []string{"a"})
		require.ErrorContains(t, err, "hook resolver")
	})

	t.Run("collapses duplicates", func(t *testing.T) {
		stub := &stubHooks{}
		hooks, err := resolveHooks(stubResolver{stub: stub}, []string{"a", "a"})
		require.NoError(t, err)
		require.Same(t, stub, hooks)
	})
}

func TestExtensionDiagnostics(t *testing.T) {
	t.Run("reports harness diagnostics once per run", func(t *testing.T) {
		client := &fakeClient{scripts: []script{endTurn("hi")}}
		engine, _ := newTestEngine(t, Config{
			Agents:      []agent.Agent{{ID: "coder"}},
			Resolver:    newTestResolver(client, Model{ID: "test-model"}),
			Diagnostics: []string{`tool "broken": kaput`},
		})

		events := collect(engine.Run(t.Context(), "hi"))
		var reported []string
		for _, event := range events {
			if len(event.Diagnostics) > 0 {
				reported = append(reported, event.Diagnostics...)
			}
		}
		require.Equal(t, []string{`tool "broken": kaput`}, reported)
	})
}

func TestNoticeCarrier(t *testing.T) {
	t.Run("round trips", func(t *testing.T) {
		called := false
		ctx := WithNotice(t.Context(), func(string) { called = true })
		emit, ok := NoticeFromContext(ctx)
		require.True(t, ok)
		emit("hi")
		require.True(t, called)
	})

	t.Run("reports absence", func(t *testing.T) {
		_, ok := NoticeFromContext(t.Context())
		require.False(t, ok)
	})
}
