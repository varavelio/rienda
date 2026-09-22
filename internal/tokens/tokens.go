package tokens

import "github.com/varavelio/rienda/internal/llm"

// bytesPerToken is the number of bytes a token is assumed to take. The ratio
// holds across providers and languages well enough for a guardrail, and
// counting bytes rather than runes lands near the real figure for text that is
// not ASCII.
const bytesPerToken = 4

// OfMessage returns the estimated number of tokens of one message. Only the
// parts the model reads are counted: text, thinking, redacted thinking, tool
// call names and arguments, and the blocks nested in a tool result.
func OfMessage(message llm.Message) int {
	return estimate(blocksBytes(message.Blocks))
}

// OfRequest returns the estimated number of tokens of a whole request. It
// counts the system prompt, every message and the tool definitions, because
// the model reads all of them.
func OfRequest(request *llm.Request) int {
	if request == nil {
		return 0
	}

	total := len(request.System)
	for _, message := range request.Messages {
		total += blocksBytes(message.Blocks)
	}
	for _, tool := range request.Tools {
		total += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}
	return estimate(total)
}

// Report is a context measurement: the estimated tokens in use, the window
// they are measured against and the resulting percentage.
type Report struct {
	// Used is the estimated number of tokens the measured request consumes.
	Used int

	// Window is the context window of the model, as it was resolved.
	Window int

	// Percent is the share of the window in use, an integer between 0 and
	// 100.
	Percent int
}

// Measure builds the report of a request measured against a context window.
// The percentage is zero when the window is not usable, it is clamped to 100
// when the estimate exceeds the window, and Used and Window are returned
// exactly as they were passed.
func Measure(used, window int) Report {
	report := Report{Used: used, Window: window}
	if window <= 0 || used <= 0 {
		return report
	}

	report.Percent = min(used*100/window, 100)
	return report
}

// blocksBytes returns the number of bytes the model reads from a list of
// blocks.
func blocksBytes(blocks []llm.Block) int {
	total := 0
	for _, block := range blocks {
		switch block.Type {
		case llm.BlockText:
			total += len(block.Text)
		case llm.BlockThinking:
			total += len(block.Thinking)
		case llm.BlockRedactedThinking:
			total += len(block.ThinkingRedactedData)
		case llm.BlockToolCall:
			total += len(block.ToolCallName) + len(block.ToolCallArguments)
		case llm.BlockToolResult:
			total += blocksBytes(block.ToolResult)
		}
	}
	return total
}

// estimate converts a byte count into tokens, rounding up so any content at
// all counts as at least one token and an empty input counts as none.
func estimate(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + bytesPerToken - 1) / bytesPerToken
}
