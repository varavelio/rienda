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
	openAIResponsesProviderName = "openai-responses"
	openAIResponsesPath         = "/responses"
)

const (
	// wireItemReasoning is the Responses reasoning item type.
	wireItemReasoning = "reasoning"
	// wireItemMessage is the Responses message item type.
	wireItemMessage = "message"
	// wireSummaryText is the Responses reasoning summary part type.
	wireSummaryText = "summary_text"
	// wireStatusCompleted marks a finished output item.
	wireStatusCompleted = "completed"
)

// openAIResponsesClient is an llm.Client for the OpenAI Responses API.
//
// Conversations are stateless: every turn replays the full history through
// input items. Thinking blocks replay only when they carry Signature, sent as
// the encrypted content of a reasoning item together with its identifier and
// summary; signature-less thinking, redacted thinking and bare BudgetTokens
// values have no wire equivalent and are dropped. Responses are created with
// store disabled so the provider retains nothing server-side.
type openAIResponsesClient struct {
	http    *http.Client
	baseURL string
}

// NewOpenAIResponses returns an llm.Client speaking the OpenAI Responses API.
func NewOpenAIResponses(cfg Config) llm.Client {
	return &openAIResponsesClient{
		http: cfg.httpClient(
			transport.BearerAuth{Token: cfg.APIKey},
			nil,
		),
		baseURL: normalizeBaseURL(cfg.BaseURL),
	}
}

var _ llm.Client = (*openAIResponsesClient)(nil)

// Generate returns the full response once generation completes.
func (c *openAIResponsesClient) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	if req == nil {
		return nil, errors.New("openai-responses: request is nil")
	}
	wire := openAIResponsesRequestFrom(req, false)
	var raw openAIResponsesResponse
	if err := postJSON(
		ctx,
		c.http,
		openAIResponsesProviderName,
		c.baseURL+openAIResponsesPath,
		wire,
		&raw,
	); err != nil {
		return nil, err
	}
	return openAIResponsesResponseTo(&raw)
}

// Stream returns a handle to consume the response incrementally.
func (c *openAIResponsesClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if req == nil {
		return nil, errors.New("openai-responses: request is nil")
	}
	wire := openAIResponsesRequestFrom(req, true)
	body, err := postStream(
		ctx,
		c.http,
		openAIResponsesProviderName,
		c.baseURL+openAIResponsesPath,
		wire,
	)
	if err != nil {
		return nil, err
	}
	return &openAIResponsesStream{scanner: transport.NewSSEScanner(body), body: body}, nil
}

// openAIResponsesRequest is the wire payload for POST /responses.
type openAIResponsesRequest struct {
	Model             string                     `json:"model"`
	Instructions      string                     `json:"instructions,omitempty"`
	Input             []openAIResponsesInputItem `json:"input"`
	Tools             []openAIResponsesTool      `json:"tools,omitempty"`
	ToolChoice        any                        `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                      `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens   int                        `json:"max_output_tokens,omitempty"`
	Temperature       *float64                   `json:"temperature,omitempty"`
	TopP              *float64                   `json:"top_p,omitempty"`
	Reasoning         *openAIResponsesReasoning  `json:"reasoning,omitempty"`
	Store             *bool                      `json:"store,omitempty"`
	Stream            bool                       `json:"stream,omitempty"`
}

// openAIResponsesInputItem is one typed input item. Only the fields valid for
// Type are populated.
type openAIResponsesInputItem struct {
	Type             string                        `json:"type"`
	ID               string                        `json:"id,omitempty"`
	Role             string                        `json:"role,omitempty"`
	Status           string                        `json:"status,omitempty"`
	Content          []openAIResponsesContentPart  `json:"content,omitempty"`
	CallID           string                        `json:"call_id,omitempty"`
	Name             string                        `json:"name,omitempty"`
	Arguments        string                        `json:"arguments,omitempty"`
	Output           string                        `json:"output,omitempty"`
	EncryptedContent string                        `json:"encrypted_content,omitempty"`
	Summary          *[]openAIResponsesSummaryPart `json:"summary,omitempty"`
}

// openAIResponsesContentPart is a message content part.
type openAIResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// Annotations and Logprobs are required by output text parts. They are
	// left nil, and therefore omitted, by input text parts.
	Annotations json.RawMessage `json:"annotations,omitempty"`
	Logprobs    json.RawMessage `json:"logprobs,omitempty"`
}

// openAIResponsesSummaryPart is a reasoning summary fragment.
type openAIResponsesSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// openAIResponsesTool is a wire function tool definition.
type openAIResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// openAIResponsesReasoning configures reasoning effort.
type openAIResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// openAIResponsesResponse is the wire payload of a completed response.
type openAIResponsesResponse struct {
	ID                string                            `json:"id"`
	Model             string                            `json:"model"`
	Status            string                            `json:"status"`
	IncompleteDetails *openAIResponsesIncompleteDetails `json:"incomplete_details,omitempty"`
	Error             *openAIResponsesError             `json:"error,omitempty"`
	Output            []openAIResponsesOutputItem       `json:"output"`
	Usage             openAIResponsesUsage              `json:"usage"`
}

// openAIResponsesIncompleteDetails explains an incomplete status.
type openAIResponsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

// openAIResponsesError carries a failed response error.
type openAIResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// openAIResponsesOutputItem is one typed output item. Only the fields valid
// for Type are populated.
type openAIResponsesOutputItem struct {
	Type             string                       `json:"type"`
	ID               string                       `json:"id,omitempty"`
	Role             string                       `json:"role,omitempty"`
	Content          []openAIResponsesContentPart `json:"content,omitempty"`
	Summary          []openAIResponsesSummaryPart `json:"summary,omitempty"`
	EncryptedContent string                       `json:"encrypted_content,omitempty"`
	CallID           string                       `json:"call_id,omitempty"`
	Name             string                       `json:"name,omitempty"`
	Arguments        string                       `json:"arguments,omitempty"`
}

// openAIResponsesUsage mirrors the wire usage object.
type openAIResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	InputDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// openAIResponsesStreamEnvelope is a single SSE data payload.
type openAIResponsesStreamEnvelope struct {
	Type        string                     `json:"type"`
	Response    *openAIResponsesResponse   `json:"response,omitempty"`
	Item        *openAIResponsesOutputItem `json:"item,omitempty"`
	ItemID      string                     `json:"item_id,omitempty"`
	OutputIndex *int                       `json:"output_index,omitempty"`
	Delta       string                     `json:"delta,omitempty"`
}

// openAIResponsesRequestFrom translates a canonical request into the wire format.
func openAIResponsesRequestFrom(req *llm.Request, stream bool) *openAIResponsesRequest {
	store := false
	wire := &openAIResponsesRequest{
		Model:           req.Model,
		Instructions:    req.System,
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Store:           &store,
		Stream:          stream,
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		wire.Reasoning = &openAIResponsesReasoning{Effort: req.Reasoning.Effort}
	}
	for _, message := range req.Messages {
		wire.Input = append(wire.Input, openAIResponsesItemsFrom(message)...)
	}
	for _, tool := range req.Tools {
		schema := tool.Parameters
		if len(schema) == 0 {
			schema = emptyJSONObject
		}
		wire.Tools = append(wire.Tools, openAIResponsesTool{
			Type:        wireToolFunction,
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
			Strict:      tool.Strict,
		})
	}
	if len(wire.Tools) > 0 && req.ToolChoice != nil {
		wire.ToolChoice = openAIToolChoiceFrom(req.ToolChoice)
	}
	wire.ParallelToolCalls = req.ParallelToolCalls
	return wire
}

// openAIResponsesItemsFrom expands one canonical message into wire input items.
// Text accumulates into a single message item; tool calls, reasoning and tool
// outputs become standalone items preserving order.
func openAIResponsesItemsFrom(message llm.Message) []openAIResponsesInputItem {
	assistant := message.Role == llm.RoleAssistant
	role := wireRoleUser
	partType := "input_text"
	if assistant {
		role = wireRoleAssistant
		partType = "output_text"
	}
	var out []openAIResponsesInputItem
	var parts []openAIResponsesContentPart
	flush := func() {
		if len(parts) == 0 {
			return
		}
		item := openAIResponsesInputItem{Type: wireItemMessage, Role: role, Content: parts}
		if assistant {
			item.ID = message.ItemID
			item.Status = wireStatusCompleted
		}
		out = append(out, item)
		parts = nil
	}
	for _, block := range message.Blocks {
		switch block.Type {
		case llm.BlockText:
			part := openAIResponsesContentPart{Type: partType, Text: block.Text}
			if assistant {
				part.Annotations = emptyJSONArray
				part.Logprobs = emptyJSONArray
			}
			parts = append(parts, part)
		case llm.BlockThinking:
			if !assistant || block.ThinkingSignature == "" {
				continue
			}
			flush()
			out = append(out, openAIResponsesInputItem{
				Type:             wireItemReasoning,
				ID:               block.ThinkingID,
				EncryptedContent: block.ThinkingSignature,
				Summary:          reasoningSummary(block.Thinking),
			})
		case llm.BlockToolCall:
			if !assistant {
				continue
			}
			flush()
			args := string(block.ToolCallArguments)
			if args == "" || args == wireJSONNull {
				args = string(emptyJSONObject)
			}
			out = append(out, openAIResponsesInputItem{
				Type:      wireFunctionCall,
				CallID:    block.ToolCallID,
				Name:      block.ToolCallName,
				Arguments: args,
			})
		case llm.BlockToolResult:
			if assistant {
				continue
			}
			flush()
			out = append(out, openAIResponsesInputItem{
				Type:   "function_call_output",
				CallID: block.ToolResultCallID,
				Output: concatTextBlocks(block.ToolResult),
			})
		}
	}
	flush()
	return out
}

// reasoningSummary builds the summary parts of a reasoning input item. The
// field is required by the wire schema, so an absent summary becomes an empty
// list instead of being omitted.
func reasoningSummary(text string) *[]openAIResponsesSummaryPart {
	parts := make([]openAIResponsesSummaryPart, 0, 1)
	if text != "" {
		parts = append(parts, openAIResponsesSummaryPart{Type: wireSummaryText, Text: text})
	}
	return &parts
}

// openAIResponsesResponseTo translates a completed wire response into canonical
// form, or an *llm.Error when the response failed.
func openAIResponsesResponseTo(raw *openAIResponsesResponse) (*llm.Response, error) {
	if raw.Status == "failed" {
		return nil, openAIResponsesFailure(raw)
	}
	resp := &llm.Response{
		ID:         raw.ID,
		Model:      raw.Model,
		StopReason: openAIResponsesStopReason(raw),
		Usage:      openAIResponsesUsageTo(raw.Usage),
	}
	for _, item := range raw.Output {
		switch item.Type {
		case wireItemMessage:
			if resp.ItemID == "" {
				resp.ItemID = item.ID
			}
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					resp.Blocks = append(
						resp.Blocks,
						llm.Block{Type: llm.BlockText, Text: part.Text},
					)
				}
			}
		case wireItemReasoning:
			var thinking strings.Builder
			for _, part := range item.Summary {
				thinking.WriteString(part.Text)
			}
			if thinking.Len() > 0 || item.EncryptedContent != "" {
				resp.Blocks = append(resp.Blocks, llm.Block{
					Type:              llm.BlockThinking,
					Thinking:          thinking.String(),
					ThinkingSignature: item.EncryptedContent,
					ThinkingID:        item.ID,
				})
			}
		case wireFunctionCall:
			args := item.Arguments
			if args == "" || args == wireJSONNull {
				args = string(emptyJSONObject)
			}
			resp.Blocks = append(resp.Blocks, llm.Block{
				Type:              llm.BlockToolCall,
				ToolCallID:        item.CallID,
				ToolCallName:      item.Name,
				ToolCallArguments: json.RawMessage(args),
			})
		}
	}
	return resp, nil
}

// openAIResponsesStopReason derives the canonical stop reason. Tool calls take
// precedence; unknown states collapse to end_turn.
func openAIResponsesStopReason(raw *openAIResponsesResponse) llm.StopReason {
	for _, item := range raw.Output {
		if item.Type == wireFunctionCall {
			return llm.StopReasonToolUse
		}
	}
	if raw.Status == "incomplete" && raw.IncompleteDetails != nil {
		switch raw.IncompleteDetails.Reason {
		case "max_output_tokens":
			return llm.StopReasonMaxTokens
		case "content_filter":
			return llm.StopReasonContentFilter
		}
	}
	return llm.StopReasonEndTurn
}

// openAIResponsesFailure maps a failed response into an *llm.Error.
func openAIResponsesFailure(raw *openAIResponsesResponse) *llm.Error {
	message := "response failed"
	code := ""
	if raw.Error != nil {
		code = raw.Error.Code
		if raw.Error.Message != "" {
			message = raw.Error.Message
		}
	}
	return newError(openAIResponsesProviderName, 0, "", code, message)
}

// openAIResponsesUsageTo maps a wire usage object to canonical form.
func openAIResponsesUsageTo(raw openAIResponsesUsage) llm.Usage {
	return llm.Usage{
		InputTokens:      raw.InputTokens,
		OutputTokens:     raw.OutputTokens,
		ReasoningTokens:  raw.OutputDetails.ReasoningTokens,
		CacheReadTokens:  raw.InputDetails.CachedTokens,
		CacheWriteTokens: raw.InputDetails.CacheWriteTokens,
	}
}

// openAIResponsesStream is a pull-based llm.Stream over a Responses SSE response.
type openAIResponsesStream struct {
	scanner       *transport.SSEScanner
	body          io.ReadCloser
	calls         map[string]string
	messageItemID string
	ended         bool
	failed        error
	closed        bool
}

var _ llm.Stream = (*openAIResponsesStream)(nil)

// Next blocks until the next event arrives. It returns io.EOF once the
// response completed.
func (s *openAIResponsesStream) Next() (llm.StreamEvent, error) {
	if s.failed != nil {
		return llm.StreamEvent{}, s.failed
	}
	if s.ended {
		return llm.StreamEvent{}, io.EOF
	}
	for {
		raw, err := s.scanner.Next()
		if err != nil {
			s.failed = openAIResponsesStreamError(err)
			return llm.StreamEvent{}, s.failed
		}
		var envelope openAIResponsesStreamEnvelope
		if err := json.Unmarshal([]byte(raw.Data), &envelope); err != nil {
			s.failed = fmt.Errorf("openai-responses: decode stream event: %w", err)
			return llm.StreamEvent{}, s.failed
		}
		if envelope.Type == "" {
			envelope.Type = raw.Name
		}
		event, terminal, err := s.translate(envelope)
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
func (s *openAIResponsesStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.body.Close(); err != nil {
		return fmt.Errorf("openai-responses: close stream: %w", err)
	}
	return nil
}

// openAIResponsesStreamError maps scanner failures. A clean EOF without a
// terminal event means the stream was truncated.
func openAIResponsesStreamError(err error) error {
	if errors.Is(err, io.EOF) {
		return errors.New("openai-responses: stream ended before completion")
	}
	return fmt.Errorf("openai-responses: read stream: %w", err)
}

// translate converts one SSE envelope into a canonical event. It returns a nil
// event for envelopes carrying no consumer-visible update.
func (s *openAIResponsesStream) translate(
	envelope openAIResponsesStreamEnvelope,
) (*llm.StreamEvent, bool, error) {
	switch envelope.Type {
	case "response.created":
		if envelope.Response != nil {
			return &llm.StreamEvent{
				Type:  llm.StreamMessageStart,
				ID:    envelope.Response.ID,
				Model: envelope.Response.Model,
			}, false, nil
		}
	case "response.output_item.added":
		if envelope.Item != nil && envelope.Item.Type == wireFunctionCall {
			if s.calls == nil {
				s.calls = map[string]string{}
			}
			s.calls[envelope.Item.ID] = envelope.Item.CallID
			return &llm.StreamEvent{
				Type:         llm.StreamToolCallStart,
				ToolCallID:   envelope.Item.CallID,
				ToolCallName: envelope.Item.Name,
			}, false, nil
		}
	case "response.output_text.delta":
		// Track the message item the text belongs to, so the identifier is
		// available even when the stream carries no output_item.done event.
		if s.messageItemID == "" {
			s.messageItemID = envelope.ItemID
		}
		return &llm.StreamEvent{Type: llm.StreamTextDelta, Text: envelope.Delta}, false, nil
	case "response.reasoning_summary_text.delta":
		return &llm.StreamEvent{
			Type:       llm.StreamThinkingDelta,
			Thinking:   envelope.Delta,
			ThinkingID: envelope.ItemID,
		}, false, nil
	case "response.function_call_arguments.delta":
		return &llm.StreamEvent{
			Type:              llm.StreamToolCallArgsDelta,
			ToolCallID:        s.calls[envelope.ItemID],
			ToolCallArgsDelta: envelope.Delta,
		}, false, nil
	case "response.output_item.done":
		if envelope.Item == nil {
			return nil, false, nil
		}
		switch envelope.Item.Type {
		case wireItemReasoning:
			if envelope.Item.EncryptedContent != "" {
				// Surface the encrypted reasoning so consumers can replay it
				// together with the identifier of its item.
				return &llm.StreamEvent{
					Type:              llm.StreamThinkingDelta,
					ThinkingSignature: envelope.Item.EncryptedContent,
					ThinkingID:        envelope.Item.ID,
				}, false, nil
			}
		case wireItemMessage:
			// Surface the identifier of the message item, which must travel
			// with the text so the message can be replayed.
			if s.messageItemID == "" {
				s.messageItemID = envelope.Item.ID
			}
		}
	case "response.completed", "response.incomplete":
		if envelope.Response == nil {
			return nil, false, fmt.Errorf("openai-responses: %s without response", envelope.Type)
		}
		return &llm.StreamEvent{
			Type:       llm.StreamMessageEnd,
			ItemID:     s.messageItemID,
			StopReason: openAIResponsesStopReason(envelope.Response),
			Usage:      openAIResponsesUsageTo(envelope.Response.Usage),
		}, true, nil
	case "response.failed":
		if envelope.Response != nil {
			return nil, false, openAIResponsesFailure(envelope.Response)
		}
		return nil, false, newError(openAIResponsesProviderName, 0, "", "", "response failed")
	}
	return nil, false, nil
}
