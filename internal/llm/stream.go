package llm

import "errors"

// ErrIncompleteStream reports a provider stream that ended or broke before the
// response completed. A caller may repeat the call, because the stream carried
// no complete answer.
var ErrIncompleteStream = errors.New("the response stream is incomplete")

// IncompleteStream marks err as coming from a stream that broke before the
// response completed. A provider wraps the malformed fragment it could not
// read, so a caller knows repeating the call may overcome the failure. The
// message of err is preserved exactly.
func IncompleteStream(err error) error {
	if err == nil {
		return nil
	}
	return &incompleteStreamError{err: err}
}

// incompleteStreamError marks a failure of an incomplete stream.
type incompleteStreamError struct{ err error }

// Error returns the description of the wrapped failure.
func (e *incompleteStreamError) Error() string { return e.err.Error() }

// Unwrap returns the wrapped failure together with the marker, so errors.Is
// reaches both.
func (e *incompleteStreamError) Unwrap() []error { return []error{e.err, ErrIncompleteStream} }

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
	// StreamThinkingRedacted carries the opaque data of a redacted reasoning
	// block in ThinkingRedactedData.
	StreamThinkingRedacted StreamEventType = "thinking_redacted"
	// StreamThinkingBoundary closes the reasoning block the thinking deltas
	// have been building, because the provider opened another one: a response
	// may carry several reasoning blocks, each with its own signature, and a
	// signature accumulates into one block alone, which no provider accepts
	// on a later turn.
	StreamThinkingBoundary StreamEventType = "thinking_boundary"
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
	// Type selects which fields of the event carry meaning.
	Type StreamEventType

	// ID and Model identify the response for StreamMessageStart events.
	ID    string
	Model string

	// ItemID identifies the output message item that carried the response text
	// for StreamMessageEnd events, when the provider assigns identifiers.
	ItemID string

	// Text carries StreamTextDelta fragments.
	Text string

	// Thinking carries StreamThinkingDelta fragments. ThinkingSignature carries
	// the latest provider signature fragment when the provider streams one.
	// ThinkingID identifies the reasoning item the signature belongs to.
	// ThinkingRedactedData carries the data of a StreamThinkingRedacted event.
	Thinking             string
	ThinkingSignature    string
	ThinkingID           string
	ThinkingRedactedData string

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
