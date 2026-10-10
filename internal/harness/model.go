package harness

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/varavelio/rienda/internal/credentials"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/provider"
	"github.com/varavelio/rienda/internal/providermod"
)

// modelResolver turns a provider/model reference into the model an engine
// runs and the client that talks to it, reading the provider declarations
// the discovery returned and the credentials of the user. It resolves every
// reference a session may select, so it is the single place where the
// declaration, the credential and the context window of a model are turned
// into something the engine can use.
//
// Every client it hands out carries the identity of the session, which is
// what a provider that routes a call by session reads from the request
// header. The resolver is the one place that knows both the session and the
// client, so the identity is attached once here and no caller has to
// remember it: a run, a summarization or any future procedure reaches the
// provider with the same identity without knowing the header exists.
//
// A client owns an HTTP client with its own connection pool, so resolving a
// reference twice hands back the client built the first time. The cache is
// what makes a session that switches model, or a run that re-plans after a
// compaction, reuse the connections of the models it already talked to.
type modelResolver struct {
	store *credentials.Store

	// providers are the declarations discovery produced, keyed by name.
	providers map[string]providermod.Provider

	// refs are the model references of every provider, sorted once.
	refs []string

	// sessionID is the harness identifier of the session every client of
	// the resolver identifies itself with.
	sessionID string

	// mu guards the cache: a run resolves the model of its branch while the
	// interface resolves the one of the footer, so two goroutines may ask
	// for a model at once.
	mu    sync.Mutex
	cache map[string]resolvedModel
}

// resolvedModel is one entry of the resolver cache: the model an engine runs
// and the client that generates its responses.
type resolvedModel struct {
	model  engine.Model
	client llm.Client
}

// newModelResolver builds the resolver of a session from the discovered
// providers, the credentials of the user and the identifier of the session,
// which every client it hands out identifies itself with.
func newModelResolver(
	providers []providermod.Provider,
	store *credentials.Store,
	sessionID string,
) *modelResolver {
	resolved := make(map[string]providermod.Provider, len(providers))
	refs := make([]string, 0, len(providers))
	for _, item := range providers {
		resolved[item.Name] = item
		refs = append(refs, item.Refs()...)
	}
	slices.Sort(refs)
	return &modelResolver{
		store:     store,
		providers: resolved,
		refs:      refs,
		sessionID: sessionID,
		cache:     make(map[string]resolvedModel),
	}
}

// Resolve returns the model a reference names and a client that talks to it.
// A reference the discovered providers do not hold is an error; a provider
// whose credential is missing first re-reads the credential file once, which
// honors a key stored while Rienda was running, and then fails with the
// documented error.
func (r *modelResolver) Resolve(ref string) (engine.Model, llm.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, found := r.cache[ref]; found {
		return cached.model, cached.client, nil
	}

	resolved, err := r.resolveLocked(ref)
	if err != nil {
		return engine.Model{}, nil, err
	}
	r.cache[ref] = resolved
	return resolved.model, resolved.client, nil
}

// resolveLocked resolves one reference, assuming the resolver lock is held.
func (r *modelResolver) resolveLocked(ref string) (resolvedModel, error) {
	providerName, modelID, err := splitReference(ref)
	if err != nil {
		return resolvedModel{}, err
	}
	item, found := r.providers[providerName]
	if !found {
		return resolvedModel{}, fmt.Errorf("unknown provider %q", providerName)
	}
	var model *providermod.ModelDeclaration
	for i := range item.Decl.Models {
		if item.Decl.Models[i].ID == modelID {
			model = &item.Decl.Models[i]
			break
		}
	}
	if model == nil {
		return resolvedModel{}, fmt.Errorf("unknown model %q in provider %q", modelID, providerName)
	}

	decl := item.Decl
	apiKey := r.store.APIKey(providerName)
	if decl.Auth == providermod.AuthAPIKey && apiKey == "" {
		if err := r.store.Reload(); err != nil {
			return resolvedModel{}, fmt.Errorf("provider %q: %w", providerName, err)
		}
		apiKey = r.store.APIKey(providerName)
		if apiKey == "" {
			return resolvedModel{}, errUnauthenticated(providerName)
		}
	}

	connection, protocol := r.connection(decl, model)
	connectionConfig := provider.Config{
		APIKey:            apiKey,
		BaseURL:           connection.BaseURL,
		ExtraHeaders:      connection.Headers,
		SessionHeaderName: connection.SessionHeader,
	}
	client, err := provider.New(protocol, connectionConfig)
	if err != nil {
		return resolvedModel{}, fmt.Errorf("provider %q: %w", providerName, err)
	}
	client = sessionClient{Client: client, sessionID: r.sessionID}

	engineModel := engine.Model{
		ID:            model.ID,
		MaxTokens:     model.MaxTokens,
		Temperature:   model.Temperature,
		TopP:          model.TopP,
		Thinking:      llm.ThinkingConfig{},
		ContextWindow: model.ContextWindow,
	}
	if model.MaxOutputTokens > 0 {
		engineModel.MaxTokens = model.MaxOutputTokens
	}
	return resolvedModel{model: engineModel, client: client}, nil
}

// connection merges the per-model overrides of a model onto the declaration
// of its provider: the resolved connection the model's requests use.
func (r *modelResolver) connection(
	decl providermod.Declaration,
	model *providermod.ModelDeclaration,
) (providermod.ModelDeclaration, provider.Protocol) {
	merged := *model
	if merged.Protocol == "" {
		merged.Protocol = decl.Protocol
	}
	if merged.BaseURL == "" {
		merged.BaseURL = decl.BaseURL
	}
	if merged.SessionHeader == "" {
		merged.SessionHeader = decl.SessionHeader
	}
	if len(decl.Headers) > 0 || len(merged.Headers) > 0 {
		merged.Headers = mergeHeaders(decl.Headers, model.Headers)
	}
	return merged, merged.Protocol
}

// errUnauthenticated reports a provider whose credential the run cannot
// resolve, naming the provider and the screen that fixes it. The wording is
// the same for every provider the registration holds, so a front end matches
// on it without knowing the name.
func errUnauthenticated(name string) error {
	return fmt.Errorf("provider %s is not authenticated; run rienda auth", name)
}

// mergeHeaders merges headers per key: the model's keys replace the
// provider's same-named ones and the rest survive.
func mergeHeaders(provider, model map[string]string) map[string]string {
	merged := make(map[string]string, len(provider)+len(model))
	maps.Copy(merged, provider)
	maps.Copy(merged, model)
	return merged
}

// splitReference cuts a model reference into its two halves.
func splitReference(ref string) (string, string, error) {
	providerName, modelID, found := strings.Cut(strings.TrimSpace(ref), "/")
	providerName, modelID = strings.TrimSpace(providerName), strings.TrimSpace(modelID)
	if !found || providerName == "" || modelID == "" {
		return "", "", fmt.Errorf("model reference %q must have the form provider/model", ref)
	}
	return providerName, modelID, nil
}

// Refs returns every reference the discovered providers hold, which is the
// roster a front end offers to switch the model of a session.
func (r *modelResolver) Refs() []string {
	return slices.Clone(r.refs)
}

// Info returns how a model reference is described beyond the reference
// itself: the wire identifier the provider receives. The thinking level is
// decided per request through the branch thinking, not here. A reference the
// providers do not hold describes nothing. It reads the declarations alone,
// which are immutable, so a caller that only displays the description never
// builds a provider client.
func (r *modelResolver) Info(ref string) engine.ModelInfo {
	providerName, modelID, err := splitReference(ref)
	if err != nil {
		return engine.ModelInfo{}
	}
	provider, found := r.providers[providerName]
	if !found {
		return engine.ModelInfo{}
	}
	for _, model := range provider.Decl.Models {
		if model.ID == modelID {
			return engine.ModelInfo{ID: model.ID}
		}
	}
	return engine.ModelInfo{}
}

// ThinkingLevels returns the thinking levels the model reference declares, in
// the declaration order of its provider module. It is what the picker of the
// interface offers; a reference the providers do not hold declares none.
func (r *modelResolver) ThinkingLevels(ref string) []string {
	providerName, modelID, err := splitReference(ref)
	if err != nil {
		return nil
	}
	item, found := r.providers[providerName]
	if !found {
		return nil
	}
	for _, model := range item.Decl.Models {
		if model.ID != modelID {
			continue
		}
		levels := make([]string, 0, len(model.ThinkingModes))
		for _, mode := range model.ThinkingModes {
			levels = append(levels, mode.Level)
		}
		return levels
	}
	return nil
}

// ThinkingBudget returns the budget a level of a model carries for the
// budget-based protocols, zero when the level carried none or when the
// reference names nothing the providers hold.
func (r *modelResolver) ThinkingMode(ref, level string) (int, bool) {
	providerName, modelID, err := splitReference(ref)
	if err != nil {
		return 0, false
	}
	item, found := r.providers[providerName]
	if !found {
		return 0, false
	}
	for _, model := range item.Decl.Models {
		if model.ID != modelID {
			continue
		}
		for _, mode := range model.ThinkingModes {
			if mode.Level == level {
				return mode.MaxTokens, true
			}
		}
	}
	return 0, false
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

// Stream forwards the call with the identity of the session attached. The
// error travels unchanged, so the caller keeps classifying it exactly as it
// would without the decorator.
func (c sessionClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	//nolint:wrapcheck // the decorator forwards the error for the caller to classify.
	return c.Client.Stream(llm.WithSessionID(ctx, c.sessionID), req)
}
