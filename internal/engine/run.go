package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tool"
)

// errNothingToContinue reports a run asked to continue an empty session.
var errNothingToContinue = errors.New("engine: there is no conversation to continue")

// Texts of the synthetic results persisted when a run is interrupted while
// tools are running. They keep the history valid for the next provider call
// and tell the model what happened.
const (
	interruptedBeforeTool = "the run was interrupted before the tool ran"
	interruptedDuringTool = "the run was interrupted while the tool ran"
)

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

	definition, err := e.agentOf()
	if err != nil {
		fail(events, err)
		return
	}

	start := Event{
		Type:      EventRunStart,
		SessionID: e.store.ID(),
		AgentID:   definition.ID,
		ModelID:   e.model.ID,
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

	// The session identifier travels with the request so providers can group
	// the calls of one conversation.
	requestCtx := llm.WithSessionID(ctx, e.store.ID())

	// At most one automatic compaction happens per run: without the guard a
	// stubborn threshold would turn into a loop.
	compacted := false
	for {
		if ctx.Err() != nil {
			emit(events, Event{Type: EventRunEnd, Reason: EndReasonInterrupted})
			return
		}

		request, tools, err := e.request()
		if err != nil {
			fail(events, err)
			return
		}
		if !compacted && e.shouldCompact(request) {
			compacted = true
			if err := e.compactBranch(requestCtx, events); err != nil {
				if ctx.Err() != nil {
					emit(events, Event{Type: EventRunEnd, Reason: EndReasonInterrupted})
					return
				}
				fail(events, err)
				return
			}
			if request, tools, err = e.request(); err != nil {
				fail(events, err)
				return
			}
		}

		response, err := e.generate(requestCtx, events, request)
		if err != nil {
			if ctx.Err() != nil {
				emit(events, Event{Type: EventRunEnd, Reason: EndReasonInterrupted})
				return
			}
			fail(events, err)
			return
		}

		// Persisting a complete response is not cancelable: the work is done,
		// so it must survive an interrupt that arrives right now.
		entry, err := e.store.Append(context.WithoutCancel(ctx), session.Entry{
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
			fail(events, err)
			return
		}
		emit(events, Event{
			Type:       EventMessageEnd,
			EntryID:    entry.ID,
			StopReason: response.stopReason,
			Usage:      usageFrom(response.usage),
		})

		calls := response.toolCalls()
		if len(calls) == 0 {
			emit(events, Event{Type: EventRunEnd, Reason: EndReasonTurn})
			return
		}

		results := e.executeTools(ctx, tools, response.argumentErrors, calls, events)
		if _, err := e.store.Append(context.WithoutCancel(ctx), session.Entry{
			Message: llm.Message{Role: llm.RoleUser, Blocks: results},
		}); err != nil {
			fail(events, err)
			return
		}
	}
}

// executeTools runs the tool calls of a response in request order and returns
// the tool result blocks to persist.
func (e *Engine) executeTools(
	ctx context.Context,
	tools turnTools,
	argumentErrors map[string]error,
	calls []llm.Block,
	events chan<- Event,
) []llm.Block {
	results := make([]llm.Block, 0, len(calls))
	for _, call := range calls {
		results = append(results, e.executeTool(ctx, tools, argumentErrors, call, events))
	}
	return results
}

// executeTool runs one tool call and returns its result block. It never fails:
// calls that cannot run are reported to the model as error results, so the
// conversation stays valid.
func (e *Engine) executeTool(
	ctx context.Context,
	tools turnTools,
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
	if err := argumentErrors[call.ToolCallID]; err != nil {
		result := tool.ErrorResult("the tool was not executed: " + err.Error())
		return reportResult(call, result, events)
	}

	executor, found := tools.executors[call.ToolCallName]
	if !found {
		result := tool.ErrorResult("unknown tool " + strconv.Quote(call.ToolCallName))
		return reportResult(call, result, events)
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
	return reportResult(call, result, events)
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
