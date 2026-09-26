package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/jsruntime"
)

// JSON field names crossing the bridge.
const (
	fieldText      = "text"
	fieldThinking  = "thinking"
	fieldArguments = "arguments"
	fieldAllow     = "allow"
	fieldReason    = "reason"
	fieldIsError   = "isError"
	fieldName      = "name"
)

// Hooks runs the hook extensions an agent declares, in order, and implements
// engine.Hooks directly.
type Hooks struct {
	order   []string
	modules map[string]*jsruntime.Module
	opts    jsruntime.Options
}

// Resolve returns the hooks the named extensions install, in order, ready for
// the engine to call. An unknown name is an error naming the available hooks.
func (r Registry) Resolve(names []string) (*Hooks, error) {
	ordered := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, ok := r.modules[name]; !ok {
			return nil, fmt.Errorf(
				"hook: unknown hook %q (available: %s)",
				name,
				strings.Join(r.Names(), ", "),
			)
		}
		ordered = append(ordered, name)
	}
	return &Hooks{order: ordered, modules: r.modules, opts: r.opts}, nil
}

// BeforeRun runs the beforeRun functions once per run.
func (h *Hooks) BeforeRun(ctx context.Context, ev engine.BeforeRunHook) {
	h.runAdvisory(ctx, "beforeRun", map[string]any{
		"sessionId": ev.SessionID,
		"agentId":   ev.AgentID,
		"modelId":   ev.ModelID,
	})
}

// AfterRun runs the afterRun functions once per run.
func (h *Hooks) AfterRun(ctx context.Context, ev engine.AfterRunHook) {
	h.runAdvisory(ctx, "afterRun", map[string]any{
		fieldReason: string(ev.Reason),
	})
}

// BeforeModelRequest runs the beforeModelRequest functions, chaining the
// system prompt they return.
func (h *Hooks) BeforeModelRequest(
	ctx context.Context,
	ev engine.BeforeModelRequestHook,
) engine.BeforeModelRequestResult {
	system := ev.System
	messages := make([]any, 0, len(ev.Messages))
	for _, message := range ev.Messages {
		messages = append(messages, map[string]any{"role": message.Role, fieldText: message.Text})
	}
	set := false
	for _, name := range h.order {
		returned, ok := h.call(ctx, name, "beforeModelRequest", map[string]any{
			"system":   system,
			"model":    ev.Model,
			"messages": messages,
		})
		if !ok {
			continue
		}
		doc, ok := asObject(returned)
		if !ok {
			h.report(ctx, name, "beforeModelRequest", "return value is not an object")
			continue
		}
		if raw, present := doc["system"]; present && raw != nil {
			text, ok := raw.(string)
			if !ok {
				h.report(ctx, name, "beforeModelRequest", "system is not a string")
				continue
			}
			system, set = text, true
		}
	}
	return engine.BeforeModelRequestResult{System: system, Set: set}
}

// AfterModelResponse runs the afterModelResponse functions, chaining the prose
// they return.
func (h *Hooks) AfterModelResponse(
	ctx context.Context,
	ev engine.AfterModelResponseHook,
) engine.AfterModelResponseResult {
	text, thinking := ev.Text, ev.Thinking
	calls := make([]any, 0, len(ev.ToolCalls))
	for _, call := range ev.ToolCalls {
		calls = append(calls, map[string]any{
			"id":           call.ID,
			fieldName:      call.Name,
			fieldArguments: hookArguments(call.Arguments),
		})
	}
	var textSet, thinkingSet bool
	for _, name := range h.order {
		returned, ok := h.call(ctx, name, "afterModelResponse", map[string]any{
			fieldText:     text,
			fieldThinking: thinking,
			"toolCalls":   calls,
		})
		if !ok {
			continue
		}
		doc, ok := asObject(returned)
		if !ok {
			h.report(ctx, name, "afterModelResponse", "return value is not an object")
			continue
		}
		if raw, present := doc[fieldText]; present && raw != nil {
			replacement, ok := raw.(string)
			if !ok {
				h.report(ctx, name, "afterModelResponse", "text is not a string")
			} else {
				text, textSet = replacement, true
			}
		}
		if raw, present := doc[fieldThinking]; present && raw != nil {
			replacement, ok := raw.(string)
			if !ok {
				h.report(ctx, name, "afterModelResponse", "thinking is not a string")
			} else {
				thinking, thinkingSet = replacement, true
			}
		}
	}
	return engine.AfterModelResponseResult{
		Text:        text,
		TextSet:     textSet,
		Thinking:    thinking,
		ThinkingSet: thinkingSet,
	}
}

// BeforeToolExecute runs the beforeToolExecute functions in order, stopping at
// the first refusal. An arguments rewrite chains into the next hook.
func (h *Hooks) BeforeToolExecute(
	ctx context.Context,
	ev engine.BeforeToolExecuteHook,
) engine.BeforeToolExecuteResult {
	arguments := hookArguments(ev.Arguments)
	for _, name := range h.order {
		returned, ok := h.call(ctx, name, "beforeToolExecute", map[string]any{
			"id":           ev.ID,
			fieldName:      ev.Name,
			fieldArguments: arguments,
		})
		if !ok {
			continue
		}
		doc, ok := asObject(returned)
		if !ok {
			h.report(ctx, name, "beforeToolExecute", "return value is not an object")
			continue
		}
		if raw, present := doc[fieldArguments]; present && raw != nil {
			replacement, ok := reencode(raw)
			if !ok {
				h.report(ctx, name, "beforeToolExecute", "arguments is not an object")
			} else {
				arguments = replacement
			}
		}
		if raw, present := doc[fieldAllow]; present && raw != nil {
			allow, ok := raw.(bool)
			if !ok {
				h.report(ctx, name, "beforeToolExecute", "allow is not a boolean")
				continue
			}
			if !allow {
				reason, _ := doc[fieldReason].(string)
				return engine.BeforeToolExecuteResult{Allow: &allow, Reason: reason}
			}
		}
	}
	if changed(ev.Arguments, arguments) {
		return engine.BeforeToolExecuteResult{Arguments: encodeRaw(arguments)}
	}
	return engine.BeforeToolExecuteResult{}
}

// AfterToolExecute runs the afterToolExecute functions, chaining the result
// they return.
func (h *Hooks) AfterToolExecute(
	ctx context.Context,
	ev engine.AfterToolExecuteHook,
) engine.AfterToolExecuteResult {
	text, isError := ev.Result.Text, ev.Result.IsError
	call := map[string]any{
		"id":           ev.Call.ID,
		fieldName:      ev.Call.Name,
		fieldArguments: hookArguments(ev.Call.Arguments),
	}
	var textSet bool
	isErrorSet := false
	for _, name := range h.order {
		returned, ok := h.call(ctx, name, "afterToolExecute", call, map[string]any{
			fieldText:    text,
			fieldIsError: isError,
		})
		if !ok {
			continue
		}
		doc, ok := asObject(returned)
		if !ok {
			h.report(ctx, name, "afterToolExecute", "return value is not an object")
			continue
		}
		if raw, present := doc[fieldText]; present && raw != nil {
			replacement, ok := raw.(string)
			if !ok {
				h.report(ctx, name, "afterToolExecute", "text is not a string")
			} else {
				text, textSet = replacement, true
			}
		}
		if raw, present := doc[fieldIsError]; present && raw != nil {
			flag, ok := raw.(bool)
			if !ok {
				h.report(ctx, name, "afterToolExecute", "isError is not a boolean")
			} else {
				isError, isErrorSet = flag, true
			}
		}
	}
	result := engine.AfterToolExecuteResult{Text: text, TextSet: textSet}
	if isErrorSet {
		result.IsError = &isError
	}
	return result
}

// runAdvisory runs the advisory functions of a point, ignoring their return
// values.
func (h *Hooks) runAdvisory(ctx context.Context, point string, payload map[string]any) {
	for _, name := range h.order {
		returned, ok := h.call(ctx, name, point, payload)
		if !ok {
			continue
		}
		if _, isObject := asObject(returned); !isObject {
			h.report(ctx, name, point, "return value is not an object")
		}
	}
}

// call invokes one hook function in a fresh runtime for this invocation only,
// returning false for no opinion: a missing export, an unusable return or a
// throw, which is reported as a notice.
func (h *Hooks) call(ctx context.Context, extension, point string, args ...any) (any, bool) {
	module, ok := h.modules[extension]
	if !ok {
		return nil, false
	}
	var (
		returned any
		called   bool
	)
	err := module.Invoke(
		ctx,
		h.opts,
		h.output(ctx),
		func(rt *jsruntime.Runtime, exports *jsruntime.Exports) error {
			fn, ok := exports.Field(point)
			if !ok || !fn.IsCallable() {
				return nil
			}
			invocation := make([]any, 0, len(args)+1)
			invocation = append(invocation, rt.ContextValue())
			invocation = append(invocation, args...)
			value, err := fn.Call(ctx, invocation...)
			if err != nil {
				return fmt.Errorf("hook %q: %w", extension, err)
			}
			if value.Undefined() {
				return nil
			}
			decoded, ok := value.JSON()
			if !ok {
				return errUnusableReturn
			}
			returned, called = decoded, true
			return nil
		},
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false
		}
		h.report(ctx, extension, point, hookErrorText(err))
		return nil, false
	}
	return returned, called
}

// errUnusableReturn marks a return value that cannot cross the bridge.
var errUnusableReturn = &unusableReturnError{}

// unusableReturnError reports a return value outside the accepted shapes.
type unusableReturnError struct{}

// Error describes the problem.
func (e *unusableReturnError) Error() string {
	return "return value is not an object"
}

// output streams hook output through the notice sink attached to ctx.
func (h *Hooks) output(ctx context.Context) func(jsruntime.Stream, []byte) {
	return func(_ jsruntime.Stream, data []byte) {
		if emit, ok := engine.NoticeFromContext(ctx); ok {
			emit(string(data))
		}
	}
}

// report sends a run-time diagnostic through the notice sink, so the front end
// shows it and the run continues.
func (h *Hooks) report(ctx context.Context, extension, point, problem string) {
	if emit, ok := engine.NoticeFromContext(ctx); ok {
		emit(`hook "` + extension + `": ` + point + ": " + problem)
	}
}

// hookErrorText renders a hook failure without host paths.
func hookErrorText(err error) string {
	text := err.Error()
	if text == "" {
		return "the hook threw"
	}
	return text
}

// asObject reads a plain object return value.
func asObject(value any) (map[string]any, bool) {
	doc, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return doc, true
}

// hookArguments decodes call arguments for hooks, defaulting to an empty
// object so a hook always reads an object.
func hookArguments(raw json.RawMessage) map[string]any {
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil || arguments == nil {
		return map[string]any{}
	}
	return arguments
}

// reencode re-serializes a replacement value, reporting whether it is an
// object.
func reencode(value any) (map[string]any, bool) {
	doc, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	var plain map[string]any
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return nil, false
	}
	return plain, true
}

// encodeRaw serializes arguments back into raw JSON.
func encodeRaw(arguments map[string]any) json.RawMessage {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

// changed reports whether the arguments differ from the recorded call.
func changed(recorded json.RawMessage, current map[string]any) bool {
	var before map[string]any
	if err := json.Unmarshal(recorded, &before); err != nil {
		return true
	}
	beforeRaw, err := json.Marshal(before)
	if err != nil {
		return true
	}
	return string(beforeRaw) != string(encodeRaw(current))
}
