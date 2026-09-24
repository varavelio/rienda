package harness

import (
	"context"
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
// Every client it hands out carries the identity of the session, which is what
// a provider that routes a call by session reads from the request header. The
// resolver is the one place that knows both the session and the client, so the
// identity is attached once here and no caller has to remember it: a run, a
// summarization or any future procedure reaches the provider with the same
// identity without knowing the header exists.
//
// A client owns an HTTP client with its own connection pool, so resolving a
// reference twice hands back the client built the first time. The cache is what
// makes a session that switches model, or a run that re-plans after a
// compaction, reuse the connections of the models it already talked to.
type modelResolver struct {
	cfg *config.Config

	// sessionID is the harness identifier of the session every client of the
	// resolver identifies itself with.
	sessionID string

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
	client = sessionClient{Client: client, sessionID: r.sessionID}

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

// Info returns how a model reference is described beyond the reference itself:
// the wire identifier the provider receives and the extended thinking level the
// configuration declares. A reference the configuration does not hold describes
// nothing. It reads the configuration alone, which is immutable, so a caller
// that only displays the description never builds a provider client.
func (r *modelResolver) Info(ref string) engine.ModelInfo {
	resolved, err := r.cfg.Resolve(ref)
	if err != nil {
		return engine.ModelInfo{}
	}
	return engine.ModelInfo{ID: resolved.ModelID, ThinkingLevel: resolved.ThinkingLevel}
}

// newModelResolver builds the resolver of a session from the configuration of
// the user and the identifier of the session, which every client it hands out
// identifies itself with: every reference a session selects is resolved
// against it.
func newModelResolver(cfg *config.Config, sessionID string) *modelResolver {
	return &modelResolver{
		cfg:       cfg,
		sessionID: sessionID,
		cache:     make(map[string]resolvedModel),
	}
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

// sessionClient decorates an llm.Client with the identity of the session it
// belongs to, so every call identifies itself with the harness session ID a
// provider that routes by session reads from the request header.
type sessionClient struct {
	llm.Client
	sessionID string
}

// Generate forwards the call with the identity of the session attached. The
// error travels unchanged, so the caller keeps classifying it exactly as it
// would without the decorator.
func (c sessionClient) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	//nolint:wrapcheck // the decorator forwards the error for the caller to classify.
	return c.Client.Generate(llm.WithSessionID(ctx, c.sessionID), req)
}

// Stream forwards the call with the identity of the session attached. The error
// travels unchanged, so the caller keeps classifying it exactly as it would
// without the decorator.
func (c sessionClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	//nolint:wrapcheck // the decorator forwards the error for the caller to classify.
	return c.Client.Stream(llm.WithSessionID(ctx, c.sessionID), req)
}
