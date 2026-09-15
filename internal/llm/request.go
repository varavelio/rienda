package llm

import "encoding/json"

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
	// ToolChoiceTool forces a call to the tool named in ToolChoice.Name.
	ToolChoiceTool ToolChoiceMode = "tool"
)

// ToolChoice constrains tool usage for a request.
type ToolChoice struct {
	Mode ToolChoiceMode
	// ToolName selects the tool when Mode is ToolChoiceTool.
	ToolName string
}

// Tool describes a function the model may invoke.
type Tool struct {
	Name        string
	Description string
	// Parameters holds the JSON Schema describing the tool arguments.
	Parameters json.RawMessage
}

// Request is a provider-neutral generation request.
type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []Tool
	// ToolChoice is nil for the provider default behavior.
	ToolChoice *ToolChoice
	// MaxTokens caps generated tokens. Some providers require it; a zero
	// value lets the provider client apply its own default.
	MaxTokens int
	// Temperature and TopP are nil to leave the provider default untouched.
	Temperature   *float64
	TopP          *float64
	StopSequences []string
	Reasoning     *ReasoningConfig
}
