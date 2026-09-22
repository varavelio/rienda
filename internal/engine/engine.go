package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
	"github.com/varavelio/rienda/internal/tool"
)

// eventBuffer is the capacity of the channel returned by Run. The buffer lets
// a run advance while its consumer catches up.
const eventBuffer = 64

// Model describes the resolved model an agent runs on. It carries every
// generation setting of the run, taken from the configuration.
type Model struct {
	// ID is the model identifier sent to the provider.
	ID string

	// ContextWindow is the context window of the model in tokens, used to
	// measure how much of it a request consumes. The engine never resolves it;
	// the harness hands it over already resolved.
	ContextWindow int

	// MaxTokens caps the response token limit when greater than zero.
	MaxTokens int

	// Temperature controls sampling randomness when set.
	Temperature *float64

	// TopP controls nucleus sampling when set.
	TopP *float64

	// Thinking holds the extended thinking configuration of the model.
	Thinking llm.ThinkingConfig
}

// Config configures an Engine.
type Config struct {
	// Client generates the model responses. It is required.
	Client llm.Client

	// Store is the session the engine runs on. It is required and must stay
	// open for the lifetime of the engine.
	Store *session.Store

	// Agents lists the definitions the engine may run, one of which is the
	// agent the session starts on. It is required, and every entry needs a
	// non-empty ID.
	Agents []agent.Agent

	// Model is the resolved model the engine runs on. Its ID is required.
	Model Model

	// Registry holds the tools the agent may use. It is required when the
	// agent declares tools.
	Registry *tool.Registry

	// Compactor summarizes the branch when the context grows too large. It is
	// nil when the session compacts nothing, which the manual command and the
	// automatic threshold both honor.
	Compactor Compactor

	// Compaction configures when the engine compacts automatically.
	Compaction Compaction

	// Workdir is the directory tools run in. It must be absolute when set.
	Workdir string
}

// Engine runs one agent over one session. The agent is resolved on every turn
// from the session, so a branch can change the agent it runs without a new
// engine: the definitions the engine was given are the ones it may select.
//
// An Engine is not safe for concurrent use: runs append to a session store,
// which callers serialize.
type Engine struct {
	client     llm.Client
	store      *session.Store
	agents     map[string]agent.Agent
	model      Model
	registry   *tool.Registry
	workdir    string
	compactor  Compactor
	compaction Compaction

	// busy guards the store against concurrent use: a run and a manual
	// compaction both append to it, so only one may be in flight.
	busy atomic.Bool
}

// New validates cfg and builds an Engine.
func New(cfg Config) (*Engine, error) {
	switch {
	case cfg.Client == nil:
		return nil, errors.New("engine: a client is required")
	case cfg.Store == nil:
		return nil, errors.New("engine: a session store is required")
	case cfg.Model.ID == "":
		return nil, errors.New("engine: the model id is required")
	case len(cfg.Agents) == 0:
		return nil, errors.New("engine: at least one agent is required")
	case cfg.Workdir != "" && !filepath.IsAbs(cfg.Workdir):
		return nil, errors.New("engine: workdir must be an absolute path")
	}

	agents := make(map[string]agent.Agent, len(cfg.Agents))
	for _, definition := range cfg.Agents {
		if definition.ID == "" {
			return nil, errors.New("engine: every agent needs an id")
		}
		agents[definition.ID] = definition
	}

	// The tools of the agent the session starts on are resolved eagerly, so a
	// definition that declares a tool the harness does not provide fails the
	// session instead of the run that first selects it.
	initial, found := agents[cfg.Store.ActiveAgent()]
	if !found {
		return nil, fmt.Errorf("engine: unknown agent %q", cfg.Store.ActiveAgent())
	}
	if _, err := resolveTools(cfg.Registry, initial.Tools); err != nil {
		return nil, err
	}

	return &Engine{
		client:     cfg.Client,
		store:      cfg.Store,
		agents:     agents,
		model:      cfg.Model,
		registry:   cfg.Registry,
		workdir:    cfg.Workdir,
		compactor:  cfg.Compactor,
		compaction: cfg.Compaction,
	}, nil
}

// agentOf returns the definition of the agent the session runs at its active
// leaf. The store resolves the selection from the branch, so a session that
// switched agent runs on the definition the branch selected. An agent the
// engine was not given is an error, which leaves the branch with nothing to
// run until another selection, or the header, names an agent that exists.
func (e *Engine) agentOf() (agent.Agent, error) {
	id := e.store.ActiveAgent()
	definition, found := e.agents[id]
	if !found {
		return agent.Agent{}, fmt.Errorf("engine: unknown agent %q", id)
	}
	return definition, nil
}

// KnowsAgent reports whether the engine may run the given agent, which is what
// lets a caller offer only the selections the session can honor.
func (e *Engine) KnowsAgent(id string) bool {
	_, found := e.agents[id]
	return found
}

// enter claims the engine for one run or compaction. It returns false when
// another one is already in flight, which keeps the store free of concurrent
// appends.
func (e *Engine) enter() bool {
	return e.busy.CompareAndSwap(false, true)
}

// leave releases the engine once a run or compaction finished.
func (e *Engine) leave() {
	e.busy.Store(false)
}

// request builds the provider request of the next turn from the stored
// history. The system prompt is rebuilt on every turn so the instructions of
// the project the session runs in stay current.
func (e *Engine) request() (*llm.Request, turnTools, error) {
	definition, err := e.agentOf()
	if err != nil {
		return nil, turnTools{}, err
	}

	system, err := e.systemPrompt(definition)
	if err != nil {
		return nil, turnTools{}, err
	}

	tools, err := resolveTools(e.registry, definition.Tools)
	if err != nil {
		return nil, turnTools{}, err
	}

	return &llm.Request{
		Model:       e.model.ID,
		System:      system,
		Messages:    e.store.History(),
		Tools:       tools.definitions,
		MaxTokens:   e.model.MaxTokens,
		Temperature: e.model.Temperature,
		TopP:        e.model.TopP,
		Thinking:    e.thinking(),
	}, tools, nil
}

// Context reports the estimated context of the request the next turn would
// send, measured against the context window of the model. It covers the system
// prompt, the history of the active branch and the tool definitions, exactly
// the request the automatic compaction measures against its threshold.
func (e *Engine) Context() (tokens.Report, error) {
	request, _, err := e.request()
	if err != nil {
		return tokens.Report{}, err
	}
	return tokens.Measure(tokens.OfRequest(request), e.model.ContextWindow), nil
}

// thinking returns the extended thinking configuration of the run. It is nil
// when the model configures no thinking.
func (e *Engine) thinking() *llm.ThinkingConfig {
	if e.model.Thinking.Level == "" && e.model.Thinking.MaxTokens == 0 {
		return nil
	}
	return &e.model.Thinking
}

// turnTools pairs the tool definitions sent to the provider with the tools
// that execute the calls the model requests. It is rebuilt on every turn from
// the agent the branch selected, so a session that switched agent sends the
// tools of the agent that runs the turn.
type turnTools struct {
	// definitions describes the tools to the model.
	definitions []llm.Tool

	// executors runs the tools by name.
	executors map[string]tool.Tool
}

// resolveTools resolves the tools declared by an agent against a registry.
func resolveTools(registry *tool.Registry, names []string) (turnTools, error) {
	if len(names) == 0 {
		return turnTools{}, nil
	}
	if registry == nil {
		return turnTools{}, errors.New(
			"engine: the agent declares tools but no registry was provided",
		)
	}

	definitions, err := registry.Definitions(names)
	if err != nil {
		return turnTools{}, fmt.Errorf("engine: %w", err)
	}

	executors := make(map[string]tool.Tool, len(definitions))
	for _, definition := range definitions {
		executors[definition.Name], _ = registry.Lookup(definition.Name)
	}
	return turnTools{definitions: definitions, executors: executors}, nil
}
