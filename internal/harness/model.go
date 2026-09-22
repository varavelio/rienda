package harness

import (
	"fmt"
	"sync"

	"github.com/varavelio/rienda/internal/catalog"
	"github.com/varavelio/rienda/internal/config"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/provider"
)

// modelResolver turns a provider/model reference into the model an engine runs
// and the client that talks to it, reading the configuration of the user for
// both. It resolves every reference a session may select, so it is the single
// place where the settings, the credentials and the context window of a model
// are turned into something the engine can use.
//
// A client owns an HTTP client with its own connection pool, so resolving a
// reference twice hands back the client built the first time. The cache is what
// makes a session that switches model, or a run that re-plans after a
// compaction, reuse the connections of the models it already talked to.
type modelResolver struct {
	cfg *config.Config

	// mu guards the cache: a run resolves the model of its branch while the
	// interface resolves the one of the footer, so two goroutines may ask for
	// a model at once.
	mu    sync.Mutex
	cache map[string]resolvedModel
}

// resolvedModel is one entry of the resolver cache: the model an engine runs
// and the client that generates its responses.
type resolvedModel struct {
	model  engine.Model
	client llm.Client
}

// Resolve returns the model a reference names, with its context window already
// resolved from the three sources of the project, and a client that talks to
// it. A reference the configuration does not hold is an error.
func (r *modelResolver) Resolve(ref string) (engine.Model, llm.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, found := r.cache[ref]; found {
		return cached.model, cached.client, nil
	}

	resolved, err := r.cfg.Resolve(ref)
	if err != nil {
		return engine.Model{}, nil, fmt.Errorf("harness: model %q: %w", ref, err)
	}
	client, err := provider.New(resolved.Protocol, resolved.ProviderConfig)
	if err != nil {
		return engine.Model{}, nil, fmt.Errorf("harness: build provider client: %w", err)
	}

	model := engineModel(resolved)
	model.ContextWindow = resolveContextWindow(resolved)
	r.cache[ref] = resolvedModel{model: model, client: client}
	return model, client, nil
}

// Refs returns every reference the configuration holds, which is the roster a
// front end offers to switch the model of a session.
func (r *modelResolver) Refs() []string {
	return r.cfg.ModelRefs()
}

// newModelResolver builds the resolver of a session from the configuration of
// the user: every reference a session selects is resolved against it.
func newModelResolver(cfg *config.Config) engine.Resolver {
	return &modelResolver{cfg: cfg, cache: make(map[string]resolvedModel)}
}

// engineModel translates the resolved model settings into engine form.
func engineModel(resolved config.Resolved) engine.Model {
	return engine.Model{
		ID:          resolved.ModelID,
		MaxTokens:   resolved.MaxTokens,
		Temperature: resolved.Temperature,
		TopP:        resolved.TopP,
		Thinking: llm.ThinkingConfig{
			Level:     resolved.ThinkingLevel,
			MaxTokens: resolved.ThinkingMaxTokens,
		},
	}
}

// resolveContextWindow returns the context window of a resolved model: the
// value the configuration declares, the value the catalog knows, or the
// conservative fallback. The catalog is best effort, so a home directory that
// cannot be located falls back instead of failing the session.
func resolveContextWindow(resolved config.Resolved) int {
	facts, err := catalog.New(catalog.Options{})
	if err != nil {
		if resolved.ContextWindow > 0 {
			return resolved.ContextWindow
		}
		return catalog.FallbackWindow
	}
	return facts.Window(resolved.ModelID, resolved.ContextWindow)
}
