package llm

import "context"

// StreamEventType discriminates the payload carried by a StreamEvent.
type StreamEventType string

const (
	// StreamMessageStart opens a streamed response. ID and Model are populated.
	StreamMessageStart StreamEventType = "message_start"
	// StreamTextDelta carries an incremental text fragment in Text.
	StreamTextDelta StreamEventType = "text_delta"
	// StreamThinkingDelta carries incremental reasoning in Thinking, with the
	// latest provider signature fragment in ThinkingSignature when available.
	StreamThinkingDelta StreamEventType = "thinking_delta"
	// StreamToolCallStart announces a new tool call. ToolCallID and ToolCallName
	// are populated.
	StreamToolCallStart StreamEventType = "tool_call_start"
	// StreamToolCallArgsDelta carries an incremental JSON fragment of the tool
	// arguments in ToolCallArgsDelta. ToolCallID identifies the call.
	StreamToolCallArgsDelta StreamEventType = "tool_call_args_delta"
	// StreamMessageEnd closes a streamed response. StopReason and Usage are
	// populated.
	StreamMessageEnd StreamEventType = "message_end"
)

// StreamEvent is a single incremental update of a streamed response. Only the
// fields valid for the event Type carry meaning.
type StreamEvent struct {
	Type StreamEventType

	// ID and Model identify the response for StreamMessageStart events.
	ID    string
	Model string

	// Text carries StreamTextDelta fragments.
	Text string

	// Thinking carries StreamThinkingDelta fragments. ThinkingSignature carries
	// the latest provider signature fragment when the provider streams one.
	Thinking          string
	ThinkingSignature string

	// ToolCallID identifies the tool call for StreamToolCallStart and
	// StreamToolCallArgsDelta events. ToolCallName names the call on start and
	// ToolCallArgsDelta carries incremental tool argument fragments.
	ToolCallID        string
	ToolCallName      string
	ToolCallArgsDelta string

	// StopReason and Usage finalize StreamMessageEnd events.
	StopReason StopReason
	Usage      Usage
}

// Stream is a pull-based handle over an in-flight streamed response.
//
// Next blocks until the next event arrives. It returns io.EOF once the
// response completed; any other error aborts the stream. Close releases the
// underlying resources and is safe to call multiple times.
type Stream interface {
	Next() (StreamEvent, error)
	Close() error
}

// Client generates model responses, either complete or streamed.
type Client interface {
	// Generate returns the full response once generation completes.
	Generate(ctx context.Context, req *Request) (*Response, error)
	// Stream returns a handle to consume the response incrementally. The
	// returned Stream must be closed by the caller.
	Stream(ctx context.Context, req *Request) (Stream, error)
}
