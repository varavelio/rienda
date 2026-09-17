package tui

import (
	"unicode/utf8"

	"github.com/varavelio/rienda/internal/engine"
)

// maxToolOutput caps the tool output retained per invocation.
const maxToolOutput = 64 << 10

// entryKind discriminates the transcript entries.
type entryKind int

const (
	// entryUser is a message written by the user.
	entryUser entryKind = iota
	// entryAssistant is text produced by the model.
	entryAssistant
	// entryThinking is reasoning produced by the model.
	entryThinking
	// entryTool is a tool invocation with its streamed output.
	entryTool
	// entryNotice reports something relevant about a run.
	entryNotice
	// entryError reports a failure.
	entryError
)

// entry is one block of the conversation transcript. For tool entries the
// text field carries the output streamed by the invocation.
type entry struct {
	kind entryKind
	text string

	// Tool fields are only meaningful for entryTool entries.
	toolCallID    string
	toolName      string
	toolArguments string
	toolError     bool
	toolDone      bool
	truncated     bool
}

// transcript accumulates the conversation shown to the user.
type transcript struct {
	entries []entry
}

// addUser appends a message written by the user.
func (t *transcript) addUser(text string) {
	t.entries = append(t.entries, entry{kind: entryUser, text: text})
}

// addNotice appends an informational notice.
func (t *transcript) addNotice(text string) {
	t.entries = append(t.entries, entry{kind: entryNotice, text: text})
}

// apply folds one engine event into the transcript.
func (t *transcript) apply(event engine.Event) {
	switch event.Type {
	case engine.EventTextDelta:
		t.appendText(entryAssistant, event.Text)
	case engine.EventThinkingDelta:
		t.appendText(entryThinking, event.Text)
	case engine.EventToolCall:
		t.entries = append(t.entries, entry{
			kind:          entryTool,
			toolCallID:    event.ToolCallID,
			toolName:      event.ToolName,
			toolArguments: string(event.Arguments),
		})
	case engine.EventToolOutput:
		if tool := t.tool(event.ToolCallID); tool != nil {
			tool.appendOutput(event.Output)
		}
	case engine.EventToolResult:
		if tool := t.tool(event.ToolCallID); tool != nil {
			tool.toolDone = true
			tool.toolError = event.IsError
			if tool.text == "" && event.Text != "" {
				tool.text = event.Text
			}
		}
	case engine.EventError:
		t.entries = append(t.entries, entry{kind: entryError, text: event.Error})
	}
}

// appendText extends the last entry of the given kind, starting a new one when
// the last entry belongs to another kind.
func (t *transcript) appendText(kind entryKind, text string) {
	if text == "" {
		return
	}
	if n := len(t.entries); n > 0 && t.entries[n-1].kind == kind {
		t.entries[n-1].text += text
		return
	}
	t.entries = append(t.entries, entry{kind: kind, text: text})
}

// tool returns the tool entry of the call, nil when it is absent.
func (t *transcript) tool(callID string) *entry {
	for i := len(t.entries) - 1; i >= 0; i-- {
		if t.entries[i].kind == entryTool && t.entries[i].toolCallID == callID {
			return &t.entries[i]
		}
	}
	return nil
}

// appendOutput extends the tool output, capping what the interface retains.
func (e *entry) appendOutput(text string) {
	if e.truncated || text == "" {
		return
	}

	remaining := maxToolOutput - len(e.text)
	if remaining <= 0 {
		e.truncated = true
		return
	}
	if len(text) > remaining {
		e.text += cutRunes(text, remaining)
		e.truncated = true
		return
	}
	e.text += text
}

// cutRunes returns the longest prefix of s that fits in maxBytes without
// splitting a rune.
func cutRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}

	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
