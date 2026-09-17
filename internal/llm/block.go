package llm

import "encoding/json"

// BlockType discriminates the content carried by a Block.
type BlockType string

const (
	// BlockText carries plain text in Text.
	BlockText BlockType = "text"
	// BlockThinking carries model reasoning in Thinking and ThinkingSignature.
	BlockThinking BlockType = "thinking"
	// BlockRedactedThinking carries provider-redacted reasoning in
	// ThinkingRedactedData. The data is opaque and must be replayed verbatim.
	BlockRedactedThinking BlockType = "redacted_thinking"
	// BlockToolCall represents a model request to invoke a tool.
	// ToolCallID, ToolCallName and ToolCallArguments are populated.
	BlockToolCall BlockType = "tool_call"
	// BlockToolResult carries the outcome of a tool invocation.
	// ToolResultCallID, ToolResult and ToolResultIsError are populated.
	BlockToolResult BlockType = "tool_result"
)

// Block is a single unit of message content. Only the fields valid for the
// block Type carry meaning; the rest must be left at their zero value.
type Block struct {
	// Type selects which fields of the block carry meaning.
	Type BlockType

	// Text holds the content of BlockText blocks.
	Text string

	// Thinking holds the reasoning of BlockThinking blocks.
	Thinking string

	// ThinkingSignature is an opaque provider token attached to BlockThinking blocks.
	// It preserves the cryptographic signature required by some providers
	// (for example Anthropic) or the encrypted reasoning content used by
	// others (for example OpenAI Responses) when the block is sent back in
	// a later turn.
	ThinkingSignature string

	// ThinkingID is the provider-assigned identifier of the reasoning item
	// that carried a BlockThinking block. Providers that require the
	// identifier, like OpenAI Responses, set it so the block can be replayed.
	ThinkingID string

	// ThinkingRedactedData holds the opaque provider data of a
	// BlockRedactedThinking block.
	ThinkingRedactedData string

	// ToolCallID is the provider-assigned identifier of a BlockToolCall.
	ToolCallID string

	// ToolCallName is the name of the tool invoked by a BlockToolCall.
	ToolCallName string

	// ToolCallArguments holds the tool arguments as a JSON object for
	// BlockToolCall blocks.
	ToolCallArguments json.RawMessage

	// ToolResultCallID references the ToolCallID of the BlockToolCall answered
	// by a BlockToolResult.
	ToolResultCallID string

	// ToolResult holds nested blocks (usually text) with the outcome of the
	// invocation for BlockToolResult blocks.
	ToolResult []Block

	// ToolResultIsError marks a BlockToolResult as a failed invocation.
	ToolResultIsError bool
}
