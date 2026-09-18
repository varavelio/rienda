//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// anthropicStream is the codec of the Anthropic Messages protocol.
type anthropicStream struct{}

// payloads turns a scripted turn into the events of a streamed message. Every
// content block is opened, streamed and closed in order, and the closing
// message_delta carries the stop reason and the output token count exactly as
// the Messages API reports them.
func (anthropicStream) payloads(turn Turn, sequence int, model string) ([]string, error) {
	builder := &payloadBuilder{}
	id := responseID("msg", sequence)

	usage := anthropicUsage{}
	if turn.Usage != nil {
		usage.InputTokens = turn.Usage.InputTokens
		usage.OutputTokens = turn.Usage.OutputTokens
		usage.CacheReadInputTokens = turn.Usage.CacheReadTokens
	}
	builder.add(anthropicEnvelope{
		Type: "message_start",
		Message: &anthropicMessagePayload{
			ID:    id,
			Model: model,
			Usage: &anthropicUsage{
				InputTokens:          usage.InputTokens,
				CacheReadInputTokens: usage.CacheReadInputTokens,
			},
		},
	})

	index := 0
	start := func(block *anthropicBlock) {
		current := index
		index++
		builder.add(
			anthropicEnvelope{Type: "content_block_start", Index: &current, ContentBlock: block},
		)
	}
	delta := func(current int, payload anthropicBlockDelta) {
		builder.add(
			anthropicEnvelope{Type: "content_block_delta", Index: &current, Delta: &payload},
		)
	}
	stop := func(current int) {
		builder.add(anthropicEnvelope{Type: "content_block_stop", Index: &current})
	}

	if turn.Thinking != "" {
		current := index
		start(&anthropicBlock{Type: "thinking", Thinking: ""})
		for _, fragment := range chunkText(turn.Thinking, turn.Chunks) {
			delta(current, anthropicBlockDelta{Type: "thinking_delta", Thinking: fragment})
		}
		stop(current)
	}

	if turn.Text != "" {
		current := index
		start(&anthropicBlock{Type: "text", Text: ""})
		for _, fragment := range chunkText(turn.Text, turn.Chunks) {
			delta(current, anthropicBlockDelta{Type: "text_delta", Text: fragment})
		}
		stop(current)
	}

	for callIndex, call := range turn.Calls {
		arguments, err := argumentsOf(call)
		if err != nil {
			return nil, err
		}

		current := index
		start(&anthropicBlock{
			Type:  "tool_use",
			ID:    callID(call, sequence, callIndex),
			Name:  call.Name,
			Input: json.RawMessage("{}"),
		})
		for _, fragment := range fragmentArguments(arguments, turn.Chunks) {
			delta(current, anthropicBlockDelta{Type: "input_json_delta", PartialJSON: fragment})
		}
		stop(current)
	}

	stopReason := "end_turn"
	if len(turn.Calls) > 0 {
		stopReason = "tool_use"
	}
	builder.add(anthropicEnvelope{
		Type:  "message_delta",
		Delta: &anthropicBlockDelta{StopReason: stopReason},
		Usage: &anthropicUsage{OutputTokens: usage.OutputTokens},
	})
	builder.add(anthropicEnvelope{Type: "message_stop"})

	payloads, err := builder.done()
	if err != nil {
		return nil, fmt.Errorf("anthropic payload: %w", err)
	}
	return payloads, nil
}

// anthropicEnvelope is one streamed event of the Messages protocol.
type anthropicEnvelope struct {
	Type         string                   `json:"type"`
	Message      *anthropicMessagePayload `json:"message,omitempty"`
	Index        *int                     `json:"index,omitempty"`
	ContentBlock *anthropicBlock          `json:"content_block,omitempty"`
	Delta        *anthropicBlockDelta     `json:"delta,omitempty"`
	Usage        *anthropicUsage          `json:"usage,omitempty"`
}

// anthropicMessagePayload is the message of a message_start event.
type anthropicMessagePayload struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// anthropicBlock is one streamed content block. Only the fields valid for its
// type carry meaning.
type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

// anthropicBlockDelta is the payload of a content_block_delta event and of the
// closing message_delta event.
type anthropicBlockDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// anthropicUsage mirrors the wire usage object.
type anthropicUsage struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
}

// AnthropicRequest is the decoded body of a captured Messages request.
type AnthropicRequest struct {
	// Model is the wire identifier the request generates with.
	Model string `json:"model"`

	// MaxTokens caps the generated tokens. The protocol requires it.
	MaxTokens int `json:"max_tokens"`

	// System is the system instruction of the conversation.
	System string `json:"system"`

	// Messages is the conversation replayed to the provider.
	Messages []AnthropicMessage `json:"messages"`

	// Tools declares the tools the model may invoke.
	Tools []AnthropicTool `json:"tools"`

	// ToolChoice constrains tool usage when the application sends one.
	ToolChoice *AnthropicToolChoice `json:"tool_choice"`

	// Temperature is the sampling temperature, nil when unset.
	Temperature *float64 `json:"temperature"`

	// TopP is the nucleus sampling, nil when unset.
	TopP *float64 `json:"top_p"`

	// Thinking configures extended reasoning when the application enables it.
	Thinking *AnthropicThinking `json:"thinking"`

	// Stream reports whether the request asks for a streamed response.
	Stream bool `json:"stream"`
}

// AnthropicMessage is one decoded wire conversation turn.
type AnthropicMessage struct {
	// Role is the author of the message.
	Role string `json:"role"`

	// Content is the raw content of the message, either a JSON string or the
	// list of content blocks the protocol allows.
	Content json.RawMessage `json:"content"`
}

// AnthropicBlock is one decoded content block of a wire message.
type AnthropicBlock struct {
	// Type is the type of the block.
	Type string `json:"type"`

	// Text is the content of a text block.
	Text string `json:"text"`

	// Thinking is the reasoning of a thinking block.
	Thinking string `json:"thinking"`

	// Signature is the provider signature of a thinking block.
	Signature string `json:"signature"`

	// ID is the identifier of a tool use block.
	ID string `json:"id"`

	// Name is the name of a tool use block.
	Name string `json:"name"`

	// Input holds the arguments of a tool use block.
	Input json.RawMessage `json:"input"`

	// ToolUseID references the tool use block a result answers.
	ToolUseID string `json:"tool_use_id"`

	// Content holds the content of a tool result block, either a string or the
	// list of content blocks the protocol allows.
	Content json.RawMessage `json:"content"`

	// IsError marks a tool result that reports a failure.
	IsError bool `json:"is_error"`
}

// AnthropicTool is one decoded tool declaration.
type AnthropicTool struct {
	// Name is the tool name.
	Name string `json:"name"`

	// Description explains the tool to the model.
	Description string `json:"description"`

	// InputSchema is the JSON Schema of the arguments.
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicToolChoice is a decoded tool choice constraint.
type AnthropicToolChoice struct {
	// Type selects how the model may use the declared tools.
	Type string `json:"type"`

	// Name selects the tool of a constrained choice.
	Name string `json:"name"`

	// DisableParallelToolUse forbids several calls in one turn.
	DisableParallelToolUse *bool `json:"disable_parallel_tool_use"`
}

// AnthropicThinking is a decoded extended reasoning configuration.
type AnthropicThinking struct {
	// Type is the kind of reasoning configuration.
	Type string `json:"type"`

	// BudgetTokens is the token budget reserved for reasoning.
	BudgetTokens int `json:"budget_tokens"`
}

// Anthropic decodes a captured request as a Messages payload, failing the test
// when the body does not follow the protocol.
func (r Request) Anthropic(t *testing.T) *AnthropicRequest {
	t.Helper()

	var decoded AnthropicRequest
	if err := json.Unmarshal(r.Body, &decoded); err != nil {
		t.Fatalf("decode anthropic request: %v (body: %s)", err, r.Body)
	}
	return &decoded
}

// Text returns the text a message carries: the content of a plain text
// message, or the concatenated text blocks of a block message.
func (m AnthropicMessage) Text() string {
	var text string
	if err := json.Unmarshal(m.Content, &text); err == nil {
		return text
	}
	return textOfBlocks(m.Blocks())
}

// Blocks returns the content blocks of a message, nil for a plain text
// message.
func (m AnthropicMessage) Blocks() []AnthropicBlock {
	var blocks []AnthropicBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil
	}
	return blocks
}

// BlockTypes returns the types of the content blocks of a message, in order.
// A message that carries plain text has no blocks.
func (m AnthropicMessage) BlockTypes() []string {
	blocks := m.Blocks()
	types := make([]string, 0, len(blocks))
	for _, block := range blocks {
		types = append(types, block.Type)
	}
	return types
}

// ContentText returns the text a tool result block carries, flattening both
// shapes the content of the protocol allows.
func (b AnthropicBlock) ContentText() string {
	var text string
	if err := json.Unmarshal(b.Content, &text); err == nil {
		return text
	}

	var blocks []AnthropicBlock
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return ""
	}
	return textOfBlocks(blocks)
}

// textOfBlocks concatenates the text of the text blocks of a list, used to
// flatten the nested content some blocks carry.
func textOfBlocks(blocks []AnthropicBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// ToolNames returns the names of the tools a request declares, in order.
func (r *AnthropicRequest) ToolNames() []string {
	names := make([]string, 0, len(r.Tools))
	for _, tool := range r.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// Roles returns the roles of the replayed messages, in order.
func (r *AnthropicRequest) Roles() []string {
	roles := make([]string, 0, len(r.Messages))
	for _, message := range r.Messages {
		roles = append(roles, message.Role)
	}
	return roles
}
