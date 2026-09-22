package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// maxToolOutput caps the tool output retained per invocation.
const maxToolOutput = 64 << 10

// compactionBody is the one-line note that closes a checkpoint block, so the
// reader sees where the conversation was summarized.
const compactionBody = "the conversation was compacted"

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
	// entryCompaction reports that the conversation before it was summarized
	// into a checkpoint.
	entryCompaction
)

// isTurn reports whether an entry opens a turn of the conversation: a message
// written by the user or an answer of the agent. The remaining kinds, the
// reasoning, the tool invocations and the notices, are activities of a turn and
// not stops the reader jumps between.
func isTurn(kind entryKind) bool {
	return kind == entryUser || kind == entryAssistant
}

// entry is one block of the conversation transcript. For tool entries the text
// carries the output streamed by the invocation.
//
// The text grows with the fragments appended to it and is joined only when the
// interface renders the entry, which keeps streaming an answer cheap.
type entry struct {
	kind entryKind

	// fragments holds the pieces of text appended to the entry, joined into
	// joined up to joinedLen when the interface renders it. size is the total
	// length of the text, tracked to cap the output of a tool without joining.
	fragments []string
	joined    string
	joinedLen int
	size      int

	// elapsed is how long the turn took, recorded on the entry that closes it.
	elapsed time.Duration

	// Tool fields are only meaningful for entryTool entries.
	toolCallID    string
	toolName      string
	toolArguments string
	toolError     bool
	toolDone      bool
	truncated     bool
}

// text returns the text of the entry, joining the fragments appended since the
// last call.
func (e *entry) text() string {
	if pending := e.fragments[e.joinedLen:]; len(pending) > 0 {
		e.joined += strings.Join(pending, "")
		e.joinedLen = len(e.fragments)
	}
	return e.joined
}

// append extends the text of the entry with one fragment.
func (e *entry) append(fragment string) {
	if fragment == "" {
		return
	}
	e.fragments = append(e.fragments, fragment)
	e.size += len(fragment)
}

// transcript accumulates the conversation shown to the user. It tracks the
// first entry whose rendering is stale, so the interface can render only the
// blocks that changed.
type transcript struct {
	entries []entry

	// dirty is the index of the first entry that must be rendered again, or
	// the number of entries when every one of them is already rendered.
	dirty int
}

// changedFrom returns the index of the first entry whose rendering is stale.
func (t *transcript) changedFrom() int {
	return min(t.dirty, len(t.entries))
}

// markRendered records that every entry is rendered.
func (t *transcript) markRendered() {
	t.dirty = len(t.entries)
}

// touch marks the entry and everything after it as stale.
func (t *transcript) touch(index int) {
	if index < t.dirty {
		t.dirty = index
	}
}

// push appends an entry and marks it, together with the one before it, as
// stale: an entry renders differently once it is no longer the last one.
func (t *transcript) push(current entry) {
	t.entries = append(t.entries, current)

	last := len(t.entries) - 1
	t.touch(last)
	if last > 0 {
		t.touch(last - 1)
	}
}

// addUser appends a message written by the user.
func (t *transcript) addUser(text string) {
	t.push(entry{kind: entryUser, fragments: []string{text}})
}

// addNotice appends an informational notice.
func (t *transcript) addNotice(text string) {
	t.push(entry{kind: entryNotice, fragments: []string{text}})
}

// addCompaction appends the checkpoint of a compaction as a turn of its own,
// labeled like any other turn.
func (t *transcript) addCompaction() {
	t.push(entry{kind: entryCompaction, fragments: []string{compactionBody}})
}

// finishTurn records how long the turn in flight took on the entry that closes
// it, the last one, so the interface shows the time under the last message of
// the turn. It does nothing while the transcript holds no entry.
func (t *transcript) finishTurn(elapsed time.Duration) {
	if len(t.entries) == 0 {
		return
	}

	last := len(t.entries) - 1
	t.entries[last].elapsed = elapsed
	t.touch(last)
}

// load seeds the transcript with the entries of a stored conversation, oldest
// first, closing every completed turn with the time it took. The duration is
// not stored: it is derived from the moment the turn opened, the user message
// that started it, and the moment it closed, the assistant message that
// answered it, so a slow provider that takes minutes to answer is reported
// like any other turn.
func (t *transcript) load(entries []session.Entry) {
	var opened time.Time
	for _, entry := range entries {
		t.fold(entry)
		switch {
		case opensTurn(entry):
			opened = entry.CreatedAt
		case closesTurn(entry) && !opened.IsZero():
			t.finishTurn(entry.CreatedAt.Sub(opened))
			opened = time.Time{}
		}
	}
}

// fold appends the content of one stored entry to the transcript.
func (t *transcript) fold(entry session.Entry) {
	if entry.Kind == session.KindCompaction {
		t.addCompaction()
		return
	}

	textKind := entryAssistant
	if entry.Message.Role == llm.RoleUser {
		textKind = entryUser
	}

	for _, block := range entry.Message.Blocks {
		switch block.Type {
		case llm.BlockText:
			t.appendText(textKind, block.Text)
		case llm.BlockThinking:
			t.appendText(entryThinking, block.Thinking)
		case llm.BlockToolCall:
			t.addTool(block.ToolCallID, block.ToolCallName, string(block.ToolCallArguments))
		case llm.BlockToolResult:
			t.finishTool(block.ToolResultCallID, block.ToolResultIsError, textOf(block.ToolResult))
		}
	}
}

// opensTurn reports whether a stored entry starts a turn: the user message that
// carries a prompt. The user messages that carry tool results continue the turn
// in flight instead of opening one.
func opensTurn(entry session.Entry) bool {
	return entry.Message.Role == llm.RoleUser &&
		!hasBlock(entry.Message.Blocks, llm.BlockToolResult)
}

// closesTurn reports whether a stored entry ends a turn: the assistant message
// that answered without asking for tools. An assistant message that requests
// tools keeps the turn open until the model answers again.
func closesTurn(entry session.Entry) bool {
	return entry.Message.Role == llm.RoleAssistant &&
		!hasBlock(entry.Message.Blocks, llm.BlockToolCall)
}

// hasBlock reports whether the blocks hold one of the given type.
func hasBlock(blocks []llm.Block, kind llm.BlockType) bool {
	return slices.ContainsFunc(blocks, func(block llm.Block) bool { return block.Type == kind })
}

// addTool appends a tool invocation to the transcript.
func (t *transcript) addTool(callID, name, arguments string) {
	t.push(entry{
		kind:          entryTool,
		toolCallID:    callID,
		toolName:      name,
		toolArguments: arguments,
	})
}

// finishTool marks a tool invocation as finished, keeping the result text when
// nothing streamed before it.
func (t *transcript) finishTool(callID string, isError bool, result string) {
	index := t.toolIndex(callID)
	if index < 0 {
		return
	}

	tool := &t.entries[index]
	tool.toolDone = true
	tool.toolError = isError
	if tool.size == 0 {
		tool.appendOutput(result)
	}
	t.touch(index)
}

// textOf concatenates the text of the given blocks.
func textOf(blocks []llm.Block) string {
	var text strings.Builder
	for _, block := range blocks {
		text.WriteString(block.Text)
	}
	return text.String()
}

// apply folds one engine event into the transcript.
func (t *transcript) apply(event engine.Event) {
	switch event.Type {
	case engine.EventTextDelta:
		t.appendText(entryAssistant, event.Text)
	case engine.EventThinkingDelta:
		t.appendText(entryThinking, event.Text)
	case engine.EventToolCall:
		t.addTool(event.ToolCallID, event.ToolName, string(event.Arguments))
	case engine.EventToolOutput:
		if index := t.toolIndex(event.ToolCallID); index >= 0 {
			t.entries[index].appendOutput(event.Output)
			t.touch(index)
		}
	case engine.EventToolResult:
		t.finishTool(event.ToolCallID, event.IsError, event.Text)
	case engine.EventRetry:
		if event.Discard {
			t.discard()
		}
		t.addNotice(retryNotice(event))
	case engine.EventCompactionEnd:
		t.addCompaction()
	case engine.EventError:
		t.push(entry{kind: entryError, fragments: []string{event.Error}})
	}
}

// retryNotice describes a model call that failed transiently and is being
// retried, so the pause before the next attempt never reads as a hang. A
// retry that discarded a partial response says so, because the answer the
// reader was watching disappears before the new one streams.
func retryNotice(event engine.Event) string {
	action := "retrying"
	if event.Discard {
		action = "restarting the response"
	}
	return fmt.Sprintf(
		"transient error, %s in %s: %s",
		action,
		event.RetryIn.Round(time.Millisecond),
		event.Error,
	)
}

// discard drops the entries a failed attempt streamed, so the retried response
// replaces the partial one instead of appending to it. A stream only produces
// reasoning and answer text before it fails, so the trailing entries of those
// kinds are removed and the first entry of any other kind stops the rollback.
// That leaves everything an earlier turn wrote, its answer and its tool
// invocations included, untouched.
func (t *transcript) discard() {
	for len(t.entries) > 0 {
		last := t.entries[len(t.entries)-1]
		if last.kind != entryAssistant && last.kind != entryThinking {
			break
		}
		t.entries = t.entries[:len(t.entries)-1]
	}
	t.touch(len(t.entries))
}

// appendText extends the last entry of the given kind, starting a new one when
// the last entry belongs to another kind.
func (t *transcript) appendText(kind entryKind, text string) {
	if text == "" {
		return
	}
	if n := len(t.entries); n > 0 && t.entries[n-1].kind == kind {
		t.entries[n-1].append(text)
		t.touch(n - 1)
		return
	}
	t.push(entry{kind: kind, fragments: []string{text}})
}

// toolIndex returns the index of the tool entry of the call, or -1 when it is
// absent.
func (t *transcript) toolIndex(callID string) int {
	for index, current := range slices.Backward(t.entries) {
		if current.kind == entryTool && current.toolCallID == callID {
			return index
		}
	}
	return -1
}

// appendOutput extends the tool output, capping what the interface retains.
func (e *entry) appendOutput(text string) {
	if e.truncated || text == "" {
		return
	}

	remaining := maxToolOutput - e.size
	if remaining <= 0 {
		e.truncated = true
		return
	}
	if len(text) > remaining {
		e.append(cutRunes(text, remaining))
		e.truncated = true
		return
	}
	e.append(text)
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
