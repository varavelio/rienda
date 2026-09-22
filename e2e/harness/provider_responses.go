//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Item identifiers and item types of the Responses protocol. Identifiers are
// built from the sequence of the request that produced them, so a test can
// assert the ones the application stores and replays.
const (
	// responsesReasoningPrefix opens the identifier of a reasoning item.
	responsesReasoningPrefix = "rs"
	// responsesMessagePrefix opens the identifier of a message item.
	responsesMessagePrefix = "msg"
	// responsesCallPrefix opens the identifier of a function call item.
	responsesCallPrefix = "fc"
	// responsesEncryptedPrefix opens the encrypted reasoning content a
	// reasoning item carries, so tests can assert the signature the
	// application stores and replays.
	responsesEncryptedPrefix = "encrypted"
	// responsesCompleted marks a completed output item of the protocol.
	responsesCompleted = "completed"
	// responsesMessageItem is the message item type of the protocol.
	responsesMessageItem = "message"
)

// responsesStream is the codec of the OpenAI Responses protocol.
type responsesStream struct{}

// payloads turns a scripted turn into the events of a streamed response. Every
// output item is announced, streamed and completed in order, and the closing
// response event carries the items and the usage exactly as the Responses API
// reports them.
func (responsesStream) payloads(turn Turn, sequence int, model string) ([]string, error) {
	builder := &payloadBuilder{}
	id := responseID("resp", sequence)

	builder.add(responsesEnvelope{
		Type:     "response.created",
		Response: &responsesPayload{ID: id, Model: model, Status: "in_progress"},
	})

	output := make([]responsesItem, 0, 3)
	outputIndex := 0

	if turn.Thinking != "" {
		itemID := responseID(responsesReasoningPrefix, sequence)
		signature := responseID(responsesEncryptedPrefix, sequence)
		index := outputIndex
		outputIndex++

		builder.add(responsesItemEvent("response.output_item.added", index, responsesItem{
			Type: wireReasoning,
			ID:   itemID,
		}))
		for _, fragment := range chunkText(turn.Thinking, turn.Chunks) {
			builder.add(responsesEnvelope{
				Type:        "response.reasoning_summary_text.delta",
				ItemID:      itemID,
				OutputIndex: &index,
				Delta:       fragment,
			})
		}

		item := responsesItem{
			Type:             wireReasoning,
			ID:               itemID,
			Status:           responsesCompleted,
			Summary:          []responsesSummaryPart{{Type: "summary_text", Text: turn.Thinking}},
			EncryptedContent: signature,
		}
		builder.add(responsesItemEvent("response.output_item.done", index, item))
		output = append(output, item)
	}

	if turn.Text != "" {
		itemID := responseID(responsesMessagePrefix, sequence)
		index := outputIndex
		outputIndex++

		builder.add(responsesItemEvent("response.output_item.added", index, responsesItem{
			Type: responsesMessageItem,
			ID:   itemID,
			Role: wireAssistant,
		}))
		for _, fragment := range chunkText(turn.Text, turn.Chunks) {
			builder.add(responsesEnvelope{
				Type:        "response.output_text.delta",
				ItemID:      itemID,
				OutputIndex: &index,
				Delta:       fragment,
			})
		}

		item := responsesItem{
			Type:    "message",
			ID:      itemID,
			Role:    "assistant",
			Status:  "completed",
			Content: []responsesContentPart{{Type: "output_text", Text: turn.Text}},
		}
		builder.add(responsesItemEvent("response.output_item.done", index, item))
		output = append(output, item)
	}

	for callIndex, call := range turn.Calls {
		arguments, err := argumentsOf(call)
		if err != nil {
			return nil, err
		}

		itemID := fmt.Sprintf("%s_%d_%d", responsesCallPrefix, sequence, callIndex+1)
		index := outputIndex
		outputIndex++

		builder.add(responsesItemEvent("response.output_item.added", index, responsesItem{
			Type:   wireFunctionCall,
			ID:     itemID,
			CallID: callID(call, sequence, callIndex),
			Name:   call.Name,
		}))
		for _, fragment := range fragmentArguments(arguments, turn.Chunks) {
			builder.add(responsesEnvelope{
				Type:        "response.function_call_arguments.delta",
				ItemID:      itemID,
				OutputIndex: &index,
				Delta:       fragment,
			})
		}

		item := responsesItem{
			Type:      wireFunctionCall,
			ID:        itemID,
			CallID:    callID(call, sequence, callIndex),
			Name:      call.Name,
			Arguments: arguments,
			Status:    responsesCompleted,
		}
		builder.add(responsesItemEvent("response.output_item.done", index, item))
		output = append(output, item)
	}

	if !turn.Truncate {
		payload := &responsesPayload{
			ID:     id,
			Model:  model,
			Status: responsesCompleted,
			Output: output,
		}
		if turn.Usage != nil {
			payload.Usage = &responsesUsage{
				InputTokens:  turn.Usage.InputTokens,
				OutputTokens: turn.Usage.OutputTokens,
			}
		}
		builder.add(responsesEnvelope{Type: "response.completed", Response: payload})
	}

	payloads, err := builder.done()
	if err != nil {
		return nil, fmt.Errorf("responses payload: %w", err)
	}
	return payloads, nil
}

// complete returns the complete JSON body of one response, which a
// non-streamed call receives.
func (responsesStream) complete(turn Turn, sequence int, model string) (string, error) {
	output := make([]responsesItem, 0, 3)
	if turn.Thinking != "" {
		output = append(output, responsesItem{
			Type:             wireReasoning,
			ID:               responseID(responsesReasoningPrefix, sequence),
			Status:           responsesCompleted,
			Summary:          []responsesSummaryPart{{Type: "summary_text", Text: turn.Thinking}},
			EncryptedContent: responseID(responsesEncryptedPrefix, sequence),
		})
	}
	if turn.Text != "" {
		output = append(output, responsesItem{
			Type:    responsesMessageItem,
			ID:      responseID(responsesMessagePrefix, sequence),
			Role:    wireAssistant,
			Status:  responsesCompleted,
			Content: []responsesContentPart{{Type: "output_text", Text: turn.Text}},
		})
	}
	for index, call := range turn.Calls {
		arguments, err := argumentsOf(call)
		if err != nil {
			return "", err
		}
		output = append(output, responsesItem{
			Type:      wireFunctionCall,
			ID:        fmt.Sprintf("%s_%d_%d", responsesCallPrefix, sequence, index+1),
			CallID:    callID(call, sequence, index),
			Name:      call.Name,
			Arguments: arguments,
			Status:    responsesCompleted,
		})
	}

	response := responsesPayload{
		ID:     responseID("resp", sequence),
		Model:  model,
		Status: responsesCompleted,
		Output: output,
	}
	if turn.Usage != nil {
		response.Usage = &responsesUsage{
			InputTokens:  turn.Usage.InputTokens,
			OutputTokens: turn.Usage.OutputTokens,
		}
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("responses complete response: %w", err)
	}
	return string(encoded), nil
}

// responsesEnvelope is one streamed event of the Responses protocol.
type responsesEnvelope struct {
	Type        string            `json:"type"`
	Response    *responsesPayload `json:"response,omitempty"`
	Item        *responsesItem    `json:"item,omitempty"`
	ItemID      string            `json:"item_id,omitempty"`
	OutputIndex *int              `json:"output_index,omitempty"`
	Delta       string            `json:"delta,omitempty"`
}

// responsesPayload is the response object of the created and completed events.
type responsesPayload struct {
	ID     string          `json:"id"`
	Model  string          `json:"model"`
	Status string          `json:"status"`
	Output []responsesItem `json:"output,omitempty"`
	Usage  *responsesUsage `json:"usage,omitempty"`
}

// responsesItem is one output item of a response. Only the fields valid for
// its type carry meaning.
type responsesItem struct {
	Type             string                 `json:"type"`
	ID               string                 `json:"id,omitempty"`
	Role             string                 `json:"role,omitempty"`
	Status           string                 `json:"status,omitempty"`
	Content          []responsesContentPart `json:"content,omitempty"`
	Summary          []responsesSummaryPart `json:"summary,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	CallID           string                 `json:"call_id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        string                 `json:"arguments,omitempty"`
}

// responsesContentPart is one part of a message item.
type responsesContentPart struct {
	// Type is the type of the part.
	Type string `json:"type"`
	// Text is the text of an output text part.
	Text string `json:"text,omitempty"`
}

// responsesSummaryPart is one part of a reasoning summary.
type responsesSummaryPart struct {
	// Type is the type of the part.
	Type string `json:"type"`
	// Text is the text of the part.
	Text string `json:"text,omitempty"`
}

// responsesUsage mirrors the wire usage object.
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// responsesItemEvent builds an event carrying one output item and its
// position.
func responsesItemEvent(eventType string, index int, item responsesItem) responsesEnvelope {
	return responsesEnvelope{Type: eventType, Item: &item, OutputIndex: &index}
}

// ResponsesRequest is the decoded body of a captured Responses request.
type ResponsesRequest struct {
	// Model is the wire identifier the request generates with.
	Model string `json:"model"`

	// Instructions is the system instruction of the conversation.
	Instructions string `json:"instructions"`

	// Input is the conversation replayed to the provider as input items.
	Input []ResponsesInputItem `json:"input"`

	// Tools declares the tools the model may invoke.
	Tools []ResponsesTool `json:"tools"`

	// ToolChoice constrains tool usage when the application sends one.
	ToolChoice json.RawMessage `json:"tool_choice"`

	// ParallelToolCalls allows the model to request several calls per turn.
	ParallelToolCalls *bool `json:"parallel_tool_calls"`

	// MaxOutputTokens caps the generated tokens.
	MaxOutputTokens int `json:"max_output_tokens"`

	// Temperature is the sampling temperature, nil when unset.
	Temperature *float64 `json:"temperature"`

	// TopP is the nucleus sampling, nil when unset.
	TopP *float64 `json:"top_p"`

	// Reasoning configures extended reasoning when the application sends it.
	Reasoning *ResponsesReasoning `json:"reasoning"`

	// Store reports whether the provider must retain the response. The
	// application always disables it.
	Store *bool `json:"store"`

	// Stream reports whether the request asks for a streamed response.
	Stream bool `json:"stream"`
}

// ResponsesInputItem is one decoded input item of a request. Only the fields
// valid for its type carry meaning.
type ResponsesInputItem struct {
	// Type is the type of the item.
	Type string `json:"type"`

	// ID is the provider identifier of a replayed item.
	ID string `json:"id"`

	// Role is the author of a message item.
	Role string `json:"role"`

	// Content holds the content parts of a message item.
	Content []ResponsesContentPart `json:"content"`

	// CallID references the invocation a function call output answers.
	CallID string `json:"call_id"`

	// Name is the invoked tool of a function call item.
	Name string `json:"name"`

	// Arguments holds the arguments of a function call item as a JSON string.
	Arguments string `json:"arguments"`

	// Output is the result text of a function call output item.
	Output string `json:"output"`

	// EncryptedContent holds the replayable reasoning of a reasoning item.
	EncryptedContent string `json:"encrypted_content"`

	// Summary holds the reasoning summary of a reasoning item.
	Summary *[]ResponsesSummaryPart `json:"summary"`
}

// ResponsesContentPart is one decoded content part of a message item.
type ResponsesContentPart struct {
	// Type is the type of the part.
	Type string `json:"type"`

	// Text is the text of the part.
	Text string `json:"text"`
}

// ResponsesSummaryPart is one decoded part of a reasoning summary.
type ResponsesSummaryPart struct {
	// Type is the type of the part.
	Type string `json:"type"`

	// Text is the text of the part.
	Text string `json:"text"`
}

// ResponsesTool is one decoded tool declaration.
type ResponsesTool struct {
	// Type is the kind of the tool, always "function".
	Type string `json:"type"`

	// Name is the tool name.
	Name string `json:"name"`

	// Description explains the tool to the model.
	Description string `json:"description"`

	// Parameters is the JSON Schema of the arguments.
	Parameters json.RawMessage `json:"parameters"`
}

// ResponsesReasoning is a decoded extended reasoning configuration.
type ResponsesReasoning struct {
	// Effort is the reasoning effort level.
	Effort string `json:"effort"`
}

// Responses decodes a captured request as a Responses payload, failing the
// test when the body does not follow the protocol.
func (r Request) Responses(t *testing.T) *ResponsesRequest {
	t.Helper()

	var decoded ResponsesRequest
	if err := json.Unmarshal(r.Body, &decoded); err != nil {
		t.Fatalf("decode responses request: %v (body: %s)", err, r.Body)
	}
	return &decoded
}

// Text returns the text a message item carries.
func (i ResponsesInputItem) Text() string {
	var text strings.Builder
	for _, part := range i.Content {
		text.WriteString(part.Text)
	}
	return text.String()
}

// SummaryText returns the reasoning summary a reasoning item carries.
func (i ResponsesInputItem) SummaryText() string {
	if i.Summary == nil {
		return ""
	}

	var text strings.Builder
	for _, part := range *i.Summary {
		text.WriteString(part.Text)
	}
	return text.String()
}

// InputTypes returns the types of the input items, in order.
func (r *ResponsesRequest) InputTypes() []string {
	types := make([]string, 0, len(r.Input))
	for _, item := range r.Input {
		types = append(types, item.Type)
	}
	return types
}

// ToolNames returns the names of the tools a request declares, in order.
func (r *ResponsesRequest) ToolNames() []string {
	names := make([]string, 0, len(r.Tools))
	for _, tool := range r.Tools {
		names = append(names, tool.Name)
	}
	return names
}
