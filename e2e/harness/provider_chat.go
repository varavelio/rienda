//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"testing"
)

// chatDone is the sentinel that closes a streamed Chat Completions response.
const chatDone = "[DONE]"

// chatFinishReason returns the wire finish reason of a scripted turn.
func chatFinishReason(turn Turn) string {
	if len(turn.Calls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

// chatStream is the codec of the OpenAI Chat Completions protocol.
type chatStream struct{}

// payloads turns a scripted turn into the chunks of a streamed completion. The
// first chunk only opens the message, as every consumer reads the message
// metadata from it, and the usage chunk precedes the chunk that closes the
// response.
func (chatStream) payloads(turn Turn, sequence int, model string) ([]string, error) {
	builder := &payloadBuilder{}
	id := responseID("chatcmpl", sequence)

	choice := func(choice chatCompletionChunkChoice) {
		choice.Index = 0
		builder.add(chatCompletionChunk{
			ID:      id,
			Model:   model,
			Choices: []chatCompletionChunkChoice{choice},
		})
	}
	delta := func(current chatDelta) {
		choice(chatCompletionChunkChoice{Delta: current})
	}

	delta(chatDelta{Role: wireAssistant})
	for _, fragment := range chunkText(turn.Thinking, turn.Chunks) {
		delta(chatDelta{ReasoningContent: fragment})
	}
	for _, fragment := range chunkText(turn.Text, turn.Chunks) {
		delta(chatDelta{Content: fragment})
	}
	for index, call := range turn.Calls {
		arguments, err := argumentsOf(call)
		if err != nil {
			return nil, err
		}

		delta(chatDelta{ToolCalls: []chatToolCallDelta{{
			Index:    index,
			ID:       callID(call, sequence, index),
			Type:     wireFunction,
			Function: chatFunctionFragment{Name: call.Name},
		}}})
		for _, fragment := range fragmentArguments(arguments, turn.Chunks) {
			delta(chatDelta{ToolCalls: []chatToolCallDelta{{
				Index:    index,
				Function: chatFunctionFragment{Arguments: fragment},
			}}})
		}
	}

	if turn.Usage != nil {
		usage := &chatUsage{
			PromptTokens:     turn.Usage.InputTokens,
			CompletionTokens: turn.Usage.OutputTokens,
		}
		if turn.Usage.CacheReadTokens > 0 {
			usage.PromptDetails = &chatCachedTokens{CachedTokens: turn.Usage.CacheReadTokens}
		}
		if turn.Usage.ReasoningTokens > 0 {
			usage.CompletionDetails = &chatReasoningTokens{
				ReasoningTokens: turn.Usage.ReasoningTokens,
			}
		}
		builder.add(chatCompletionChunk{
			ID:      id,
			Model:   model,
			Choices: []chatCompletionChunkChoice{},
			Usage:   usage,
		})
	}
	if !turn.Truncate {
		choice(chatCompletionChunkChoice{FinishReason: chatFinishReason(turn)})
		builder.addRaw(chatDone)
	}

	payloads, err := builder.done()
	if err != nil {
		return nil, fmt.Errorf("chat completions payload: %w", err)
	}
	return payloads, nil
}

// complete returns the complete JSON body of one chat completion, which a
// non-streamed call receives. It carries the same answer and usage as the
// streamed form.
func (chatStream) complete(turn Turn, sequence int, model string) (string, error) {
	message := map[string]any{"role": wireAssistant}
	if turn.Text != "" {
		message["content"] = turn.Text
	} else {
		message["content"] = nil
	}
	if turn.Thinking != "" {
		message["reasoning_content"] = turn.Thinking
	}
	if len(turn.Calls) > 0 {
		calls := make([]map[string]any, 0, len(turn.Calls))
		for index, call := range turn.Calls {
			arguments, err := argumentsOf(call)
			if err != nil {
				return "", err
			}
			calls = append(calls, map[string]any{
				"id":   callID(call, sequence, index),
				"type": wireFunction,
				"function": map[string]any{
					"name":      call.Name,
					"arguments": arguments,
				},
			})
		}
		message["tool_calls"] = calls
	}

	response := map[string]any{
		"id":    responseID("chatcmpl", sequence),
		"model": model,
		"choices": []any{map[string]any{
			"index":         0,
			wireMessage:     message,
			"finish_reason": chatFinishReason(turn),
		}},
	}
	if turn.Usage != nil {
		usage := map[string]any{
			"prompt_tokens":     turn.Usage.InputTokens,
			"completion_tokens": turn.Usage.OutputTokens,
		}
		if turn.Usage.CacheReadTokens > 0 {
			usage["prompt_tokens_details"] = map[string]any{
				"cached_tokens": turn.Usage.CacheReadTokens,
			}
		}
		if turn.Usage.ReasoningTokens > 0 {
			usage["completion_tokens_details"] = map[string]any{
				"reasoning_tokens": turn.Usage.ReasoningTokens,
			}
		}
		response["usage"] = usage
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("chat completions complete response: %w", err)
	}
	return string(encoded), nil
}

// chatCompletionChunk is one streamed chunk of a completion.
type chatCompletionChunk struct {
	ID      string                      `json:"id"`
	Model   string                      `json:"model"`
	Choices []chatCompletionChunkChoice `json:"choices"`
	Usage   *chatUsage                  `json:"usage,omitempty"`
}

// chatCompletionChunkChoice carries the delta of one candidate completion.
type chatCompletionChunkChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

// chatDelta is the incremental content of one chunk.
type chatDelta struct {
	Role             string              `json:"role,omitempty"`
	Content          string              `json:"content,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCallDelta `json:"tool_calls,omitempty"`
}

// chatToolCallDelta is one streamed fragment of a tool invocation. Fragments
// sharing an index belong to the same call.
type chatToolCallDelta struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function chatFunctionFragment `json:"function"`
}

// chatFunctionFragment carries the streamed name and arguments of a call.
type chatFunctionFragment struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// chatUsage mirrors the wire usage object.
type chatUsage struct {
	PromptTokens      int                  `json:"prompt_tokens"`
	CompletionTokens  int                  `json:"completion_tokens"`
	PromptDetails     *chatCachedTokens    `json:"prompt_tokens_details,omitempty"`
	CompletionDetails *chatReasoningTokens `json:"completion_tokens_details,omitempty"`
}

// chatCachedTokens reports the input tokens served from the provider cache.
type chatCachedTokens struct {
	CachedTokens int `json:"cached_tokens"`
}

// chatReasoningTokens reports the output tokens spent on reasoning.
type chatReasoningTokens struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ChatRequest is the decoded body of a captured Chat Completions request.
type ChatRequest struct {
	// Model is the wire identifier the request generates with.
	Model string `json:"model"`

	// Messages is the conversation replayed to the provider.
	Messages []ChatMessage `json:"messages"`

	// Tools declares the tools the model may invoke.
	Tools []ChatTool `json:"tools"`

	// ToolChoice constrains tool usage when the application sends one.
	ToolChoice json.RawMessage `json:"tool_choice"`

	// ParallelToolCalls allows the model to request several calls per turn.
	ParallelToolCalls *bool `json:"parallel_tool_calls"`

	// MaxCompletionTokens caps the generated tokens.
	MaxCompletionTokens int `json:"max_completion_tokens"`

	// Temperature is the sampling temperature, nil when unset.
	Temperature *float64 `json:"temperature"`

	// TopP is the nucleus sampling, nil when unset.
	TopP *float64 `json:"top_p"`

	// ReasoningEffort selects the reasoning effort level.
	ReasoningEffort string `json:"reasoning_effort"`

	// Stream reports whether the request asks for a streamed response.
	Stream bool `json:"stream"`

	// StreamOptions carries the streaming options of the request.
	StreamOptions *ChatStreamOptions `json:"stream_options"`
}

// ChatStreamOptions mirrors the streaming options of the wire format.
type ChatStreamOptions struct {
	// IncludeUsage asks the provider to report usage in the stream.
	IncludeUsage bool `json:"include_usage"`
}

// ChatMessage is one decoded wire conversation turn.
type ChatMessage struct {
	// Role is the author of the message.
	Role string `json:"role"`

	// Content is the text of the message, nil when the message carries none.
	Content *string `json:"content"`

	// ToolCalls lists the invocations an assistant message requests.
	ToolCalls []ChatToolCall `json:"tool_calls"`

	// ToolCallID references the invocation a tool result answers.
	ToolCallID string `json:"tool_call_id"`
}

// Text returns the text content of the message, empty when it carries none.
func (m ChatMessage) Text() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

// ChatTool is one decoded tool declaration.
type ChatTool struct {
	// Type is the kind of the tool, always "function".
	Type string `json:"type"`

	// Function describes the callable function.
	Function ChatFunction `json:"function"`
}

// ChatFunction describes one callable function.
type ChatFunction struct {
	// Name is the tool name.
	Name string `json:"name"`

	// Description explains the tool to the model.
	Description string `json:"description"`

	// Parameters is the JSON Schema of the arguments.
	Parameters json.RawMessage `json:"parameters"`
}

// ChatToolCall is one decoded tool invocation of an assistant message.
type ChatToolCall struct {
	// ID is the identifier of the invocation.
	ID string `json:"id"`

	// Type is the kind of the invocation, always "function".
	Type string `json:"type"`

	// Function carries the invoked name and its arguments.
	Function ChatFunctionCall `json:"function"`
}

// ChatFunctionCall carries the name and arguments of an invocation.
type ChatFunctionCall struct {
	// Name is the invoked tool.
	Name string `json:"name"`

	// Arguments holds the invocation arguments as a JSON string.
	Arguments string `json:"arguments"`
}

// Chat decodes a captured request as a Chat Completions payload, failing the
// test when the body does not follow the protocol.
func (r Request) Chat(t *testing.T) *ChatRequest {
	t.Helper()

	var decoded ChatRequest
	if err := json.Unmarshal(r.Body, &decoded); err != nil {
		t.Fatalf("decode chat completions request: %v (body: %s)", err, r.Body)
	}
	return &decoded
}

// ToolNames returns the names of the tools a request declares, in order.
func (r *ChatRequest) ToolNames() []string {
	names := make([]string, 0, len(r.Tools))
	for _, tool := range r.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

// Roles returns the roles of the replayed messages, in order.
func (r *ChatRequest) Roles() []string {
	roles := make([]string, 0, len(r.Messages))
	for _, message := range r.Messages {
		roles = append(roles, message.Role)
	}
	return roles
}
