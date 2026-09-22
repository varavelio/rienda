package compaction

import (
	"strconv"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
)

// maxToolResultRunes caps how much of one tool result reaches the
// summarization request, so a huge output cannot make the request itself
// exceed the window it is meant to protect. The dropped count is reported, so
// the model knows that it is reading a fragment.
const maxToolResultRunes = 2000

// Serialize renders a conversation as plain text for the summarization
// request, labeling every part so the model can tell who wrote what. Tool
// results are truncated, because a single one can otherwise dwarf the whole
// conversation.
func Serialize(messages []llm.Message) string {
	var text strings.Builder
	for _, message := range messages {
		writeMessage(&text, message)
	}
	return strings.TrimRight(text.String(), "\n")
}

// writeMessage appends the parts of one message to the serialized text.
func writeMessage(text *strings.Builder, message llm.Message) {
	label := "[Assistant]"
	if message.Role == llm.RoleUser {
		label = "[User]"
	}

	for _, block := range message.Blocks {
		switch block.Type {
		case llm.BlockText:
			writeBlock(text, label, block.Text)
		case llm.BlockThinking:
			writeBlock(text, "[Assistant thinking]", block.Thinking)
		case llm.BlockToolCall:
			writeBlock(
				text,
				"[Assistant tool calls]",
				block.ToolCallName+" "+string(block.ToolCallArguments),
			)
		case llm.BlockToolResult:
			writeBlock(text, "[Tool result]", truncate(textOf(block.ToolResult)))
		}
	}
}

// writeBlock appends one labeled part to the serialized text, skipping empty
// content so a message with no text never leaves a dangling label.
func writeBlock(text *strings.Builder, label, content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	text.WriteString(label)
	text.WriteString(": ")
	text.WriteString(strings.TrimRight(content, "\n"))
	text.WriteString("\n")
}

// textOf concatenates the text of nested blocks.
func textOf(blocks []llm.Block) string {
	var text strings.Builder
	for _, block := range blocks {
		text.WriteString(block.Text)
	}
	return text.String()
}

// truncate cuts content to maxToolResultRunes, appending a marker that names
// how much was dropped so the truncation is never silent.
func truncate(content string) string {
	runes := []rune(content)
	if len(runes) <= maxToolResultRunes {
		return content
	}

	dropped := len(runes) - maxToolResultRunes
	return string(runes[:maxToolResultRunes]) +
		"\n[truncated: " + strconv.Itoa(dropped) + " characters dropped]"
}
