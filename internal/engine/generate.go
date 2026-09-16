package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
)

// turn is a completed model response ready to persist.
type turn struct {
	model      string
	blocks     []llm.Block
	stopReason llm.StopReason
	usage      llm.Usage

	// argumentErrors reports the tool calls whose streamed arguments were not
	// a JSON object, keyed by call ID.
	argumentErrors map[string]error
}

// toolCalls returns the tool call blocks of the turn in request order.
func (t turn) toolCalls() []llm.Block {
	calls := make([]llm.Block, 0, len(t.blocks))
	for _, block := range t.blocks {
		if block.Type == llm.BlockToolCall {
			calls = append(calls, block)
		}
	}
	return calls
}

// streamedCall accumulates one tool call while its arguments stream.
type streamedCall struct {
	id        string
	name      string
	arguments strings.Builder
}

// accumulator assembles the stream events of one model response into a turn.
type accumulator struct {
	text       strings.Builder
	thinking   strings.Builder
	signature  strings.Builder
	redacted   []string
	calls      []*streamedCall
	callsByID  map[string]*streamedCall
	model      string
	stopReason llm.StopReason
	usage      llm.Usage
}

// newAccumulator returns an empty response accumulator.
func newAccumulator() *accumulator {
	return &accumulator{callsByID: map[string]*streamedCall{}}
}

// observe records one stream event.
func (a *accumulator) observe(event llm.StreamEvent) {
	switch event.Type {
	case llm.StreamMessageStart:
		a.model = event.Model
	case llm.StreamTextDelta:
		a.text.WriteString(event.Text)
	case llm.StreamThinkingDelta:
		a.thinking.WriteString(event.Thinking)
		a.signature.WriteString(event.ThinkingSignature)
	case llm.StreamThinkingRedacted:
		a.redacted = append(a.redacted, event.ThinkingRedactedData)
	case llm.StreamToolCallStart:
		a.startCall(event)
	case llm.StreamToolCallArgsDelta:
		a.appendArguments(event)
	case llm.StreamMessageEnd:
		a.stopReason = event.StopReason
		a.usage = event.Usage
	}
}

// startCall records the beginning of a tool call, ignoring starts without an
// identifier and repeated identifiers.
func (a *accumulator) startCall(event llm.StreamEvent) {
	if event.ToolCallID == "" {
		return
	}
	if _, found := a.callsByID[event.ToolCallID]; found {
		return
	}

	call := &streamedCall{id: event.ToolCallID, name: event.ToolCallName}
	a.callsByID[event.ToolCallID] = call
	a.calls = append(a.calls, call)
}

// appendArguments extends a streamed tool call with an argument fragment,
// ignoring fragments of calls that never started.
func (a *accumulator) appendArguments(event llm.StreamEvent) {
	if call, found := a.callsByID[event.ToolCallID]; found {
		call.arguments.WriteString(event.ToolCallArgsDelta)
	}
}

// turn returns the assembled model response. It fails when the response
// carries no content at all.
func (a *accumulator) turn(fallbackModel string) (turn, error) {
	blocks := make([]llm.Block, 0, 4)
	if a.thinking.Len() > 0 || a.signature.Len() > 0 {
		blocks = append(blocks, llm.Block{
			Type:              llm.BlockThinking,
			Thinking:          a.thinking.String(),
			ThinkingSignature: a.signature.String(),
		})
	}
	for _, data := range a.redacted {
		blocks = append(
			blocks,
			llm.Block{Type: llm.BlockRedactedThinking, ThinkingRedactedData: data},
		)
	}
	if a.text.Len() > 0 {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: a.text.String()})
	}

	argumentErrors := make(map[string]error, len(a.calls))
	for _, call := range a.calls {
		arguments, err := normalizeArguments(call.arguments.String())
		if err != nil {
			argumentErrors[call.id] = err
		}
		blocks = append(blocks, llm.Block{
			Type:              llm.BlockToolCall,
			ToolCallID:        call.id,
			ToolCallName:      call.name,
			ToolCallArguments: arguments,
		})
	}

	if len(blocks) == 0 {
		return turn{}, errors.New("engine: the model returned an empty response")
	}

	model := a.model
	if model == "" {
		model = fallbackModel
	}
	return turn{
		model:          model,
		blocks:         blocks,
		stopReason:     a.stopReason,
		usage:          a.usage,
		argumentErrors: argumentErrors,
	}, nil
}

// normalizeArguments turns the streamed arguments of a tool call into a JSON
// object. Empty arguments become an empty object. Anything that is not a JSON
// object is reported and replaced by an empty object, so the persisted history
// stays well formed for every provider.
func normalizeArguments(raw string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage("{}"), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return json.RawMessage("{}"), errors.New("the streamed arguments are not valid JSON")
	}
	if !strings.HasPrefix(trimmed, "{") {
		return json.RawMessage("{}"), errors.New("the streamed arguments are not a JSON object")
	}
	return json.RawMessage(trimmed), nil
}

// generate streams one model response, forwarding its deltas as events, and
// returns the completed turn.
func (e *Engine) generate(ctx context.Context, events chan<- Event) (turn, error) {
	stream, err := e.client.Stream(ctx, e.request())
	if err != nil {
		return turn{}, fmt.Errorf("engine: stream response: %w", err)
	}
	defer func() { _ = stream.Close() }()

	accumulator := newAccumulator()
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return turn{}, fmt.Errorf("engine: read response stream: %w", err)
		}

		switch event.Type {
		case llm.StreamTextDelta:
			emit(events, Event{Type: EventTextDelta, Text: event.Text})
		case llm.StreamThinkingDelta:
			if event.Thinking != "" {
				emit(events, Event{Type: EventThinkingDelta, Text: event.Thinking})
			}
		}
		accumulator.observe(event)
	}

	return accumulator.turn(e.model.ID)
}
