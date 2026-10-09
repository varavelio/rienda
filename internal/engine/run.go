package engine

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tool"
)

// errNothingToContinue reports a run asked to continue an empty session.
var errNothingToContinue = errors.New("engine: there is no conversation to continue")

// errEmptyResponse reports a model response left with no content: either the
// stream carried nothing at all, which is retried, or the hooks rewrote the
// answer into nothing, which is not, because the model already answered.
var errEmptyResponse = errors.New("engine: the model returned an empty response")

// errToolLoop reports a run the model kept repeating the same tool call with
// the same arguments and the same result. Without this guard a model stuck on
// a call it cannot fix would loop forever, spending a model call per round.
var errToolLoop = errors.New(
	"engine: the model kept requesting the same tool call with the same arguments and the run was stopped",
)

// Texts of the synthetic results persisted when a run is interrupted while
// tools are running. They keep the history valid for the next provider call
// and tell the model what happened.
const (
	interruptedBeforeTool = "the run was interrupted before the tool ran"
	interruptedDuringTool = "the run was interrupted while the tool ran"
)

// maxIdenticalToolRounds bounds how many times in a row a run may perform the
// same tool batch and see the same results. The model gets this many chances
// to change its approach before the run stops, which tolerates a retry while
// refusing a loop.
const maxIdenticalToolRounds = 3

// Run starts a run and returns the channel carrying its events.
//
// A non-empty prompt is appended to the session as the first user message of
// the run; an empty prompt continues the conversation from the active leaf.
//
// The caller must keep receiving from the channel until it closes: a run
// blocks while an event cannot be delivered. Canceling ctx interrupts the run
// and closes the channel.
func (e *Engine) Run(ctx context.Context, prompt string) <-chan Event {
	events := make(chan Event, eventBuffer)
	go func() {
		defer close(events)
		if !e.enter() {
			fail(events, errRunInFlight)
			return
		}
		defer e.leave()
		e.run(ctx, prompt, events)
	}()
	return events
}

// run executes the conversation loop, emitting every update through events.
// It always ends by emitting a run_end event.
func (e *Engine) run(ctx context.Context, prompt string, events chan<- Event) {
	if e.workdir != "" {
		ctx = tool.WithWorkdir(ctx, e.workdir)
	}
	baseCtx := ctx

	// The plan of the first turn names the agent and the model of the run, which
	// the start event reports. Building it here also fails the run before the
	// prompt is written when the branch names an agent or a model the harness
	// cannot run.
	plan, err := e.plan()
	if err != nil {
		fail(events, err)
		return
	}

	// The system prompt of the run is the one read from the workspace when it
	// opened, and every turn of the run sends it unchanged. Reading it once per
	// run rather than once per turn is what keeps a skill or an instruction file
	// from changing under the model in the middle of the work it was asked to
	// do; the next run reads the workspace again and picks the change up.
	system := plan.request.System

	start := Event{
		Type:        EventRunStart,
		SessionID:   e.store.ID(),
		AgentID:     plan.agent.ID,
		ModelID:     plan.model.ID,
		Diagnostics: plan.diagnostics,
	}
	switch {
	case prompt != "":
		entry, err := e.store.Append(ctx, session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: prompt}},
		}})
		if err != nil {
			fail(events, err)
			return
		}
		start.EntryID = entry.ID
	case e.store.Leaf() == "":
		fail(events, errNothingToContinue)
		return
	}
	emit(events, start)
	e.emitContext(events)

	// The run is open: afterRun runs exactly once from here on, on every exit
	// path, through finish and failRun. The hooks and the decorated context
	// follow the plan of the latest turn, so a session that switched agent
	// closes with the hooks it runs.
	runCtx := e.attachRun(baseCtx, plan, events)
	hooks := plan.hooks
	hooks.BeforeRun(runCtx, BeforeRunHook{
		SessionID: e.store.ID(),
		AgentID:   plan.agent.ID,
		ModelID:   plan.model.ID,
	})
	finish := func(reason EndReason) {
		emit(events, Event{Type: EventRunEnd, Reason: reason})
		hooks.AfterRun(runCtx, AfterRunHook{Reason: reason})
	}
	failRun := func(err error) {
		emit(events, Event{Type: EventError, Error: err.Error()})
		finish(EndReasonError)
	}

	// At most one automatic compaction happens per run: without the guard a
	// stubborn threshold would turn into a loop.
	compacted := false
	// The signature of the last tool batch and how many times in a row it
	// repeated, which is what stops a model stuck on the same call.
	lastRound := ""
	identicalRounds := 0
	// Persisting a complete response is not cancelable, so the context of the
	// writes is built once, outside the loop.
	persistCtx := context.WithoutCancel(ctx)
	for {
		if ctx.Err() != nil {
			finish(EndReasonInterrupted)
			return
		}

		plan, err = e.plan()
		if err != nil {
			failRun(err)
			return
		}
		plan = plan.withSystem(system)
		//nolint:fatcontext // the run identity follows the agent of the turn.
		runCtx = e.attachRun(baseCtx, plan, events)
		hooks = plan.hooks
		if !compacted && e.shouldCompact(plan) {
			compacted = true
			if err := e.compactBranch(ctx, events); err != nil {
				if ctx.Err() != nil {
					finish(EndReasonInterrupted)
					return
				}
				failRun(err)
				return
			}
			// The branch changed under the run: the compaction appended a
			// checkpoint, so the plan is rebuilt from the branch as it stands
			// and pinned to the system prompt the run opened with.
			if plan, err = e.plan(); err != nil {
				failRun(err)
				return
			}
			plan = plan.withSystem(system)
			runCtx = e.attachRun(baseCtx, plan, events)
			hooks = plan.hooks
		}

		if rewritten, ok := e.beforeModelRequest(runCtx, plan); ok {
			plan.request.System = rewritten
		}

		response, err := e.generate(ctx, events, plan)
		if err != nil {
			if ctx.Err() != nil {
				finish(EndReasonInterrupted)
				return
			}
			failRun(err)
			return
		}

		response = e.afterModelResponse(runCtx, plan, response)
		if len(response.toolCalls()) == 0 && responseProseEmpty(response.blocks) {
			failRun(errEmptyResponse)
			return
		}

		// Persisting a complete response is not cancelable: the work is done,
		// so it must survive an interrupt that arrives right now.
		entry, err := e.store.Append(persistCtx, session.Entry{
			Message: llm.Message{
				Role:   llm.RoleAssistant,
				Blocks: response.blocks,
				ItemID: response.messageItemID,
			},
			ResponseModel:      response.model,
			ResponseStopReason: response.stopReason,
			ResponseUsage:      response.usage,
		})
		if err != nil {
			failRun(err)
			return
		}
		emit(events, Event{
			Type:       EventMessageEnd,
			EntryID:    entry.ID,
			StopReason: response.stopReason,
			Usage:      usageFrom(response.usage),
		})
		e.emitContext(events)

		calls := response.toolCalls()
		if len(calls) == 0 {
			finish(EndReasonTurn)
			return
		}

		results := e.executeTools(runCtx, plan, response.argumentErrors, calls, events)
		if _, err := e.store.Append(persistCtx, session.Entry{
			Message: llm.Message{Role: llm.RoleUser, Blocks: results},
		}); err != nil {
			failRun(err)
			return
		}
		e.emitContext(events)

		if round := toolRoundSignature(calls, results); round == lastRound {
			identicalRounds++
			if identicalRounds >= maxIdenticalToolRounds {
				failRun(errToolLoop)
				return
			}
		} else {
			lastRound = round
			identicalRounds = 1
		}
	}
}

// beforeModelRequest runs the beforeModelRequest hooks of the turn, returning
// the replacement system prompt when a hook rewrote it.
func (e *Engine) beforeModelRequest(ctx context.Context, plan turnPlan) (string, bool) {
	result := plan.hooks.BeforeModelRequest(ctx, BeforeModelRequestHook{
		System:   plan.request.System,
		Model:    plan.request.Model,
		Messages: hookMessages(plan.request.Messages),
	})
	if !result.Set {
		return "", false
	}
	return result.System, true
}

// afterModelResponse runs the afterModelResponse hooks of the turn, rewriting
// the assistant prose they return.
func (e *Engine) afterModelResponse(ctx context.Context, plan turnPlan, response turn) turn {
	hookCalls := hookToolCalls(response.toolCalls(), response.argumentErrors)
	var text strings.Builder
	var thinking strings.Builder
	for _, block := range response.blocks {
		switch block.Type {
		case llm.BlockText:
			text.WriteString(block.Text)
		case llm.BlockThinking:
			thinking.WriteString(block.Thinking)
		}
	}
	result := plan.hooks.AfterModelResponse(ctx, AfterModelResponseHook{
		Text:      text.String(),
		Thinking:  thinking.String(),
		ToolCalls: hookCalls,
	})
	response.blocks = applyModelResponseRewrite(response.blocks, result)
	return response
}

// executeTools runs the tool calls of a response in request order and returns
// the tool result blocks to persist.
func (e *Engine) executeTools(
	ctx context.Context,
	plan turnPlan,
	argumentErrors map[string]error,
	calls []llm.Block,
	events chan<- Event,
) []llm.Block {
	// A hook that repairs malformed arguments legitimizes the call: the
	// replacement is a re-serialized object, well formed by construction.
	errors := make(map[string]error, len(argumentErrors))
	maps.Copy(errors, argumentErrors)
	results := make([]llm.Block, 0, len(calls))
	for _, call := range calls {
		results = append(results, e.executeTool(ctx, plan, errors, call, events))
	}
	return results
}

// executeTool runs one tool call and returns its result block. It never fails:
// calls that cannot run are reported to the model as error results, so the
// conversation stays valid. The hooks run for every call the model requested,
// in request order, before the engine's own checks, so a hook never misses a
// call because the tool is unknown or its arguments arrived malformed.
func (e *Engine) executeTool(
	ctx context.Context,
	plan turnPlan,
	argumentErrors map[string]error,
	call llm.Block,
	events chan<- Event,
) llm.Block {
	emit(events, Event{
		Type:       EventToolCall,
		ToolCallID: call.ToolCallID,
		ToolName:   call.ToolCallName,
		Arguments:  call.ToolCallArguments,
	})

	if ctx.Err() != nil {
		return reportResult(call, tool.ErrorResult(interruptedBeforeTool), events)
	}

	hookCall := HookToolCall{
		ID:        call.ToolCallID,
		Name:      call.ToolCallName,
		Arguments: call.ToolCallArguments,
	}
	before := plan.hooks.BeforeToolExecute(ctx, BeforeToolExecuteHook(hookCall))
	if len(before.Arguments) > 0 {
		hookCall.Arguments = before.Arguments
		call.ToolCallArguments = before.Arguments
		delete(argumentErrors, call.ToolCallID)
	}
	if before.Allow != nil && !*before.Allow {
		reason := before.Reason
		if reason == "" {
			reason = "the call was refused by a hook"
		}
		refused := tool.ErrorResult(reason)
		after := plan.hooks.AfterToolExecute(ctx, AfterToolExecuteHook{
			Call:   hookCall,
			Result: HookResult{Text: resultText(refused.Blocks), IsError: true},
		})
		return reportResult(call, applyToolRewrite(refused, after), events)
	}

	if err := argumentErrors[call.ToolCallID]; err != nil {
		result := tool.ErrorResult("the tool was not executed: " + err.Error())
		return e.afterTool(ctx, plan, hookCall, call, result, events)
	}

	executor, found := plan.tools.executors[call.ToolCallName]
	if !found {
		result := tool.ErrorResult("unknown tool " + strconv.Quote(call.ToolCallName))
		return e.afterTool(ctx, plan, hookCall, call, result, events)
	}

	result, err := executor.Execute(ctx, tool.Call{
		ID:        call.ToolCallID,
		Name:      call.ToolCallName,
		Arguments: call.ToolCallArguments,
	}, eventSink{
		events:   events,
		done:     ctx.Done(),
		callID:   call.ToolCallID,
		toolName: call.ToolCallName,
	})
	switch {
	case err != nil && ctx.Err() != nil:
		result = tool.ErrorResult(interruptedDuringTool)
	case err != nil:
		result = tool.ErrorResult(err.Error())
	case len(result.Blocks) == 0:
		result = tool.TextResult("(no output)")
	}
	return e.afterTool(ctx, plan, hookCall, call, result, events)
}

// afterTool runs the afterToolExecute hooks and returns the block to persist.
func (e *Engine) afterTool(
	ctx context.Context,
	plan turnPlan,
	hookCall HookToolCall,
	call llm.Block,
	result tool.Result,
	events chan<- Event,
) llm.Block {
	after := plan.hooks.AfterToolExecute(ctx, AfterToolExecuteHook{
		Call:   hookCall,
		Result: HookResult{Text: resultText(result.Blocks), IsError: result.IsError},
	})
	return reportResult(call, applyToolRewrite(result, after), events)
}

// applyToolRewrite replaces the result text and error flag the hook returned,
// leaving each untouched field as the tool reported it.
func applyToolRewrite(result tool.Result, after AfterToolExecuteResult) tool.Result {
	if !after.TextSet && after.IsError == nil {
		return result
	}
	text := resultText(result.Blocks)
	isError := result.IsError
	if after.TextSet {
		text = after.Text
	}
	if after.IsError != nil {
		isError = *after.IsError
	}
	if isError {
		return tool.ErrorResult(text)
	}
	return tool.TextResult(text)
}

// reportResult emits a tool result and returns the block to persist.
func reportResult(call llm.Block, result tool.Result, events chan<- Event) llm.Block {
	emit(events, Event{
		Type:       EventToolResult,
		ToolCallID: call.ToolCallID,
		ToolName:   call.ToolCallName,
		Text:       resultText(result.Blocks),
		IsError:    result.IsError,
	})
	return llm.Block{
		Type:              llm.BlockToolResult,
		ToolResultCallID:  call.ToolCallID,
		ToolResult:        result.Blocks,
		ToolResultIsError: result.IsError,
	}
}

// toolRoundSignature returns a stable signature of a tool batch: the name, the
// arguments and the result of every call, in order. Two rounds sharing a
// signature produced no progress at all, which is what lets a run notice a
// model that is stuck asking for the same call.
func toolRoundSignature(calls, results []llm.Block) string {
	if len(calls) == 0 || len(results) != len(calls) {
		return ""
	}
	var signature strings.Builder
	for i, call := range calls {
		signature.WriteString(call.ToolCallName)
		signature.WriteByte(0)
		signature.Write(call.ToolCallArguments)
		signature.WriteByte(0)
		signature.WriteString(resultText(results[i].ToolResult))
		signature.WriteByte('\n')
	}
	return signature.String()
}

// resultText flattens the model-facing text of tool result blocks.
func resultText(blocks []llm.Block) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == llm.BlockText && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// eventSink forwards the output of a running tool to the run events. Tool
// output arrives from the goroutines that produce it, and sending to a channel
// is safe for concurrent use.
type eventSink struct {
	events   chan<- Event
	done     <-chan struct{}
	callID   string
	toolName string
}

// Emit reports one chunk of tool output, dropping it when the run is over.
func (s eventSink) Emit(stream tool.Stream, data []byte) {
	if len(data) == 0 {
		return
	}

	select {
	case s.events <- Event{
		Type:       EventToolOutput,
		ToolCallID: s.callID,
		ToolName:   s.toolName,
		Stream:     stream,
		Output:     string(data),
	}:
	case <-s.done:
	}
}

// emit delivers an event to the run consumer, blocking until it receives.
func emit(events chan<- Event, event Event) {
	events <- event
}

// fail reports a failure and ends the run.
func fail(events chan<- Event, err error) {
	emit(events, Event{Type: EventError, Error: err.Error()})
	emit(events, Event{Type: EventRunEnd, Reason: EndReasonError})
}
