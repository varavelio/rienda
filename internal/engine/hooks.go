package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// HookResolver resolves the hook extensions an agent declares into the hooks
// of the turn. The harness provides the production implementation over
// internal/hook; the engine tests inject a stub.
type HookResolver interface {
	// Resolve returns the hooks the named extensions install, in order. An
	// unknown name is an error naming the available hooks.
	Resolve(names []string) (Hooks, error)
}

// Hooks runs the hook extensions of a turn at the fixed points of a run. No
// method reports a hook failure: a hook never fails a run, so a throw or an
// unusable return value is reported as a notice and the engine proceeds with
// the "no opinion" result.
type Hooks interface {
	// BeforeRun runs once per run, after the run-start event.
	BeforeRun(ctx context.Context, ev BeforeRunHook)

	// AfterRun runs once per run, after the last turn, before the run-end
	// event reaches the consumer.
	AfterRun(ctx context.Context, ev AfterRunHook)

	// BeforeModelRequest runs every turn, before the provider call.
	BeforeModelRequest(ctx context.Context, ev BeforeModelRequestHook) BeforeModelRequestResult

	// AfterModelResponse runs every turn, after the response completes.
	AfterModelResponse(ctx context.Context, ev AfterModelResponseHook) AfterModelResponseResult

	// BeforeToolExecute runs before every tool call the model requested.
	BeforeToolExecute(ctx context.Context, ev BeforeToolExecuteHook) BeforeToolExecuteResult

	// AfterToolExecute runs after every tool call the model requested.
	AfterToolExecute(ctx context.Context, ev AfterToolExecuteHook) AfterToolExecuteResult
}

// BeforeRunHook opens a run, after the run-start event.
type BeforeRunHook struct {
	// SessionID is the session that owns the run.
	SessionID string

	// AgentID is the agent the run starts on.
	AgentID string

	// ModelID is the provider model identifier the provider receives.
	ModelID string
}

// AfterRunHook closes a run, after the last turn of the run.
type AfterRunHook struct {
	// Reason explains why the run ended.
	Reason EndReason
}

// HookMessage is one message of a request, flattened for hooks.
type HookMessage struct {
	// Role is the author of the message.
	Role string

	// Text is the flattened text of the message.
	Text string
}

// BeforeModelRequestHook describes the request the turn is about to send.
type BeforeModelRequestHook struct {
	// System is the system prompt of the turn.
	System string

	// Model is the provider model identifier the provider receives.
	Model string

	// Messages is the read-only conversation the turn sends.
	Messages []HookMessage
}

// BeforeModelRequestResult carries what the hook writes. Set distinguishes a
// returned empty system prompt from no opinion.
type BeforeModelRequestResult struct {
	// System replaces the system prompt of the turn.
	System string

	// Set reports the hook returned a system prompt.
	Set bool
}

// HookToolCall is one tool call the model requested, as a hook sees it.
type HookToolCall struct {
	// ID is the provider-assigned identifier of the call.
	ID string

	// Name is the name of the invoked tool.
	Name string

	// Arguments holds the invocation arguments as a JSON object.
	Arguments json.RawMessage
}

// AfterModelResponseHook describes the response the model just produced.
type AfterModelResponseHook struct {
	// Text is the concatenated text of the response.
	Text string

	// Thinking is the concatenated reasoning of the response.
	Thinking string

	// ToolCalls lists the calls the model requested, read-only.
	ToolCalls []HookToolCall
}

// AfterModelResponseResult carries what the hook rewrites. Each Set flag
// distinguishes a returned empty string from no opinion.
type AfterModelResponseResult struct {
	// Text replaces the assistant prose.
	Text string

	// TextSet reports the hook returned a text.
	TextSet bool

	// Thinking replaces the assistant reasoning.
	Thinking string

	// ThinkingSet reports the hook returned a thinking.
	ThinkingSet bool
}

// BeforeToolExecuteHook describes one tool call before it runs.
type BeforeToolExecuteHook struct {
	// ID is the provider-assigned identifier of the call.
	ID string

	// Name is the name of the invoked tool.
	Name string

	// Arguments holds the invocation arguments as a JSON object.
	Arguments json.RawMessage
}

// BeforeToolExecuteResult carries what the hook decides. A nil Allow means no
// opinion; a non-nil false refuses the call.
type BeforeToolExecuteResult struct {
	// Allow refuses the call when pointing at false.
	Allow *bool

	// Reason explains the refusal to the model.
	Reason string

	// Arguments replaces the call arguments when set.
	Arguments json.RawMessage
}

// HookResult is the outcome of a tool invocation, as a hook sees it.
type HookResult struct {
	// Text is the model-facing text of the result.
	Text string

	// IsError marks an invocation that ran but failed.
	IsError bool
}

// AfterToolExecuteHook describes one tool call after it ran.
type AfterToolExecuteHook struct {
	// Call is the invocation that ran.
	Call HookToolCall

	// Result is its outcome.
	Result HookResult
}

// AfterToolExecuteResult carries what the hook rewrites. TextSet distinguishes
// a returned empty text from no opinion; a nil IsError keeps the flag the
// tool reported.
type AfterToolExecuteResult struct {
	// Text replaces the result text.
	Text string

	// TextSet reports the hook returned a text.
	TextSet bool

	// IsError replaces the error flag.
	IsError *bool
}

// noHooks runs no hook extension. It is the zero-cost path of a turn whose
// agent declares no hooks or whose engine has no resolver.
type noHooks struct{}

// NoHooks returns the Hooks of a turn with no hook extension.
func NoHooks() Hooks {
	return noHooks{}
}

// BeforeRun runs no hook.
func (noHooks) BeforeRun(context.Context, BeforeRunHook) {}

// AfterRun runs no hook.
func (noHooks) AfterRun(context.Context, AfterRunHook) {}

// BeforeModelRequest runs no hook.
func (noHooks) BeforeModelRequest(
	_ context.Context,
	_ BeforeModelRequestHook,
) BeforeModelRequestResult {
	return BeforeModelRequestResult{}
}

// AfterModelResponse runs no hook.
func (noHooks) AfterModelResponse(
	_ context.Context,
	_ AfterModelResponseHook,
) AfterModelResponseResult {
	return AfterModelResponseResult{}
}

// BeforeToolExecute runs no hook.
func (noHooks) BeforeToolExecute(
	_ context.Context,
	_ BeforeToolExecuteHook,
) BeforeToolExecuteResult {
	return BeforeToolExecuteResult{}
}

// AfterToolExecute runs no hook.
func (noHooks) AfterToolExecute(_ context.Context, _ AfterToolExecuteHook) AfterToolExecuteResult {
	return AfterToolExecuteResult{}
}

// errNoHookResolver reports an agent that declares hooks while the engine
// has no resolver.
var errNoHookResolver = errors.New(
	"engine: the agent declares hooks but no hook resolver was provided",
)

// resolveHooks resolves the hooks declared by an agent against a resolver,
// collapsing duplicate names to their first occurrence.
func resolveHooks(resolver HookResolver, names []string) (Hooks, error) {
	if len(names) == 0 {
		return NoHooks(), nil
	}
	if resolver == nil {
		return nil, errNoHookResolver
	}

	ordered := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		ordered = append(ordered, name)
	}

	hooks, err := resolver.Resolve(ordered)
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	if hooks == nil {
		return NoHooks(), nil
	}
	return hooks, nil
}

// noticeKey is the context key carrying the sink of an extension report.
type noticeKey struct{}

// WithNotice attaches the sink that carries what an extension said while it
// ran. The engine attaches one before every hook call.
func WithNotice(ctx context.Context, emit func(text string)) context.Context {
	return context.WithValue(ctx, noticeKey{}, emit)
}

// NoticeFromContext returns the sink attached to ctx, if any.
func NoticeFromContext(ctx context.Context) (func(text string), bool) {
	emit, ok := ctx.Value(noticeKey{}).(func(text string))
	if !ok || emit == nil {
		return nil, false
	}
	return emit, true
}
