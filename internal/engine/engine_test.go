package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tool"
)

// fakeClient is a scripted llm.Client: every Stream call consumes the next
// script and the received requests are recorded for assertions.
type fakeClient struct {
	requests   []*llm.Request
	sessionIDs []string
	scripts    []script
}

// Stream returns the next scripted response.
func (c *fakeClient) Stream(ctx context.Context, request *llm.Request) (llm.Stream, error) {
	c.requests = append(c.requests, request)
	sessionID, _ := llm.SessionIDFromContext(ctx)
	c.sessionIDs = append(c.sessionIDs, sessionID)

	if len(c.requests) > len(c.scripts) {
		return nil, fmt.Errorf("fakeClient: unexpected stream call %d", len(c.requests))
	}
	script := c.scripts[len(c.requests)-1]
	if script.openErr != nil {
		return nil, script.openErr
	}
	return &fakeStream{done: ctx.Done(), script: script}, nil
}

// Generate is not scripted: the engine runs exclusively over streams.
func (c *fakeClient) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, errors.New("fakeClient: Generate is not scripted")
}

// script is one scripted model response.
type script struct {
	// openErr fails the Stream call.
	openErr error

	// events are delivered in order before any failure.
	events []llm.StreamEvent

	// nextErr fails the stream after its events.
	nextErr error

	// block makes the stream wait for cancellation after its events.
	block bool
}

// endTurn scripts a response that answers text and finishes the turn.
func endTurn(text string) script {
	return script{events: []llm.StreamEvent{
		{Type: llm.StreamTextDelta, Text: text},
		{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonEndTurn},
	}}
}

// toolTurn scripts a response that requests one tool call.
func toolTurn(id, name, arguments string) script {
	return script{events: []llm.StreamEvent{
		{Type: llm.StreamToolCallStart, ToolCallID: id, ToolCallName: name},
		{Type: llm.StreamToolCallArgsDelta, ToolCallID: id, ToolCallArgsDelta: arguments},
		{Type: llm.StreamMessageEnd, StopReason: llm.StopReasonToolUse},
	}}
}

// fakeStream replays the events of a script.
type fakeStream struct {
	done   <-chan struct{}
	script script
	index  int
}

// Next returns the next scripted event.
func (s *fakeStream) Next() (llm.StreamEvent, error) {
	if s.index < len(s.script.events) {
		event := s.script.events[s.index]
		s.index++
		return event, nil
	}

	switch {
	case s.script.block:
		<-s.done
		return llm.StreamEvent{}, context.Canceled
	case s.script.nextErr != nil:
		return llm.StreamEvent{}, s.script.nextErr
	default:
		return llm.StreamEvent{}, io.EOF
	}
}

// Close releases the stream.
func (s *fakeStream) Close() error { return nil }

// newTestStore creates a session store in a temporary directory.
func newTestStore(t *testing.T) *session.Store {
	t.Helper()

	store, err := session.Create(t.Context(), t.TempDir(), session.Header{
		Agent: "coder",
		Model: "test/model",
	}, id.NewIDGenerator())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// newTestEngine builds an engine backed by a real session store in a
// temporary directory and returns both.
func newTestEngine(t *testing.T, cfg Config) (*Engine, *session.Store) {
	t.Helper()

	store := newTestStore(t)
	cfg.Store = store
	if cfg.Client == nil {
		cfg.Client = &fakeClient{}
	}
	if cfg.Model.ID == "" {
		cfg.Model.ID = "test-model"
	}

	engine, err := New(cfg)
	require.NoError(t, err)
	return engine, store
}

// newTestRegistry builds a registry holding tools.
func newTestRegistry(t *testing.T, tools ...tool.Tool) *tool.Registry {
	t.Helper()

	registry, err := tool.NewRegistry(tools...)
	require.NoError(t, err)
	return registry
}

// collect drains an event channel.
func collect(events <-chan Event) []Event {
	collected := make([]Event, 0, 8)
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

// eventTypes returns the types of collected events.
func eventTypes(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

// TestNew verifies engine construction.
func TestNew(t *testing.T) {
	store := newTestStore(t)
	base := func() Config {
		return Config{Client: &fakeClient{}, Store: store, Model: Model{ID: "test-model"}}
	}

	t.Run("rejects invalid configurations", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(*Config)
			wantErr string
		}{
			{
				name:    "missing client",
				mutate:  func(cfg *Config) { cfg.Client = nil },
				wantErr: "client is required",
			},
			{
				name:    "missing store",
				mutate:  func(cfg *Config) { cfg.Store = nil },
				wantErr: "session store is required",
			},
			{
				name:    "missing model id",
				mutate:  func(cfg *Config) { cfg.Model.ID = "" },
				wantErr: "model id is required",
			},
			{
				name:    "relative workdir",
				mutate:  func(cfg *Config) { cfg.Workdir = "relative" },
				wantErr: "workdir must be an absolute path",
			},
			{
				name:    "negative max turns",
				mutate:  func(cfg *Config) { cfg.MaxTurns = -1 },
				wantErr: "max turns must not be negative",
			},
			{
				name:    "declared tools without a registry",
				mutate:  func(cfg *Config) { cfg.Agent.Tools = []string{"shell"} },
				wantErr: "the agent declares tools but no registry was provided",
			},
			{
				name: "unknown declared tool",
				mutate: func(cfg *Config) {
					cfg.Agent.Tools = []string{"ghost"}
					cfg.Registry = newTestRegistry(t, &fakeTool{name: "shell"})
				},
				wantErr: `unknown tool "ghost"`,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				cfg := base()
				test.mutate(&cfg)

				_, err := New(cfg)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})

	t.Run("applies defaults", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{})

		require.Equal(t, defaultMaxTurns, engine.maxTurns)
		require.Empty(t, engine.tools.definitions)
		require.Nil(t, engine.tools.executors)
	})

	t.Run("resolves the declared tools", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, &fakeTool{name: "shell"}),
			Agent:    agent.Agent{ID: "coder", Tools: []string{"shell", "shell"}},
			MaxTurns: 5,
		})

		require.Equal(t, 5, engine.maxTurns)
		require.Len(t, engine.tools.definitions, 1)
		require.Equal(t, "shell", engine.tools.definitions[0].Name)
		require.Contains(t, engine.tools.executors, "shell")
	})
}

// TestRequest verifies request building.
func TestRequest(t *testing.T) {
	temperature, topP := 0.5, 0.9

	t.Run("sends the generation settings of the model", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Agent: agent.Agent{SystemPrompt: "be nice"},
			Model: Model{
				ID:          "wire-model",
				MaxTokens:   100,
				Temperature: &temperature,
				TopP:        &topP,
				Thinking:    llm.ThinkingConfig{Level: "high", MaxTokens: 200},
			},
		})

		request := engine.request()

		require.Equal(t, "wire-model", request.Model)
		require.Equal(t, "be nice", request.System)
		require.Equal(t, 100, request.MaxTokens)
		require.Equal(t, &temperature, request.Temperature)
		require.Equal(t, &topP, request.TopP)
		require.Equal(t, &llm.ThinkingConfig{Level: "high", MaxTokens: 200}, request.Thinking)
	})

	t.Run("leaves unset generation settings untouched", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{Model: Model{ID: "wire-model"}})

		request := engine.request()

		require.Zero(t, request.MaxTokens)
		require.Nil(t, request.Temperature)
		require.Nil(t, request.TopP)
		require.Nil(t, request.Thinking)
	})

	t.Run("sends the stored history", func(t *testing.T) {
		engine, store := newTestEngine(t, Config{})
		_, err := store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}},
		}})
		require.NoError(t, err)

		require.Equal(t, store.History(), engine.request().Messages)
	})
}
