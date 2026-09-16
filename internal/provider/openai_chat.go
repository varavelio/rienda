package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/transport"
)

const (
	openAIChatProviderName    = "openai-chat"
	openAIChatCompletionsPath = "/chat/completions"
)

// openAIChatClient is an llm.Client for the OpenAI Chat Completions API and
// its many compatible implementations.
//
// Only the first choice (index 0) is consumed; parallel completions (n > 1)
// are not supported. Thinking blocks are dropped when sending, since the
// protocol has no standard representation for them, and tool error flags have
// no wire equivalent either. A ReasoningConfig carries only Effort; a bare
// BudgetTokens value has no effect here.
type openAIChatClient struct {
	http    *http.Client
	baseURL string
}

// NewOpenAIChat returns an llm.Client speaking the OpenAI Chat Completions API.
func NewOpenAIChat(cfg Config) llm.Client {
	return &openAIChatClient{
		http: cfg.httpClient(
			transport.BearerAuth{Token: cfg.APIKey},
			nil,
		),
		baseURL: normalizeBaseURL(cfg.BaseURL),
	}
}

var _ llm.Client = (*openAIChatClient)(nil)

// Generate returns the full response once generation completes.
func (c *openAIChatClient) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req == nil {
		return nil, errors.New("openai-chat: request is nil")
	}
	wire := openAIChatRequestFrom(req, false)
	var raw openAIChatResponse
	if err := postJSON(
		ctx,
		c.http,
		openAIChatProviderName,
		c.baseURL+openAIChatCompletionsPath,
		wire,
		&raw,
	); err != nil {
		return nil, err
	}
	return openAIChatResponseTo(&raw), nil
}

// Stream returns a handle to consume the response incrementally.
func (c *openAIChatClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if req == nil {
		return nil, errors.New("openai-chat: request is nil")
	}
	wire := openAIChatRequestFrom(req, true)
	body, err := postStream(
		ctx,
		c.http,
		openAIChatProviderName,
		c.baseURL+openAIChatCompletionsPath,
		wire,
	)
	if err != nil {
		return nil, err
	}
	return &openAIChatStream{scanner: transport.NewSSEScanner(body), body: body}, nil
}

// openAIChatRequest is the wire payload for POST /chat/completions.
type openAIChatRequest struct {
	Model               string                   `json:"model"`
	Messages            []openAIChatMessage      `json:"messages"`
	Tools               []openAIChatTool         `json:"tools,omitempty"`
	ToolChoice          any                      `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                    `json:"parallel_tool_calls,omitempty"`
	MaxCompletionTokens int                      `json:"max_completion_tokens,omitempty"`
	Temperature         *float64                 `json:"temperature,omitempty"`
	TopP                *float64                 `json:"top_p,omitempty"`
	Stop                []string                 `json:"stop,omitempty"`
	ReasoningEffort     string                   `json:"reasoning_effort,omitempty"`
	Stream              bool                     `json:"stream,omitempty"`
	StreamOptions       *openAIChatStreamOptions `json:"stream_options,omitempty"`
}

// openAIChatStreamOptions requests usage reporting in the final chunk.
type openAIChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// openAIChatMessage is a single wire conversation turn.
type openAIChatMessage struct {
	Role       string               `json:"role"`
	Content    any                  `json:"content,omitempty"`
	ToolCalls  []openAIChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

// openAIChatTool is a wire function tool definition.
type openAIChatTool struct {
	Type     string                `json:"type"`
	Function openAIChatFunctionDef `json:"function"`
}

// openAIChatFunctionDef describes a callable function.
type openAIChatFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// openAIChatToolCall is a model-requested tool invocation.
type openAIChatToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIChatFunctionCall `json:"function"`
}

// openAIChatFunctionCall carries the invoked function name and arguments.
type openAIChatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIChatResponse is the wire payload of a completed chat completion.
type openAIChatResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Choices []openAIChatChoice `json:"choices"`
	Usage   openAIChatUsage    `json:"usage"`
}

// openAIChatChoice is a single wire completion candidate.
type openAIChatChoice struct {
	Index        int                  `json:"index"`
	Message      openAIChatMessageOut `json:"message"`
	FinishReason string               `json:"finish_reason"`
}

// openAIChatMessageOut is a wire assistant message, including the nonstandard
// reasoning_content field some compatible providers emit.
type openAIChatMessageOut struct {
	Role             string               `json:"role"`
	Content          *string              `json:"content"`
	ReasoningContent string               `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIChatToolCall `json:"tool_calls,omitempty"`
}

// openAIChatUsage mirrors the wire usage object.
type openAIChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// openAIChatChunk is a single streamed delta payload.
type openAIChatChunk struct {
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
	Choices []openAIChatChunkChoice `json:"choices"`
	Usage   *openAIChatUsage        `json:"usage,omitempty"`
	Error   *wireError              `json:"error,omitempty"`
}

// openAIChatChunkChoice carries one choice delta, including the nonstandard
// reasoning_content fragment some compatible providers emit.
type openAIChatChunkChoice struct {
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
	Delta        struct {
		Role             string                    `json:"role,omitempty"`
		Content          string                    `json:"content,omitempty"`
		ReasoningContent string                    `json:"reasoning_content,omitempty"`
		ToolCalls        []openAIChatChunkToolCall `json:"tool_calls,omitempty"`
	} `json:"delta"`
}

// openAIChatChunkToolCall is a streamed tool call fragment. Deltas sharing an
// index belong to the same call; ID and name usually arrive in the first one.
type openAIChatChunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// openAIChatRequestFrom translates a canonical request into the wire format.
func openAIChatRequestFrom(req *llm.Request, stream bool) *openAIChatRequest {
	wire := &openAIChatRequest{
		Model:               req.Model,
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		Stop:                req.StopSequences,
		Stream:              stream,
	}
	if req.Reasoning != nil {
		wire.ReasoningEffort = req.Reasoning.Effort
	}
	if stream {
		wire.StreamOptions = &openAIChatStreamOptions{IncludeUsage: true}
	}
	if req.System != "" {
		wire.Messages = append(
			wire.Messages,
			openAIChatMessage{Role: "system", Content: req.System},
		)
	}
	for _, message := range req.Messages {
		wire.Messages = append(wire.Messages, openAIChatMessagesFrom(message)...)
	}
	for _, tool := range req.Tools {
		schema := tool.Parameters
		if len(schema) == 0 {
			schema = emptyJSONObject
		}
		wire.Tools = append(wire.Tools, openAIChatTool{
			Type: wireToolFunction,
			Function: openAIChatFunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
				Strict:      tool.Strict,
			},
		})
	}
	if len(wire.Tools) > 0 && req.ToolChoice != nil {
		wire.ToolChoice = openAIToolChoiceFrom(req.ToolChoice)
	}
	wire.ParallelToolCalls = req.ParallelToolCalls
	return wire
}

// openAIToolChoiceFrom translates a canonical tool choice. The Chat
// Completions and Responses APIs share this shape.
func openAIToolChoiceFrom(choice *llm.ToolChoice) any {
	switch choice.Mode {
	case llm.ToolChoiceNone:
		return wireToolChoiceNone
	case llm.ToolChoiceRequired:
		return "required"
	case llm.ToolChoiceTool:
		return map[string]any{
			"type":     wireToolFunction,
			"function": map[string]any{"name": choice.ToolName},
		}
	default:
		return wireToolChoiceAuto
	}
}

// openAIChatMessagesFrom expands one canonical message into wire messages.
// Tool results become standalone tool messages; surrounding text is kept in
// user messages preserving order.
func openAIChatMessagesFrom(message llm.Message) []openAIChatMessage {
	switch message.Role {
	case llm.RoleAssistant:
		var text strings.Builder
		var calls []openAIChatToolCall
		for _, block := range message.Blocks {
			switch block.Type {
			case llm.BlockText:
				text.WriteString(block.Text)
			case llm.BlockToolCall:
				args := string(block.ToolCallArguments)
				if args == "" || args == wireJSONNull {
					args = string(emptyJSONObject)
				}
				calls = append(calls, openAIChatToolCall{
					ID:   block.ToolCallID,
					Type: wireToolFunction,
					Function: openAIChatFunctionCall{
						Name:      block.ToolCallName,
						Arguments: args,
					},
				})
			}
		}
		out := openAIChatMessage{Role: wireRoleAssistant, ToolCalls: calls}
		if content := text.String(); content != "" {
			out.Content = content
		}
		return []openAIChatMessage{out}
	default:
		var out []openAIChatMessage
		var text strings.Builder
		flush := func() {
			if content := text.String(); content != "" {
				out = append(out, openAIChatMessage{Role: wireRoleUser, Content: content})
				text.Reset()
			}
		}
		for _, block := range message.Blocks {
			switch block.Type {
			case llm.BlockText:
				text.WriteString(block.Text)
			case llm.BlockToolResult:
				flush()
				out = append(out, openAIChatMessage{
					Role:       "tool",
					Content:    concatTextBlocks(block.ToolResult),
					ToolCallID: block.ToolResultCallID,
				})
			}
		}
		flush()
		return out
	}
}

// concatTextBlocks concatenates the text of text blocks. It is shared by the
// OpenAI clients to flatten tool outputs and system prompts.
func concatTextBlocks(blocks []llm.Block) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == llm.BlockText {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// openAIChatUsageTo maps a wire usage object to canonical form.
func openAIChatUsageTo(raw openAIChatUsage) llm.Usage {
	return llm.Usage{
		InputTokens:     raw.PromptTokens,
		OutputTokens:    raw.CompletionTokens,
		ReasoningTokens: raw.CompletionDetails.ReasoningTokens,
		CacheReadTokens: raw.PromptDetails.CachedTokens,
	}
}

// openAIChatResponseTo translates a completed wire response into canonical form.
func openAIChatResponseTo(raw *openAIChatResponse) *llm.Response {
	resp := &llm.Response{
		ID:    raw.ID,
		Model: raw.Model,
		Usage: openAIChatUsageTo(raw.Usage),
	}
	if len(raw.Choices) == 0 {
		return resp
	}
	choice := raw.Choices[0]
	resp.StopReason = openAIChatStopReasonTo(choice.FinishReason)
	if choice.Message.Content != nil && *choice.Message.Content != "" {
		resp.Blocks = append(
			resp.Blocks,
			llm.Block{Type: llm.BlockText, Text: *choice.Message.Content},
		)
	}
	if choice.Message.ReasoningContent != "" {
		resp.Blocks = append(
			resp.Blocks,
			llm.Block{Type: llm.BlockThinking, Thinking: choice.Message.ReasoningContent},
		)
	}
	for _, call := range choice.Message.ToolCalls {
		args := call.Function.Arguments
		if args == "" || args == wireJSONNull {
			args = string(emptyJSONObject)
		}
		resp.Blocks = append(resp.Blocks, llm.Block{
			Type:              llm.BlockToolCall,
			ToolCallID:        call.ID,
			ToolCallName:      call.Function.Name,
			ToolCallArguments: json.RawMessage(args),
		})
	}
	return resp
}

// openAIChatStopReasonTo maps wire finish reasons to canonical ones. Unknown
// reasons collapse to end_turn.
func openAIChatStopReasonTo(reason string) llm.StopReason {
	switch reason {
	case "stop":
		return llm.StopReasonEndTurn
	case "length":
		return llm.StopReasonMaxTokens
	case "tool_calls", wireFunctionCall:
		return llm.StopReasonToolUse
	case "content_filter":
		return llm.StopReasonContentFilter
	default:
		return llm.StopReasonEndTurn
	}
}

// openAIChatStream is a pull-based llm.Stream over a Chat Completions SSE response.
type openAIChatStream struct {
	scanner *transport.SSEScanner
	body    io.ReadCloser
	started bool
	ended   bool
	failed  error
	closed  bool
	tools   map[int]*openAIChatStreamTool
	usage   llm.Usage
	stop    string
	id      string
	model   string
}

// openAIChatStreamTool accumulates one streamed tool call by chunk index.
type openAIChatStreamTool struct {
	id   string
	name string
}

var _ llm.Stream = (*openAIChatStream)(nil)

// Next blocks until the next event arrives. It returns io.EOF once the
// response completed.
func (s *openAIChatStream) Next() (llm.StreamEvent, error) {
	if s.failed != nil {
		return llm.StreamEvent{}, s.failed
	}
	if s.ended {
		return llm.StreamEvent{}, io.EOF
	}
	for {
		raw, err := s.scanner.Next()
		if err != nil {
			s.failed = openAIChatStreamError(err)
			return llm.StreamEvent{}, s.failed
		}
		if raw.Data == "[DONE]" {
			s.ended = true
			if s.stop == "" {
				s.failed = errors.New("openai-chat: stream ended before finish_reason")
				return llm.StreamEvent{}, s.failed
			}
			return llm.StreamEvent{}, io.EOF
		}
		var chunk openAIChatChunk
		if err := json.Unmarshal([]byte(raw.Data), &chunk); err != nil {
			s.failed = fmt.Errorf("openai-chat: decode stream chunk: %w", err)
			return llm.StreamEvent{}, s.failed
		}
		event, terminal, err := s.translate(chunk)
		if err != nil {
			s.failed = err
			return llm.StreamEvent{}, s.failed
		}
		if terminal {
			s.ended = true
		}
		if event != nil {
			return *event, nil
		}
	}
}

// Close releases the underlying response body. It is safe to call multiple times.
func (s *openAIChatStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.body.Close(); err != nil {
		return fmt.Errorf("openai-chat: close stream: %w", err)
	}
	return nil
}

// openAIChatStreamError maps scanner failures. A clean EOF without [DONE]
// means the stream was truncated.
func openAIChatStreamError(err error) error {
	if errors.Is(err, io.EOF) {
		return errors.New("openai-chat: stream ended before [DONE]")
	}
	return fmt.Errorf("openai-chat: read stream: %w", err)
}

// translate converts one chunk into a canonical event. It returns a nil event
// for chunks carrying no consumer-visible update.
func (s *openAIChatStream) translate(
	chunk openAIChatChunk,
) (*llm.StreamEvent, bool, error) {
	if chunk.Error != nil && chunk.Error.Message != "" {
		return nil, false, newError(
			openAIChatProviderName,
			0,
			chunk.Error.Type,
			chunk.Error.Code,
			chunk.Error.Message,
		)
	}
	if !s.started {
		s.started = true
		s.id = chunk.ID
		s.model = chunk.Model
		return &llm.StreamEvent{
			Type:  llm.StreamMessageStart,
			ID:    chunk.ID,
			Model: chunk.Model,
		}, false, nil
	}
	if chunk.Usage != nil {
		s.usage = openAIChatUsageTo(*chunk.Usage)
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		}
		if choice.FinishReason != "" {
			s.stop = choice.FinishReason
			return &llm.StreamEvent{
				Type:       llm.StreamMessageEnd,
				StopReason: openAIChatStopReasonTo(s.stop),
				Usage:      s.usage,
			}, true, nil
		}
		if choice.Delta.Content != "" {
			return &llm.StreamEvent{
				Type: llm.StreamTextDelta,
				Text: choice.Delta.Content,
			}, false, nil
		}
		if choice.Delta.ReasoningContent != "" {
			return &llm.StreamEvent{
				Type:     llm.StreamThinkingDelta,
				Thinking: choice.Delta.ReasoningContent,
			}, false, nil
		}
		for _, call := range choice.Delta.ToolCalls {
			return s.translateToolCall(call), false, nil
		}
	}
	return nil, false, nil
}

// translateToolCall converts one tool call fragment, emitting a start event on
// first sight of its index and argument deltas afterwards.
func (s *openAIChatStream) translateToolCall(call openAIChatChunkToolCall) *llm.StreamEvent {
	if s.tools == nil {
		s.tools = map[int]*openAIChatStreamTool{}
	}
	tool, seen := s.tools[call.Index]
	if !seen {
		tool = &openAIChatStreamTool{id: call.ID, name: call.Function.Name}
		s.tools[call.Index] = tool
		return &llm.StreamEvent{
			Type:         llm.StreamToolCallStart,
			ToolCallID:   call.ID,
			ToolCallName: call.Function.Name,
		}
	}
	if tool.id == "" && call.ID != "" {
		tool.id = call.ID
	}
	if tool.name == "" && call.Function.Name != "" {
		tool.name = call.Function.Name
	}
	return &llm.StreamEvent{
		Type:              llm.StreamToolCallArgsDelta,
		ToolCallID:        tool.id,
		ToolCallArgsDelta: call.Function.Arguments,
	}
}
