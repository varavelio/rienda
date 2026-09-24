package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
	"github.com/varavelio/rienda/internal/tool"
)

// fakeClient is a scripted llm.Client: every Stream call consumes the next
// script and the received requests are recorded for assertions.
type fakeClient struct {
	requests []*llm.Request
	scripts  []script
}

// Stream returns the next scripted response.
func (c *fakeClient) Stream(ctx context.Context, request *llm.Request) (llm.Stream, error) {
	c.requests = append(c.requests, request)

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

// testResolver is the scripted engine.Resolver: every reference resolves to a
// model of the map, or to a single default model when the map does not name
// one, and every reference shares the client the resolver was built with.
type testResolver struct {
	client llm.Client
	models map[string]Model
}

// Resolve returns the scripted model of a reference and the shared client.
func (r *testResolver) Resolve(ref string) (Model, llm.Client, error) {
	if model, found := r.models[ref]; found {
		return model, r.client, nil
	}
	if len(r.models) == 0 {
		return Model{ID: ref}, r.client, nil
	}
	return Model{}, nil, fmt.Errorf("testResolver: unknown model %q", ref)
}

// Refs returns the references the scripted resolver holds, in the order of the
// map, which a test asserts on as a set.
func (r *testResolver) Refs() []string {
	refs := make([]string, 0, len(r.models))
	for ref := range r.models {
		refs = append(refs, ref)
	}
	return refs
}

// newTestResolver builds a resolver serving one model for every reference. The
// model it is given names the wire identifier the tests assert on.
func newTestResolver(client llm.Client, model Model) Resolver {
	if client == nil {
		client = &fakeClient{}
	}
	if model.ID == "" {
		model.ID = "test-model"
	}
	return &testResolver{client: client, models: map[string]Model{"test/model": model}}
}

// newTestEngine builds an engine backed by a real session store in a
// temporary directory and returns both.
func newTestEngine(t *testing.T, cfg Config) (*Engine, *session.Store) {
	t.Helper()

	store := newTestStore(t)
	cfg.Store = store
	if cfg.Resolver == nil {
		cfg.Resolver = newTestResolver(&fakeClient{}, Model{ID: "test-model"})
	}
	if len(cfg.Agents) == 0 {
		cfg.Agents = []agent.Agent{{ID: "coder"}}
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
		return Config{
			Store:    store,
			Agents:   []agent.Agent{{ID: "coder"}},
			Resolver: newTestResolver(&fakeClient{}, Model{ID: "test-model"}),
		}
	}

	t.Run("rejects invalid configurations", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(*Config)
			wantErr string
		}{
			{
				name:    "missing store",
				mutate:  func(cfg *Config) { cfg.Store = nil },
				wantErr: "session store is required",
			},
			{
				name:    "missing resolver",
				mutate:  func(cfg *Config) { cfg.Resolver = nil },
				wantErr: "model resolver is required",
			},
			{
				name:    "relative workdir",
				mutate:  func(cfg *Config) { cfg.Workdir = "relative" },
				wantErr: "workdir must be an absolute path",
			},
			{
				name:    "no agents",
				mutate:  func(cfg *Config) { cfg.Agents = nil },
				wantErr: "at least one agent is required",
			},
			{
				name:    "an agent without an id",
				mutate:  func(cfg *Config) { cfg.Agents = []agent.Agent{{}} },
				wantErr: "every agent needs an id",
			},
			{
				name: "declared tools without a registry",
				mutate: func(cfg *Config) {
					cfg.Agents = []agent.Agent{{ID: "coder", Tools: []string{"shell"}}}
				},
				wantErr: "the agent declares tools but no registry was provided",
			},
			{
				name: "unknown declared tool",
				mutate: func(cfg *Config) {
					cfg.Agents = []agent.Agent{{ID: "coder", Tools: []string{"ghost"}}}
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

	t.Run("resolves the agent the session runs", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{})

		require.Contains(t, engine.agents, "coder")
		require.True(t, engine.KnowsAgent("coder"))
		require.False(t, engine.KnowsAgent("ghost"))
	})

	t.Run("opens a branch whose agent is gone", func(t *testing.T) {
		// A branch names the agent of its header, which the roster no longer
		// holds: the session still opens so a front end reads the
		// conversation, and reports that it holds nothing to run.
		engine, store := newTestEngine(t, Config{
			Agents: []agent.Agent{{ID: "reviewer"}},
		})
		require.NoError(t, store.SetAgent(t.Context(), "ghost"))

		refusal, refused := engine.Runnable()

		require.True(t, refused)
		require.Equal(t, RunnableUnknownAgent, refusal.Kind)
		require.Equal(t, "ghost", refusal.ID)
	})

	t.Run("opens a branch whose model is gone", func(t *testing.T) {
		engine, store := newTestEngine(t, Config{
			Resolver: &testResolver{models: map[string]Model{"other/model": {}}},
		})
		require.NoError(t, store.SetModel(t.Context(), "test/model"))

		refusal, refused := engine.Runnable()

		require.True(t, refused)
		require.Equal(t, RunnableUnknownModel, refusal.Kind)
		require.Equal(t, "test/model", refusal.ID)
	})

	t.Run("reports a runnable branch", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{})

		refusal, refused := engine.Runnable()

		require.False(t, refused)
		require.Equal(t, RunnableRefusal{}, refusal)
	})

	t.Run("resolves the tools of the agent", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Registry: newTestRegistry(t, &fakeTool{name: "shell"}),
			Agents:   []agent.Agent{{ID: "coder", Tools: []string{"shell", "shell"}}},
		})

		plan, err := engine.plan()
		require.NoError(t, err)
		require.Len(t, plan.request.Tools, 1)
		require.Equal(t, "shell", plan.request.Tools[0].Name)
		require.Contains(t, plan.tools.executors, "shell")
	})
}

// TestRequest verifies request building.
func TestRequest(t *testing.T) {
	temperature, topP := 0.5, 0.9

	t.Run("sends the generation settings of the model", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Agents: []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Resolver: newTestResolver(&fakeClient{}, Model{
				ID:          "wire-model",
				MaxTokens:   100,
				Temperature: &temperature,
				TopP:        &topP,
				Thinking:    llm.ThinkingConfig{Level: "high", MaxTokens: 200},
			}),
		})

		plan, err := engine.plan()
		require.NoError(t, err)

		require.Equal(t, "wire-model", plan.request.Model)
		require.Equal(t, "be nice", plan.request.System)
		require.Equal(t, 100, plan.request.MaxTokens)
		require.Equal(t, &temperature, plan.request.Temperature)
		require.Equal(t, &topP, plan.request.TopP)
		require.Equal(t, &llm.ThinkingConfig{Level: "high", MaxTokens: 200}, plan.request.Thinking)
	})

	t.Run("leaves unset generation settings untouched", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Resolver: newTestResolver(&fakeClient{}, Model{ID: "wire-model"}),
		})

		plan, err := engine.plan()
		require.NoError(t, err)

		require.Zero(t, plan.request.MaxTokens)
		require.Nil(t, plan.request.Temperature)
		require.Nil(t, plan.request.TopP)
		require.Nil(t, plan.request.Thinking)
	})

	t.Run("sends the stored history", func(t *testing.T) {
		engine, store := newTestEngine(t, Config{})
		_, err := store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}},
		}})
		require.NoError(t, err)

		plan, err := engine.plan()
		require.NoError(t, err)
		require.Equal(t, store.History(), plan.request.Messages)
	})

	t.Run("includes the project instructions in the system prompt", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs.")
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		plan, err := engine.plan()
		require.NoError(t, err)
		require.Contains(t, plan.request.System, "be nice")
		require.Contains(t, plan.request.System, "Use tabs.")
	})
}

// TestRequestSkills verifies that the skills of the workspace reach the request
// of a turn.
func TestRequestSkills(t *testing.T) {
	t.Run("publishes the catalog of the workspace", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		plan, err := engine.plan()

		require.NoError(t, err)
		require.Contains(t, plan.request.System, "<name>pdfs</name>")
		require.Contains(
			t,
			plan.request.System,
			"<location>./.agents/skills/pdfs/SKILL.md</location>",
		)
		require.Empty(t, plan.diagnostics)
	})

	t.Run("publishes the catalog for an agent that declares no tool", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		plan, err := engine.plan()

		require.NoError(t, err)
		require.Empty(t, plan.request.Tools)
		require.Contains(t, plan.request.System, "<name>pdfs</name>")
		require.Contains(t, plan.request.System, "cannot read a skill")
	})

	t.Run("changes nothing when the workspace declares no skill", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs.")
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		plan, err := engine.plan()

		require.NoError(t, err)
		require.True(t, strings.HasPrefix(plan.request.System, "be nice"+sectionSeparator))
		require.Contains(t, plan.request.System, projectSection)
		require.Contains(t, plan.request.System, "Use tabs.")
		require.NotContains(t, plan.request.System, "available_skills")
		require.Empty(t, plan.diagnostics)
	})

	t.Run("keeps the catalog current across runs", func(t *testing.T) {
		dir := t.TempDir()
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder"}},
			Workdir: dir,
		})

		before, err := engine.plan()
		require.NoError(t, err)
		require.NotContains(t, before.request.System, "available_skills")

		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))

		after, err := engine.plan()
		require.NoError(t, err)
		require.Contains(t, after.request.System, "<name>pdfs</name>")
	})

	t.Run("measures the catalog as part of the request", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
			Resolver: newTestResolver(&fakeClient{}, Model{
				ID:            "test-model",
				ContextWindow: 100000,
			}),
		})

		withSkills, err := engine.Context()
		require.NoError(t, err)

		require.NoError(t, os.RemoveAll(filepath.Join(dir, ".agents")))

		withoutSkills, err := engine.Context()
		require.NoError(t, err)

		require.Greater(t, withSkills.Used, withoutSkills.Used,
			"the catalog is counted like any other system prompt content")
	})
}

// TestContext verifies the context read the interface reports.
func TestContext(t *testing.T) {
	t.Run("measures the system prompt, the tools and the history", func(t *testing.T) {
		engine, store := newTestEngine(t, Config{
			Agents: []agent.Agent{
				{ID: "coder", SystemPrompt: "be brief", Tools: []string{"shell"}},
			},
			Registry: newTestRegistry(t, &fakeTool{name: "shell"}),
			Resolver: newTestResolver(&fakeClient{}, Model{ID: "test-model", ContextWindow: 200}),
		})
		_, err := store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello world"}},
		}})
		require.NoError(t, err)

		report, err := engine.Context()

		require.NoError(t, err)
		require.Equal(t, 200, report.Window)
		require.NotZero(t, report.Used)
		require.Greater(t, report.Percent, 0.0)

		plan, err := engine.plan()
		require.NoError(t, err)
		require.Equal(t, tokens.OfRequest(plan.request), report.Used)
		require.NotEmpty(
			t,
			plan.request.Tools,
			"the measured request declares the tools of the agent",
		)
	})

	t.Run("reports a window that is not usable", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Resolver: newTestResolver(&fakeClient{}, Model{ID: "test-model"}),
		})

		report, err := engine.Context()

		require.NoError(t, err)
		require.Zero(t, report.Window)
		require.Zero(t, report.Percent)
	})
}
