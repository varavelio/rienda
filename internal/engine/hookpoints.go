package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/llm"
)

// attachRun decorates ctx with the run identity of the turn and the notice
// sink that carries what a hook reports, emitting notices as EventNotice.
func (e *Engine) attachRun(
	ctx context.Context,
	plan turnPlan,
	events chan<- Event,
) context.Context {
	ctx = agent.WithRun(ctx, agent.Run{
		SessionID: e.store.ID(),
		ModelID:   plan.model.ID,
		Agent:     plan.agent,
	})
	return WithNotice(ctx, func(text string) {
		if text == "" {
			return
		}
		select {
		case events <- Event{Type: EventNotice, Text: text}:
		case <-ctx.Done():
		}
	})
}

// hookMessages flattens the request messages for hooks: one entry per message
// with the concatenated text, leaving thinking and tool blocks out.
func hookMessages(messages []llm.Message) []HookMessage {
	flattened := make([]HookMessage, 0, len(messages))
	for _, message := range messages {
		var text strings.Builder
		for _, block := range message.Blocks {
			if block.Type == llm.BlockText {
				text.WriteString(block.Text)
			}
		}
		flattened = append(flattened, HookMessage{Role: string(message.Role), Text: text.String()})
	}
	return flattened
}

// hookToolCalls projects the tool calls of a response for hooks. A call whose
// arguments arrived malformed is listed with an empty object, so a hook
// always reads an object.
func hookToolCalls(calls []llm.Block, argumentErrors map[string]error) []HookToolCall {
	projected := make([]HookToolCall, 0, len(calls))
	for _, call := range calls {
		arguments := call.ToolCallArguments
		if err := argumentErrors[call.ToolCallID]; err != nil {
			arguments = json.RawMessage("{}")
		}
		projected = append(projected, HookToolCall{
			ID:        call.ToolCallID,
			Name:      call.ToolCallName,
			Arguments: arguments,
		})
	}
	return projected
}

// applyModelResponseRewrite replaces the assistant prose with what the hooks
// returned: all text blocks become one carrying text, all thinking blocks one
// carrying thinking. Tool-call, tool-result and redacted-thinking blocks stay
// in place.
func applyModelResponseRewrite(blocks []llm.Block, result AfterModelResponseResult) []llm.Block {
	if !result.TextSet && !result.ThinkingSet {
		return blocks
	}
	rewritten := make([]llm.Block, 0, len(blocks)+2)
	textDone := !result.TextSet
	thinkingDone := !result.ThinkingSet
	for _, block := range blocks {
		switch block.Type {
		case llm.BlockText:
			if textDone {
				continue
			}
			textDone = true
			if result.Text != "" {
				rewritten = append(rewritten, llm.Block{Type: llm.BlockText, Text: result.Text})
			}
		case llm.BlockThinking:
			if thinkingDone {
				continue
			}
			thinkingDone = true
			if result.Thinking != "" {
				rewritten = append(rewritten, llm.Block{
					Type:     llm.BlockThinking,
					Thinking: result.Thinking,
				})
			}
		default:
			rewritten = append(rewritten, block)
		}
	}
	return rewritten
}

// responseProseEmpty reports whether blocks carry no text, no thinking and no
// tool call.
func responseProseEmpty(blocks []llm.Block) bool {
	for _, block := range blocks {
		switch block.Type {
		case llm.BlockText:
			if block.Text != "" {
				return false
			}
		case llm.BlockThinking:
			if block.Thinking != "" {
				return false
			}
		case llm.BlockToolCall:
			return false
		}
	}
	return true
}
