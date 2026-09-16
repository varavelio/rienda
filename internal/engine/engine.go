package engine

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tool"
)

// defaultMaxTurns caps the model turns of one run when Config leaves the limit
// unset.
const defaultMaxTurns = 64

// eventBuffer is the capacity of the channel returned by Run. The buffer lets
// a run advance while its consumer catches up.
const eventBuffer = 64

// Model describes the resolved model an agent runs on.
type Model struct {
	// ID is the model identifier sent to the provider.
	ID string

	// MaxTokens is the response token limit configured for the model.
	MaxTokens int

	// Reasoning holds the reasoning defaults configured for the model.
	Reasoning llm.ReasoningConfig
}

// Config configures an Engine.
type Config struct {
	// Client generates the model responses. It is required.
	Client llm.Client

	// Store is the session the engine runs on. It is required and must stay
	// open for the lifetime of the engine.
	Store *session.Store

	// Agent is the definition the engine runs: its system prompt, its tool
	// selection and its generation overrides.
	Agent agent.Agent

	// Model is the resolved model the agent runs on. Its ID is required.
	Model Model

	// Registry holds the tools the agent may use. It is required when the
	// agent declares tools.
	Registry *tool.Registry

	// Workdir is the directory tools run in. It must be absolute when set.
	Workdir string

	// MaxTurns caps the model turns of one run. It defaults to 64.
	MaxTurns int
}

// Engine runs one agent over one session.
//
// An Engine is not safe for concurrent use: runs append to a session store,
// which callers serialize.
type Engine struct {
	client   llm.Client
	store    *session.Store
	agent    agent.Agent
	model    Model
	tools    toolset
	workdir  string
	maxTurns int
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
	case cfg.Workdir != "" && !filepath.IsAbs(cfg.Workdir):
		return nil, errors.New("engine: workdir must be an absolute path")
	case cfg.MaxTurns < 0:
		return nil, errors.New("engine: max turns must not be negative")
	}

	tools, err := resolveTools(cfg.Registry, cfg.Agent.Tools)
	if err != nil {
		return nil, err
	}

	return &Engine{
		client:   cfg.Client,
		store:    cfg.Store,
		agent:    cfg.Agent,
		model:    cfg.Model,
		tools:    tools,
		workdir:  cfg.Workdir,
		maxTurns: cmp.Or(cfg.MaxTurns, defaultMaxTurns),
	}, nil
}

// request builds the provider request of the next turn from the stored history.
func (e *Engine) request() *llm.Request {
	return &llm.Request{
		Model:       e.model.ID,
		System:      e.agent.SystemPrompt,
		Messages:    e.store.History(),
		Tools:       e.tools.definitions,
		MaxTokens:   e.maxTokens(),
		Temperature: e.agent.Temperature,
		TopP:        e.agent.TopP,
		Reasoning:   e.reasoning(),
	}
}

// maxTokens returns the response token limit of the run: the agent override
// wins over the model default.
func (e *Engine) maxTokens() int {
	return cmp.Or(e.agent.MaxTokens, e.model.MaxTokens)
}

// reasoning returns the reasoning configuration of the run: agent overrides
// win over model defaults. It is nil when neither side configures reasoning.
func (e *Engine) reasoning() *llm.ReasoningConfig {
	reasoning := llm.ReasoningConfig{
		Effort:       cmp.Or(e.agent.ReasoningEffort, e.model.Reasoning.Effort),
		BudgetTokens: cmp.Or(e.agent.ReasoningBudgetTokens, e.model.Reasoning.BudgetTokens),
	}
	if reasoning.Effort == "" && reasoning.BudgetTokens == 0 {
		return nil
	}
	return &reasoning
}

// toolset pairs the tool definitions sent to the provider with the tools that
// execute the calls the model requests.
type toolset struct {
	// definitions describes the tools to the model.
	definitions []llm.Tool

	// executors runs the tools by name.
	executors map[string]tool.Tool
}

// resolveTools resolves the tools declared by an agent against a registry.
func resolveTools(registry *tool.Registry, names []string) (toolset, error) {
	if len(names) == 0 {
		return toolset{}, nil
	}
	if registry == nil {
		return toolset{}, errors.New(
			"engine: the agent declares tools but no registry was provided",
		)
	}

	definitions, err := registry.Definitions(names)
	if err != nil {
		return toolset{}, fmt.Errorf("engine: %w", err)
	}

	executors := make(map[string]tool.Tool, len(definitions))
	for _, definition := range definitions {
		executors[definition.Name], _ = registry.Lookup(definition.Name)
	}
	return toolset{definitions: definitions, executors: executors}, nil
}
