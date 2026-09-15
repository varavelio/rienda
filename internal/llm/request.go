package llm

import "encoding/json"

// Role identifies the author of a Message. Only user and assistant turns
// exist in a conversation: the system prompt is carried by Request.System and
// tool results are BlockToolResult blocks.
type Role string

const (
	// RoleUser marks a message authored by the user.
	RoleUser Role = "user"
	// RoleAssistant marks a message authored by the model.
	RoleAssistant Role = "assistant"
)

// Message is a single turn in a conversation.
type Message struct {
	// Role identifies the author of the message.
	Role Role

	// Blocks is the ordered content of the message.
	Blocks []Block
}

// ReasoningConfig controls the reasoning effort of a model that supports it.
type ReasoningConfig struct {
	// Effort selects a named reasoning level (for example "low", "medium" or
	// "high") for providers with effort-based APIs.
	Effort string

	// BudgetTokens reserves a token budget for reasoning for providers with
	// budget-based APIs.
	BudgetTokens int
}

// ToolChoiceMode selects how the model may use the declared tools.
type ToolChoiceMode string

const (
	// ToolChoiceAuto lets the model decide whether to call tools.
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceNone forbids tool calls.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceRequired forces at least one tool call.
	ToolChoiceRequired ToolChoiceMode = "required"
	// ToolChoiceTool forces a call to the tool named in ToolChoice.ToolName.
	ToolChoiceTool ToolChoiceMode = "tool"
)

// ToolChoice constrains tool usage for a request.
type ToolChoice struct {
	// Mode selects how the model may use the declared tools.
	Mode ToolChoiceMode

	// ToolName selects the tool when Mode is ToolChoiceTool.
	ToolName string
}

// Tool describes a function the model may invoke.
type Tool struct {
	// Name is the tool name the model uses to reference the tool in a call.
	Name string

	// Description explains what the tool does and when the model should use it.
	Description string

	// Parameters holds the JSON Schema describing the tool arguments.
	Parameters json.RawMessage
}

// Request is a provider-neutral generation request.
type Request struct {
	// Model is the provider model identifier to generate with.
	Model string

	// System is the system instruction that applies to the whole conversation.
	System string

	// Messages is the ordered conversation history sent to the model.
	Messages []Message

	// Tools declares the functions the model is allowed to invoke.
	Tools []Tool

	// ToolChoice is nil for the provider default behavior.
	ToolChoice *ToolChoice

	// MaxTokens caps generated tokens. Some providers require it; a zero
	// value lets the provider client apply its own default.
	MaxTokens int

	// Temperature controls sampling randomness. It is nil to leave the
	// provider default untouched.
	Temperature *float64

	// TopP controls nucleus sampling. It is nil to leave the provider default
	// untouched.
	TopP *float64

	// StopSequences ends generation when the model produces any of them.
	StopSequences []string

	// Reasoning configures extended reasoning. It is nil to send no explicit
	// reasoning configuration.
	Reasoning *ReasoningConfig
}
