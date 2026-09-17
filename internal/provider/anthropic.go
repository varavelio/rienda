package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/transport"
)

const (
	anthropicProviderName     = "anthropic"
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 8192
)

// Anthropic wire block types used more than once.
const (
	// wireBlockText is the Anthropic text block type.
	wireBlockText = "text"
	// wireBlockThinking is the Anthropic thinking block type.
	wireBlockThinking = "thinking"
	// wireBlockRedactedThinking is the Anthropic redacted thinking block type.
	wireBlockRedactedThinking = "redacted_thinking"
	// wireBlockToolUse is the Anthropic tool use block type.
	wireBlockToolUse = "tool_use"
)

// anthropicClient is an llm.Client for the Anthropic Messages API.
//
// Requests fall back to anthropicDefaultMaxTokens when llm.Request.MaxTokens is
// unset, and temperature is dropped whenever extended thinking is enabled, as
// the API rejects both together.
type anthropicClient struct {
	http    *http.Client
	baseURL string
}

// NewAnthropic returns an llm.Client speaking the Anthropic Messages API.
func NewAnthropic(cfg Config) llm.Client {
	return &anthropicClient{
		http: cfg.httpClient(
			transport.HeaderAuth{Header: "x-api-key", Value: cfg.APIKey},
			map[string]string{"anthropic-version": anthropicVersion},
		),
		baseURL: normalizeBaseURL(cfg.BaseURL),
	}
}

var _ llm.Client = (*anthropicClient)(nil)

// Generate returns the full response once generation completes.
func (c *anthropicClient) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req == nil {
		return nil, errors.New("anthropic: request is nil")
	}
	wire, err := anthropicRequestFrom(req, false)
	if err != nil {
		return nil, err
	}
	var raw anthropicResponse
	if err := postJSON(
		ctx,
		c.http,
		anthropicProviderName,
		c.baseURL+"/messages",
		wire,
		&raw,
	); err != nil {
		return nil, err
	}
	return anthropicResponseTo(&raw), nil
}

// Stream returns a handle to consume the response incrementally.
func (c *anthropicClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if req == nil {
		return nil, errors.New("anthropic: request is nil")
	}
	wire, err := anthropicRequestFrom(req, true)
	if err != nil {
		return nil, err
	}
	body, err := postStream(ctx, c.http, anthropicProviderName, c.baseURL+"/messages", wire)
	if err != nil {
		return nil, err
	}
	return &anthropicStream{scanner: transport.NewSSEScanner(body), body: body}, nil
}

// anthropicRequest is the wire payload for POST /messages.
type anthropicRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"`
	System      string               `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Tools       []anthropicTool      `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
	Temperature *float64             `json:"temperature,omitempty"`
	TopP        *float64             `json:"top_p,omitempty"`
	Thinking    *anthropicThinking   `json:"thinking,omitempty"`
	Stream      bool                 `json:"stream,omitempty"`
}

// anthropicMessage is a single wire conversation turn.
type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

// anthropicBlock is a wire content block. Only the fields valid for Type are
// populated.
type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// anthropicTool is a wire tool definition.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
	Strict      bool            `json:"strict,omitempty"`
}

// anthropicToolChoice is a wire tool choice constraint.
type anthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

// anthropicThinking enables extended thinking with a token budget.
type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// anthropicResponse is the wire payload of a completed message.
type anthropicResponse struct {
	ID         string           `json:"id"`
	Model      string           `json:"model"`
	Content    []anthropicBlock `json:"content"`
	StopReason string           `json:"stop_reason"`
	Usage      anthropicUsage   `json:"usage"`
}

// anthropicUsage mirrors the wire usage object.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// anthropicStreamEnvelope is a single SSE data payload.
type anthropicStreamEnvelope struct {
	Type         string             `json:"type"`
	Message      *anthropicResponse `json:"message,omitempty"`
	Index        *int               `json:"index,omitempty"`
	ContentBlock *anthropicBlock    `json:"content_block,omitempty"`
	Delta        *anthropicDelta    `json:"delta,omitempty"`
	Usage        *anthropicUsage    `json:"usage,omitempty"`
	Error        *wireError         `json:"error,omitempty"`
}

// anthropicDelta is a content_block_delta or message_delta payload.
type anthropicDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// anthropicRequestFrom translates a canonical request into the wire format.
func anthropicRequestFrom(req *llm.Request, stream bool) (*anthropicRequest, error) {
	wire := &anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Stream:    stream,
	}
	if wire.MaxTokens <= 0 {
		wire.MaxTokens = anthropicDefaultMaxTokens
	}
	for _, message := range req.Messages {
		switch message.Role {
		case llm.RoleAssistant:
			wire.Messages = append(wire.Messages, anthropicMessage{
				Role:    wireRoleAssistant,
				Content: anthropicAssistantBlocks(message.Blocks),
			})
		default:
			wire.Messages = append(wire.Messages, anthropicMessage{
				Role:    wireRoleUser,
				Content: anthropicUserBlocks(message.Blocks),
			})
		}
	}
	for _, tool := range req.Tools {
		schema := tool.Parameters
		if len(schema) == 0 {
			schema = emptyJSONObject
		}
		wire.Tools = append(wire.Tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
			Strict:      tool.Strict,
		})
	}
	wire.ToolChoice = anthropicToolChoiceFromRequest(req)
	if req.Reasoning != nil && req.Reasoning.BudgetTokens > 0 {
		// The API rejects temperature alongside thinking.
		wire.Thinking = &anthropicThinking{
			Type:         "enabled",
			BudgetTokens: req.Reasoning.BudgetTokens,
		}
	} else {
		wire.Temperature = req.Temperature
	}
	wire.TopP = req.TopP
	return wire, nil
}

// anthropicToolChoiceFromRequest builds the wire tool choice for a request,
// including the parallel tool use flag when the request sets one. The API only
// accepts that flag for the auto and any modes, so it is dropped for the rest.
func anthropicToolChoiceFromRequest(req *llm.Request) *anthropicToolChoice {
	if len(req.Tools) == 0 {
		return nil
	}
	if req.ToolChoice == nil && req.ParallelToolCalls == nil {
		return nil
	}
	choice := anthropicToolChoiceFrom(req.ToolChoice)
	if req.ParallelToolCalls != nil &&
		(choice.Type == wireToolChoiceAuto || choice.Type == "any") {
		disable := !*req.ParallelToolCalls
		choice.DisableParallelToolUse = &disable
	}
	return choice
}

// anthropicToolChoiceFrom translates a canonical tool choice. A nil choice
// becomes the provider default.
func anthropicToolChoiceFrom(choice *llm.ToolChoice) *anthropicToolChoice {
	if choice == nil {
		return &anthropicToolChoice{Type: wireToolChoiceAuto}
	}
	switch choice.Mode {
	case llm.ToolChoiceNone:
		return &anthropicToolChoice{Type: wireToolChoiceNone}
	case llm.ToolChoiceRequired:
		return &anthropicToolChoice{Type: "any"}
	case llm.ToolChoiceTool:
		return &anthropicToolChoice{Type: "tool", Name: choice.ToolName}
	default:
		return &anthropicToolChoice{Type: wireToolChoiceAuto}
	}
}

// anthropicAssistantBlocks maps canonical blocks valid in assistant turns.
func anthropicAssistantBlocks(blocks []llm.Block) []anthropicBlock {
	var out []anthropicBlock
	for _, block := range blocks {
		switch block.Type {
		case llm.BlockText:
			out = append(out, anthropicBlock{Type: wireBlockText, Text: block.Text})
		case llm.BlockThinking:
			out = append(out, anthropicBlock{
				Type:      wireBlockThinking,
				Thinking:  block.Thinking,
				Signature: block.ThinkingSignature,
			})
		case llm.BlockRedactedThinking:
			out = append(out, anthropicBlock{
				Type: wireBlockRedactedThinking,
				Data: block.ThinkingRedactedData,
			})
		case llm.BlockToolCall:
			args := block.ToolCallArguments
			if len(args) == 0 || string(args) == wireJSONNull {
				args = emptyJSONObject
			}
			out = append(out, anthropicBlock{
				Type:  wireBlockToolUse,
				ID:    block.ToolCallID,
				Name:  block.ToolCallName,
				Input: args,
			})
		}
	}
	return out
}

// anthropicUserBlocks maps canonical blocks valid in user turns.
func anthropicUserBlocks(blocks []llm.Block) []anthropicBlock {
	var out []anthropicBlock
	for _, block := range blocks {
		switch block.Type {
		case llm.BlockText:
			out = append(out, anthropicBlock{Type: wireBlockText, Text: block.Text})
		case llm.BlockToolResult:
			content := any("")
			if len(block.ToolResult) > 0 {
				inner := anthropicUserBlocks(block.ToolResult)
				if len(inner) > 0 {
					content = inner
				}
			}
			out = append(out, anthropicBlock{
				Type:      "tool_result",
				ToolUseID: block.ToolResultCallID,
				Content:   content,
				IsError:   block.ToolResultIsError,
			})
		}
	}
	return out
}

// anthropicResponseTo translates a completed wire message into canonical form.
func anthropicResponseTo(raw *anthropicResponse) *llm.Response {
	resp := &llm.Response{
		ID:         raw.ID,
		Model:      raw.Model,
		StopReason: anthropicStopReasonTo(raw.StopReason),
		Usage: llm.Usage{
			InputTokens:      raw.Usage.InputTokens,
			OutputTokens:     raw.Usage.OutputTokens,
			CacheReadTokens:  raw.Usage.CacheReadInputTokens,
			CacheWriteTokens: raw.Usage.CacheCreationInputTokens,
		},
	}
	for _, block := range raw.Content {
		switch block.Type {
		case wireBlockText:
			resp.Blocks = append(resp.Blocks, llm.Block{Type: llm.BlockText, Text: block.Text})
		case wireBlockThinking:
			resp.Blocks = append(resp.Blocks, llm.Block{
				Type:              llm.BlockThinking,
				Thinking:          block.Thinking,
				ThinkingSignature: block.Signature,
			})
		case wireBlockRedactedThinking:
			resp.Blocks = append(resp.Blocks, llm.Block{
				Type:                 llm.BlockRedactedThinking,
				ThinkingRedactedData: block.Data,
			})
		case wireBlockToolUse:
			args := block.Input
			if len(args) == 0 || string(args) == wireJSONNull {
				args = emptyJSONObject
			}
			resp.Blocks = append(resp.Blocks, llm.Block{
				Type:              llm.BlockToolCall,
				ToolCallID:        block.ID,
				ToolCallName:      block.Name,
				ToolCallArguments: args,
			})
		}
	}
	return resp
}

// anthropicStopReasonTo maps wire stop reasons to canonical ones. Unknown
// reasons collapse to end_turn.
func anthropicStopReasonTo(reason string) llm.StopReason {
	switch reason {
	case "end_turn":
		return llm.StopReasonEndTurn
	case "max_tokens":
		return llm.StopReasonMaxTokens
	case "tool_use":
		return llm.StopReasonToolUse
	case "stop_sequence":
		return llm.StopReasonStopSequence
	case "refusal":
		return llm.StopReasonRefusal
	case "pause_turn":
		return llm.StopReasonPaused
	default:
		return llm.StopReasonEndTurn
	}
}

// anthropicStream is a pull-based llm.Stream over an Anthropic SSE response.
type anthropicStream struct {
	scanner *transport.SSEScanner
	body    io.ReadCloser
	toolIDs map[int]string
	usage   llm.Usage
	stop    string
	done    bool
	failed  error
	closed  bool
}

var _ llm.Stream = (*anthropicStream)(nil)

// Next blocks until the next event arrives. It returns io.EOF once the
// response completed.
func (s *anthropicStream) Next() (llm.StreamEvent, error) {
	if s.failed != nil {
		return llm.StreamEvent{}, s.failed
	}
	if s.done {
		return llm.StreamEvent{}, io.EOF
	}
	for {
		raw, err := s.scanner.Next()
		if err != nil {
			s.failed = anthropicStreamError(err)
			return llm.StreamEvent{}, s.failed
		}
		var envelope anthropicStreamEnvelope
		if err := json.Unmarshal([]byte(raw.Data), &envelope); err != nil {
			s.failed = fmt.Errorf("anthropic: decode stream event: %w", err)
			return llm.StreamEvent{}, s.failed
		}
		event, terminal, err := s.translate(envelope)
		if err != nil {
			s.failed = err
			return llm.StreamEvent{}, s.failed
		}
		if terminal {
			s.done = true
		}
		if event != nil {
			return *event, nil
		}
	}
}

// Close releases the underlying response body. It is safe to call multiple times.
func (s *anthropicStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.body.Close(); err != nil {
		return fmt.Errorf("anthropic: close stream: %w", err)
	}
	return nil
}

// anthropicStreamError maps scanner failures. A clean EOF without message_stop
// means the stream was truncated.
func anthropicStreamError(err error) error {
	if errors.Is(err, io.EOF) {
		return errors.New("anthropic: stream ended before message_stop")
	}
	return fmt.Errorf("anthropic: read stream: %w", err)
}

// translate converts one SSE envelope into a canonical event. It returns a nil
// event for envelopes carrying no consumer-visible update.
func (s *anthropicStream) translate(
	envelope anthropicStreamEnvelope,
) (*llm.StreamEvent, bool, error) {
	switch envelope.Type {
	case "message_start":
		if envelope.Message != nil {
			s.usage.InputTokens = envelope.Message.Usage.InputTokens
			s.usage.CacheReadTokens = envelope.Message.Usage.CacheReadInputTokens
			s.usage.CacheWriteTokens = envelope.Message.Usage.CacheCreationInputTokens
			return &llm.StreamEvent{
				Type:  llm.StreamMessageStart,
				ID:    envelope.Message.ID,
				Model: envelope.Message.Model,
			}, false, nil
		}
	case "content_block_start":
		if envelope.ContentBlock != nil && envelope.ContentBlock.Type == wireBlockRedactedThinking {
			return &llm.StreamEvent{
				Type:                 llm.StreamThinkingRedacted,
				ThinkingRedactedData: envelope.ContentBlock.Data,
			}, false, nil
		}
		if envelope.ContentBlock != nil && envelope.ContentBlock.Type == wireBlockToolUse &&
			envelope.Index != nil {
			if s.toolIDs == nil {
				s.toolIDs = map[int]string{}
			}
			s.toolIDs[*envelope.Index] = envelope.ContentBlock.ID
			return &llm.StreamEvent{
				Type:         llm.StreamToolCallStart,
				ToolCallID:   envelope.ContentBlock.ID,
				ToolCallName: envelope.ContentBlock.Name,
			}, false, nil
		}
	case "content_block_delta":
		return s.translateDelta(envelope), false, nil
	case "content_block_stop":
		if envelope.Index != nil {
			delete(s.toolIDs, *envelope.Index)
		}
	case "message_delta":
		if envelope.Delta != nil {
			s.stop = envelope.Delta.StopReason
		}
		if envelope.Usage != nil {
			s.usage.OutputTokens = envelope.Usage.OutputTokens
		}
	case "message_stop":
		return &llm.StreamEvent{
			Type:       llm.StreamMessageEnd,
			StopReason: anthropicStopReasonTo(s.stop),
			Usage:      s.usage,
		}, true, nil
	case "error":
		if envelope.Error != nil && envelope.Error.Message != "" {
			return nil, false, newError(
				anthropicProviderName,
				0,
				envelope.Error.Type,
				envelope.Error.Code,
				envelope.Error.Message,
			)
		}
		return nil, false, &llm.Error{Provider: anthropicProviderName, Message: "stream error"}
	case "ping":
		// Keep-alive, no consumer-visible update.
	}
	return nil, false, nil
}

// translateDelta converts a content_block_delta envelope into a canonical event.
func (s *anthropicStream) translateDelta(envelope anthropicStreamEnvelope) *llm.StreamEvent {
	if envelope.Delta == nil {
		return nil
	}
	switch envelope.Delta.Type {
	case "text_delta":
		return &llm.StreamEvent{Type: llm.StreamTextDelta, Text: envelope.Delta.Text}
	case "thinking_delta":
		return &llm.StreamEvent{Type: llm.StreamThinkingDelta, Thinking: envelope.Delta.Thinking}
	case "signature_delta":
		return &llm.StreamEvent{
			Type:              llm.StreamThinkingDelta,
			ThinkingSignature: envelope.Delta.Signature,
		}
	case "input_json_delta":
		var toolID string
		if envelope.Index != nil {
			toolID = s.toolIDs[*envelope.Index]
		}
		return &llm.StreamEvent{
			Type:              llm.StreamToolCallArgsDelta,
			ToolCallID:        toolID,
			ToolCallArgsDelta: envelope.Delta.PartialJSON,
		}
	default:
		return nil
	}
}
