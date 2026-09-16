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
	openAIChatCompletionsProviderName = "openai-chat-completions"
	openAIChatCompletionsPath         = "/chat/completions"
)

// openAIChatCompletionsClient is an llm.Client for the OpenAI Chat Completions
// API and its many compatible implementations.
//
// Only the first choice (index 0) is consumed; parallel completions (n > 1)
// are not supported. Thinking blocks are dropped when sending, since the
// protocol has no standard representation for them, and tool error flags have
// no wire equivalent either. A ReasoningConfig carries only Effort; a bare
// BudgetTokens value has no effect here.
type openAIChatCompletionsClient struct {
	http    *http.Client
	baseURL string
}

// NewOpenAIChatCompletions returns an llm.Client speaking the OpenAI Chat
// Completions API.
func NewOpenAIChatCompletions(cfg Config) llm.Client {
	return &openAIChatCompletionsClient{
		http: cfg.httpClient(
			transport.BearerAuth{Token: cfg.APIKey},
			nil,
		),
		baseURL: normalizeBaseURL(cfg.BaseURL),
	}
}

var _ llm.Client = (*openAIChatCompletionsClient)(nil)

// Generate returns the full response once generation completes.
func (c *openAIChatCompletionsClient) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	if req == nil {
		return nil, errors.New("openai-chat-completions: request is nil")
	}
	wire := openAIChatCompletionsRequestFrom(req, false)
	var raw openAIChatCompletionsResponse
	if err := postJSON(
		ctx,
		c.http,
		openAIChatCompletionsProviderName,
		c.baseURL+openAIChatCompletionsPath,
		wire,
		&raw,
	); err != nil {
		return nil, err
	}
	return openAIChatCompletionsResponseTo(&raw), nil
}

// Stream returns a handle to consume the response incrementally.
func (c *openAIChatCompletionsClient) Stream(
	ctx context.Context,
	req *llm.Request,
) (llm.Stream, error) {
	if req == nil {
		return nil, errors.New("openai-chat-completions: request is nil")
	}
	wire := openAIChatCompletionsRequestFrom(req, true)
	body, err := postStream(
		ctx,
		c.http,
		openAIChatCompletionsProviderName,
		c.baseURL+openAIChatCompletionsPath,
		wire,
	)
	if err != nil {
		return nil, err
	}
	return &openAIChatCompletionsStream{scanner: transport.NewSSEScanner(body), body: body}, nil
}

// openAIChatCompletionsRequest is the wire payload for POST /chat/completions.
type openAIChatCompletionsRequest struct {
	Model               string                              `json:"model"`
	Messages            []openAIChatCompletionsMessage      `json:"messages"`
	Tools               []openAIChatCompletionsTool         `json:"tools,omitempty"`
	ToolChoice          any                                 `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                               `json:"parallel_tool_calls,omitempty"`
	MaxCompletionTokens int                                 `json:"max_completion_tokens,omitempty"`
	Temperature         *float64                            `json:"temperature,omitempty"`
	TopP                *float64                            `json:"top_p,omitempty"`
	ReasoningEffort     string                              `json:"reasoning_effort,omitempty"`
	Stream              bool                                `json:"stream,omitempty"`
	StreamOptions       *openAIChatCompletionsStreamOptions `json:"stream_options,omitempty"`
}

// openAIChatCompletionsStreamOptions requests usage reporting in the final chunk.
type openAIChatCompletionsStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// openAIChatCompletionsMessage is a single wire conversation turn.
type openAIChatCompletionsMessage struct {
	Role       string                          `json:"role"`
	Content    any                             `json:"content,omitempty"`
	ToolCalls  []openAIChatCompletionsToolCall `json:"tool_calls,omitempty"`
	ToolCallID string                          `json:"tool_call_id,omitempty"`
}

// openAIChatCompletionsTool is a wire function tool definition.
type openAIChatCompletionsTool struct {
	Type     string                           `json:"type"`
	Function openAIChatCompletionsFunctionDef `json:"function"`
}

// openAIChatCompletionsFunctionDef describes a callable function.
type openAIChatCompletionsFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// openAIChatCompletionsToolCall is a model-requested tool invocation.
type openAIChatCompletionsToolCall struct {
	ID       string                            `json:"id"`
	Type     string                            `json:"type"`
	Function openAIChatCompletionsFunctionCall `json:"function"`
}

// openAIChatCompletionsFunctionCall carries the invoked function name and arguments.
type openAIChatCompletionsFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIChatCompletionsResponse is the wire payload of a completed chat completion.
type openAIChatCompletionsResponse struct {
	ID      string                        `json:"id"`
	Model   string                        `json:"model"`
	Choices []openAIChatCompletionsChoice `json:"choices"`
	Usage   openAIChatCompletionsUsage    `json:"usage"`
}

// openAIChatCompletionsChoice is a single wire completion candidate.
type openAIChatCompletionsChoice struct {
	Index        int                             `json:"index"`
	Message      openAIChatCompletionsMessageOut `json:"message"`
	FinishReason string                          `json:"finish_reason"`
}

// openAIChatCompletionsMessageOut is a wire assistant message, including the nonstandard
// reasoning_content field some compatible providers emit.
type openAIChatCompletionsMessageOut struct {
	Role             string                          `json:"role"`
	Content          *string                         `json:"content"`
	ReasoningContent string                          `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIChatCompletionsToolCall `json:"tool_calls,omitempty"`
}

// openAIChatCompletionsUsage mirrors the wire usage object.
type openAIChatCompletionsUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// openAIChatCompletionsChunk is a single streamed delta payload.
type openAIChatCompletionsChunk struct {
	ID      string                             `json:"id"`
	Model   string                             `json:"model"`
	Choices []openAIChatCompletionsChunkChoice `json:"choices"`
	Usage   *openAIChatCompletionsUsage        `json:"usage,omitempty"`
	Error   *wireError                         `json:"error,omitempty"`
}

// openAIChatCompletionsChunkChoice carries one choice delta, including the nonstandard
// reasoning_content fragment some compatible providers emit.
type openAIChatCompletionsChunkChoice struct {
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
	Delta        struct {
		Role             string                               `json:"role,omitempty"`
		Content          string                               `json:"content,omitempty"`
		ReasoningContent string                               `json:"reasoning_content,omitempty"`
		ToolCalls        []openAIChatCompletionsChunkToolCall `json:"tool_calls,omitempty"`
	} `json:"delta"`
}

// openAIChatCompletionsChunkToolCall is a streamed tool call fragment. Deltas sharing an
// index belong to the same call; ID and name usually arrive in the first one.
type openAIChatCompletionsChunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// openAIChatCompletionsRequestFrom translates a canonical request into the wire format.
func openAIChatCompletionsRequestFrom(req *llm.Request, stream bool) *openAIChatCompletionsRequest {
	wire := &openAIChatCompletionsRequest{
		Model:               req.Model,
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		Stream:              stream,
	}
	if req.Reasoning != nil {
		wire.ReasoningEffort = req.Reasoning.Effort
	}
	if stream {
		wire.StreamOptions = &openAIChatCompletionsStreamOptions{IncludeUsage: true}
	}
	if req.System != "" {
		wire.Messages = append(
			wire.Messages,
			openAIChatCompletionsMessage{Role: "system", Content: req.System},
		)
	}
	for _, message := range req.Messages {
		wire.Messages = append(wire.Messages, openAIChatCompletionsMessagesFrom(message)...)
	}
	for _, tool := range req.Tools {
		schema := tool.Parameters
		if len(schema) == 0 {
			schema = emptyJSONObject
		}
		wire.Tools = append(wire.Tools, openAIChatCompletionsTool{
			Type: wireToolFunction,
			Function: openAIChatCompletionsFunctionDef{
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

// openAIChatCompletionsMessagesFrom expands one canonical message into wire messages.
// Tool results become standalone tool messages; surrounding text is kept in
// user messages preserving order.
func openAIChatCompletionsMessagesFrom(message llm.Message) []openAIChatCompletionsMessage {
	switch message.Role {
	case llm.RoleAssistant:
		var text strings.Builder
		var calls []openAIChatCompletionsToolCall
		for _, block := range message.Blocks {
			switch block.Type {
			case llm.BlockText:
				text.WriteString(block.Text)
			case llm.BlockToolCall:
				args := string(block.ToolCallArguments)
				if args == "" || args == wireJSONNull {
					args = string(emptyJSONObject)
				}
				calls = append(calls, openAIChatCompletionsToolCall{
					ID:   block.ToolCallID,
					Type: wireToolFunction,
					Function: openAIChatCompletionsFunctionCall{
						Name:      block.ToolCallName,
						Arguments: args,
					},
				})
			}
		}
		out := openAIChatCompletionsMessage{Role: wireRoleAssistant, ToolCalls: calls}
		if content := text.String(); content != "" {
			out.Content = content
		}
		return []openAIChatCompletionsMessage{out}
	default:
		var out []openAIChatCompletionsMessage
		var text strings.Builder
		flush := func() {
			if content := text.String(); content != "" {
				out = append(
					out,
					openAIChatCompletionsMessage{Role: wireRoleUser, Content: content},
				)
				text.Reset()
			}
		}
		for _, block := range message.Blocks {
			switch block.Type {
			case llm.BlockText:
				text.WriteString(block.Text)
			case llm.BlockToolResult:
				flush()
				out = append(out, openAIChatCompletionsMessage{
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

// openAIChatCompletionsUsageTo maps a wire usage object to canonical form.
func openAIChatCompletionsUsageTo(raw openAIChatCompletionsUsage) llm.Usage {
	return llm.Usage{
		InputTokens:     raw.PromptTokens,
		OutputTokens:    raw.CompletionTokens,
		ReasoningTokens: raw.CompletionDetails.ReasoningTokens,
		CacheReadTokens: raw.PromptDetails.CachedTokens,
	}
}

// openAIChatCompletionsResponseTo translates a completed wire response into canonical form.
func openAIChatCompletionsResponseTo(raw *openAIChatCompletionsResponse) *llm.Response {
	resp := &llm.Response{
		ID:    raw.ID,
		Model: raw.Model,
		Usage: openAIChatCompletionsUsageTo(raw.Usage),
	}
	if len(raw.Choices) == 0 {
		return resp
	}
	choice := raw.Choices[0]
	resp.StopReason = openAIChatCompletionsStopReasonTo(choice.FinishReason)
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

// openAIChatCompletionsStopReasonTo maps wire finish reasons to canonical ones. Unknown
// reasons collapse to end_turn.
func openAIChatCompletionsStopReasonTo(reason string) llm.StopReason {
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

// openAIChatCompletionsStream is a pull-based llm.Stream over a Chat Completions SSE response.
type openAIChatCompletionsStream struct {
	scanner *transport.SSEScanner
	body    io.ReadCloser
	started bool
	ended   bool
	failed  error
	closed  bool
	tools   map[int]*openAIChatCompletionsStreamTool
	usage   llm.Usage
	stop    string
	id      string
	model   string
}

// openAIChatCompletionsStreamTool accumulates one streamed tool call by chunk index.
type openAIChatCompletionsStreamTool struct {
	id   string
	name string
}

var _ llm.Stream = (*openAIChatCompletionsStream)(nil)

// Next blocks until the next event arrives. It returns io.EOF once the
// response completed.
func (s *openAIChatCompletionsStream) Next() (llm.StreamEvent, error) {
	if s.failed != nil {
		return llm.StreamEvent{}, s.failed
	}
	if s.ended {
		return llm.StreamEvent{}, io.EOF
	}
	for {
		raw, err := s.scanner.Next()
		if err != nil {
			s.failed = openAIChatCompletionsStreamError(err)
			return llm.StreamEvent{}, s.failed
		}
		if raw.Data == "[DONE]" {
			s.ended = true
			if s.stop == "" {
				s.failed = errors.New("openai-chat-completions: stream ended before finish_reason")
				return llm.StreamEvent{}, s.failed
			}
			return llm.StreamEvent{}, io.EOF
		}
		var chunk openAIChatCompletionsChunk
		if err := json.Unmarshal([]byte(raw.Data), &chunk); err != nil {
			s.failed = fmt.Errorf("openai-chat-completions: decode stream chunk: %w", err)
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
func (s *openAIChatCompletionsStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.body.Close(); err != nil {
		return fmt.Errorf("openai-chat-completions: close stream: %w", err)
	}
	return nil
}

// openAIChatCompletionsStreamError maps scanner failures. A clean EOF without [DONE]
// means the stream was truncated.
func openAIChatCompletionsStreamError(err error) error {
	if errors.Is(err, io.EOF) {
		return errors.New("openai-chat-completions: stream ended before [DONE]")
	}
	return fmt.Errorf("openai-chat-completions: read stream: %w", err)
}

// translate converts one chunk into a canonical event. It returns a nil event
// for chunks carrying no consumer-visible update.
func (s *openAIChatCompletionsStream) translate(
	chunk openAIChatCompletionsChunk,
) (*llm.StreamEvent, bool, error) {
	if chunk.Error != nil && chunk.Error.Message != "" {
		return nil, false, newError(
			openAIChatCompletionsProviderName,
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
		s.usage = openAIChatCompletionsUsageTo(*chunk.Usage)
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		}
		if choice.FinishReason != "" {
			s.stop = choice.FinishReason
			return &llm.StreamEvent{
				Type:       llm.StreamMessageEnd,
				StopReason: openAIChatCompletionsStopReasonTo(s.stop),
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
func (s *openAIChatCompletionsStream) translateToolCall(
	call openAIChatCompletionsChunkToolCall,
) *llm.StreamEvent {
	if s.tools == nil {
		s.tools = map[int]*openAIChatCompletionsStreamTool{}
	}
	tool, seen := s.tools[call.Index]
	if !seen {
		tool = &openAIChatCompletionsStreamTool{id: call.ID, name: call.Function.Name}
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
