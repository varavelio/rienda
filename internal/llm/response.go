package llm

// StopReason explains why generation ended.
type StopReason string

const (
	// StopReasonEndTurn marks a natural completion of the response.
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonMaxTokens marks a response truncated by a token limit.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonToolUse marks a response that requests tool calls.
	StopReasonToolUse StopReason = "tool_use"
	// StopReasonStopSequence marks a response ended by a stop sequence.
	StopReasonStopSequence StopReason = "stop_sequence"
	// StopReasonRefusal marks a response the model refused to produce.
	StopReasonRefusal StopReason = "refusal"
	// StopReasonContentFilter marks a response blocked by content filtering.
	StopReasonContentFilter StopReason = "content_filter"
	// StopReasonPaused marks a response paused by the provider mid-turn.
	StopReasonPaused StopReason = "paused"
)

// Usage reports token consumption of a single response.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
}

// Response is a provider-neutral generation result.
type Response struct {
	ID         string
	Model      string
	Blocks     []Block
	StopReason StopReason
	Usage      Usage
}
