package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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

// ModelInfo describes a model a session may run, as a front end names it beyond
// the provider/model reference that addresses it: the wire identifier the
// provider receives and the extended thinking level the configuration declares.
type ModelInfo struct {
	// ID is the model identifier sent on the wire, which defaults to the alias
	// the reference names when the configuration declares none.
	ID string

	// ThinkingLevel is the extended thinking level the configuration declares,
	// empty when the model uses its provider default.
	ThinkingLevel string
}

// Resolver resolves a provider/model reference into the model the engine runs
// and the client that talks to it. The harness provides the production
// implementation over internal/config, which reads the credentials of the user
// and caches one client per reference; the engine tests inject a scripted one.
type Resolver interface {
	// Resolve returns the model a reference names and a client that generates
	// its responses. A reference the resolver does not know is an error.
	Resolve(ref string) (Model, llm.Client, error)

	// Refs returns every reference the resolver accepts, which is the roster a
	// front end offers to switch the model of a session.
	Refs() []string
}

// Config configures an Engine.
type Config struct {
	// Store is the session the engine runs on. It is required and must stay
	// open for the lifetime of the engine.
	Store *session.Store

	// Agents lists the definitions the engine may run, one of which is the
	// agent the session starts on. It is required, and every entry needs a
	// non-empty ID.
	Agents []agent.Agent

	// Resolver resolves the model reference of the branch into the model and
	// the client of the turn. It is required, and it is called for the model
	// the session starts on and for every model a branch selects.
	Resolver Resolver

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

// Engine runs one agent over one session. The agent and the model are resolved
// on every turn from the session, so a branch can change either without a new
// engine: the definitions the engine was given are the agents it may select,
// and the resolver is what turns the model reference of the branch into a model
// and a client.
//
// The engine holds no resolved model of its own, so it has no mutable state to
// race over: everything a turn needs is rebuilt from the branch it runs. The
// resolver it was given is the only collaborator that may hold a cache, and it
// is the caller's business to make that cache safe for concurrent use.
//
// An Engine is not safe for concurrent use: it drives one conversation at a
// time, which callers serialize.
type Engine struct {
	store      *session.Store
	agents     map[string]agent.Agent
	resolver   Resolver
	registry   *tool.Registry
	workdir    string
	compactor  Compactor
	compaction Compaction

	// busy admits one run at a time: a run and a manual compaction both
	// append to the store, so only one of them may be in flight.
	busy atomic.Bool
}

// New validates cfg and builds an Engine.
func New(cfg Config) (*Engine, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("engine: a session store is required")
	case cfg.Resolver == nil:
		return nil, errors.New("engine: a model resolver is required")
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

	// The model the session starts on is resolved eagerly when the
	// configuration holds it, so a broken provider fails the session instead
	// of the run that first sends it. A model the configuration no longer
	// holds does not fail the session: the branch holds nothing to run until
	// another selection names a model that exists, which is what lets a front
	// end read a conversation whose model disappeared (see Runnable).
	if ref := cfg.Store.ActiveModel(); slices.Contains(cfg.Resolver.Refs(), ref) {
		if _, _, err := cfg.Resolver.Resolve(ref); err != nil {
			return nil, fmt.Errorf("engine: resolve model: %w", err)
		}
	}

	// The definition the branch opens on is resolved eagerly only when the
	// roster still holds it: a definition that declares a tool the harness
	// does not provide fails the session instead of the run that first
	// selects it. A branch whose agent is gone, because the definition was
	// renamed or removed, still opens: it holds nothing to run until another
	// selection names an agent that exists, which is what lets a front end
	// read a conversation whose agent disappeared.
	if initial, found := agents[cfg.Store.ActiveAgent()]; found {
		if _, err := resolveTools(cfg.Registry, initial.Tools); err != nil {
			return nil, err
		}
	}

	return &Engine{
		store:      cfg.Store,
		agents:     agents,
		resolver:   cfg.Resolver,
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

// Models returns the provider/model references the resolver accepts, which is
// the roster a caller offers to switch the model of a session.
func (e *Engine) Models() []string {
	refs := e.resolver.Refs()
	slices.Sort(refs)
	return refs
}

// KnowsModel reports whether the resolver accepts the given reference, which is
// what lets a caller offer only the selections the session can honor.
func (e *Engine) KnowsModel(ref string) bool {
	return slices.Contains(e.resolver.Refs(), ref)
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

// turnPlan is everything one turn of a run needs, resolved from the branch the
// session runs at the moment the turn starts: the agent that writes it, the
// model and the client that generate it, and the request that carries them.
//
// It is built once per turn and passed to the code that runs it, so the agent,
// the model and the tools of a turn can never disagree with each other, and the
// engine keeps no resolved state of its own between turns.
type turnPlan struct {
	// agent is the definition of the agent the branch runs.
	agent agent.Agent

	// model is the resolved model of the branch.
	model Model

	// client generates the response of the turn.
	client llm.Client

	// request is the provider request of the turn.
	request *llm.Request

	// tools pairs the tool definitions sent to the provider with the tools
	// that execute the calls the model requests.
	tools turnTools

	// diagnostics lists the non-fatal problems found while discovering the
	// skills of the workspace. The run that opens reports them; a context
	// measurement ignores them, so measuring a branch never reports anything.
	diagnostics []string
}

// withSystem returns the plan of a turn of a run, sending the system prompt the
// run read when it opened instead of the one this plan read from the workspace,
// and dropping the diagnostics, which only the turn that opens a run reports.
// The run owns the system prompt of its turns because a skill or an instruction
// file has to be able to change without the model seeing a different prompt in
// the middle of the work it is doing.
func (p turnPlan) withSystem(system string) turnPlan {
	request := *p.request
	request.System = system
	p.request = &request
	p.diagnostics = nil
	return p
}

// plan builds the plan of the next turn from the stored history: the agent, the
// model and the client the branch runs, and its request. The system prompt is
// read from the workspace on every turn, so a plan always reflects the workspace
// as it stands; a run pins the prompt of its own turns through withSystem, and a
// context measurement reads it and reports nothing.
func (e *Engine) plan() (turnPlan, error) {
	definition, err := e.agentOf()
	if err != nil {
		return turnPlan{}, err
	}

	model, client, err := e.resolver.Resolve(e.store.ActiveModel())
	if err != nil {
		return turnPlan{}, fmt.Errorf("engine: resolve model: %w", err)
	}

	// The system prompt is read from the workspace on every turn, so a skill or
	// an instruction file created, edited or removed applies without anything
	// having to be invalidated for that to be true.
	system, diagnostics, err := e.systemPrompt(definition)
	if err != nil {
		return turnPlan{}, err
	}

	tools, err := resolveTools(e.registry, definition.Tools)
	if err != nil {
		return turnPlan{}, err
	}

	return turnPlan{
		agent:  definition,
		model:  model,
		client: client,
		request: &llm.Request{
			Model:       model.ID,
			System:      system,
			Messages:    e.store.History(),
			Tools:       tools.definitions,
			MaxTokens:   model.MaxTokens,
			Temperature: model.Temperature,
			TopP:        model.TopP,
			Thinking:    thinking(model),
		},
		tools:       tools,
		diagnostics: diagnostics,
	}, nil
}

// Context reports the estimated context of the request the next turn would
// send, measured against the context window of the model the branch runs. It
// covers the system prompt, the history of the active branch and the tool
// definitions, exactly the request the automatic compaction measures against
// its threshold.
func (e *Engine) Context() (tokens.Report, error) {
	plan, err := e.plan()
	if err != nil {
		return tokens.Report{}, err
	}
	return tokens.Measure(tokens.OfRequest(plan.request), plan.model.ContextWindow), nil
}

// emitContext reports the estimated context of the request the next turn would
// send. The engine emits it after every change to the stored conversation, so a
// front end shows the live figure of a branch while a run is in flight instead
// of only when it ends. A measurement that cannot be built is skipped: the
// figure is a courtesy and never a reason to fail a run.
func (e *Engine) emitContext(events chan<- Event) {
	report, err := e.Context()
	if err != nil {
		return
	}
	emit(events, Event{Type: EventContext, Context: contextFrom(report)})
}

// thinking returns the extended thinking configuration of a model. It is nil
// when the model configures no thinking.
func thinking(model Model) *llm.ThinkingConfig {
	if model.Thinking.Level == "" && model.Thinking.MaxTokens == 0 {
		return nil
	}
	return &model.Thinking
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
