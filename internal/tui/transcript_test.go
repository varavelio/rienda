package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// TestTranscript verifies event folding.
func TestTranscript(t *testing.T) {
	t.Run("folds a run with a tool call", func(t *testing.T) {
		conversation := transcript{}
		conversation.addUser("list the files")
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Let me "})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "check."})
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
			Arguments:  json.RawMessage(`{"command":"ls"}`),
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolOutput,
			ToolCallID: "call_1",
			Output:     "a.txt\n",
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "a.txt",
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Done."})

		require.Len(t, conversation.entries, 4)
		require.Equal(t, entryUser, conversation.entries[0].kind)
		require.Equal(t, "list the files", conversation.entries[0].text())
		require.Equal(t, entryAssistant, conversation.entries[1].kind)
		require.Equal(t, "Let me check.", conversation.entries[1].text())

		invocation := conversation.entries[2]
		require.Equal(t, entryTool, invocation.kind)
		require.Equal(t, "shell", invocation.toolName)
		require.Equal(t, `{"command":"ls"}`, invocation.toolArguments)
		require.Equal(t, "a.txt\n", invocation.text())
		require.True(t, invocation.toolDone)
		require.False(t, invocation.toolError)

		require.Equal(t, entryAssistant, conversation.entries[3].kind)
		require.Equal(t, "Done.", conversation.entries[3].text())
	})

	t.Run("separates thinking from the answer", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: " yes"})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "hello"})

		require.Len(t, conversation.entries, 2)
		require.Equal(t, entryThinking, conversation.entries[0].kind)
		require.Equal(t, "hmm yes", conversation.entries[0].text())
		require.Equal(t, entryAssistant, conversation.entries[1].kind)
	})

	t.Run("keeps a result that did not stream", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "boom",
			IsError:    true,
		})

		invocation := conversation.entries[0]
		require.True(t, invocation.toolDone)
		require.True(t, invocation.toolError)
		require.Equal(t, "boom", invocation.text())
	})

	t.Run("caps the tool output", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})

		chunk := strings.Repeat("x", maxToolOutput/2)
		for range 4 {
			conversation.apply(engine.Event{
				Type:       engine.EventToolOutput,
				ToolCallID: "call_1",
				Output:     chunk,
			})
		}
		conversation.apply(engine.Event{Type: engine.EventToolOutput, ToolCallID: "call_1"})

		invocation := conversation.entries[0]
		require.Len(t, invocation.text(), maxToolOutput)
		require.True(t, invocation.truncated)
	})

	t.Run("records errors and notices", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventError, Error: "boom"})
		conversation.addNotice("the run was interrupted")

		require.Len(t, conversation.entries, 2)
		require.Equal(t, entryError, conversation.entries[0].kind)
		require.Equal(t, "boom", conversation.entries[0].text())
		require.Equal(t, entryNotice, conversation.entries[1].kind)
	})

	t.Run("derives the time of a stored turn from its entries", func(t *testing.T) {
		base := time.Date(2026, 9, 17, 18, 54, 0, 0, time.UTC)
		conversation := transcript{}
		conversation.load([]session.Entry{
			messageEntryAt(base, llm.RoleUser,
				llm.Block{Type: llm.BlockText, Text: "slow one"}),
			// The model took five minutes: the provider was slow, not the
			// reader, and the stored timestamps carry the whole wait.
			messageEntryAt(base.Add(5*time.Minute), llm.RoleAssistant,
				llm.Block{Type: llm.BlockText, Text: "finally"}),
		}, "coder")

		require.Len(t, conversation.entries, 2)
		require.Equal(t, 5*time.Minute, conversation.entries[1].elapsed)
	})

	t.Run("keeps a turn with tools open until the model answers", func(t *testing.T) {
		base := time.Date(2026, 9, 17, 18, 54, 0, 0, time.UTC)
		conversation := transcript{}
		conversation.load([]session.Entry{
			messageEntryAt(base, llm.RoleUser,
				llm.Block{Type: llm.BlockText, Text: "run it"}),
			messageEntryAt(base.Add(2*time.Second), llm.RoleAssistant,
				llm.Block{Type: llm.BlockToolCall, ToolCallID: "c1", ToolCallName: "shell"}),
			messageEntryAt(base.Add(3*time.Second), llm.RoleUser,
				llm.Block{Type: llm.BlockToolResult, ToolResultCallID: "c1"}),
			messageEntryAt(base.Add(30*time.Second), llm.RoleAssistant,
				llm.Block{Type: llm.BlockText, Text: "done"}),
		}, "coder")

		last := conversation.entries[len(conversation.entries)-1]
		require.Equal(t, 30*time.Second, last.elapsed, "the whole turn counts")
		require.Zero(t, conversation.entries[1].elapsed, "the tool request does not close it")
	})

	t.Run("leaves an interrupted turn without a time", func(t *testing.T) {
		base := time.Date(2026, 9, 17, 18, 54, 0, 0, time.UTC)
		conversation := transcript{}
		conversation.load([]session.Entry{
			messageEntryAt(base, llm.RoleUser,
				llm.Block{Type: llm.BlockText, Text: "never answered"}),
		}, "coder")

		require.Len(t, conversation.entries, 1)
		require.Zero(t, conversation.entries[0].elapsed)
	})

	t.Run("records the time a turn took on its last entry", func(t *testing.T) {
		conversation := transcript{}
		conversation.addUser("hello")
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "hi"})
		conversation.finishTurn(90 * time.Second)

		require.Len(t, conversation.entries, 2)
		require.Zero(t, conversation.entries[0].elapsed, "only the closing entry carries it")
		require.Equal(t, 90*time.Second, conversation.entries[1].elapsed)
	})

	t.Run("ignores a turn that produced no entry", func(t *testing.T) {
		conversation := transcript{}
		conversation.finishTurn(time.Second)

		require.Empty(t, conversation.entries)
	})

	t.Run("records retried model calls as notices", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: 250 * time.Millisecond,
			Error:   "engine: stream response: overloaded",
		})

		require.Len(t, conversation.entries, 1)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Contains(t, conversation.entries[0].text(), "retrying in 250ms")
		require.Contains(t, conversation.entries[0].text(), "overloaded")
	})

	t.Run("drops the partial response of a discarded attempt", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: "thinking"})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "partial"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: 250 * time.Millisecond,
			Error:   "stream ended before [DONE]",
			Discard: true,
		})

		require.Len(t, conversation.entries, 1)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Contains(t, conversation.entries[0].text(), "restarting the response")

		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "recovered"})
		require.Len(t, conversation.entries, 2)
		require.Equal(t, "recovered", conversation.entries[1].text())
	})

	t.Run("drops every partial response of consecutive attempts", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "first partial"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: time.Second,
			Error:   "truncated",
			Discard: true,
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "second partial"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 2,
			RetryIn: time.Second,
			Error:   "truncated",
			Discard: true,
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "recovered"})

		require.Len(t, conversation.entries, 3)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Equal(t, entryNotice, conversation.entries[1].kind)
		require.Equal(t, entryAssistant, conversation.entries[2].kind)
		require.Equal(t, "recovered", conversation.entries[2].text())
	})

	t.Run("keeps an earlier answer when an attempt is discarded", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "first"})
		conversation.apply(engine.Event{
			Type:       engine.EventMessageEnd,
			StopReason: llm.StopReasonToolUse,
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "ok",
		})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "second"})
		conversation.apply(engine.Event{
			Type:    engine.EventRetry,
			Attempt: 1,
			RetryIn: 250 * time.Millisecond,
			Error:   "dropped",
			Discard: true,
		})

		require.Len(t, conversation.entries, 3)
		require.Equal(t, entryAssistant, conversation.entries[0].kind)
		require.Equal(t, "first", conversation.entries[0].text())
		require.Equal(t, entryTool, conversation.entries[1].kind)
		require.Equal(t, entryNotice, conversation.entries[2].kind)
	})

	t.Run("ignores output of unknown calls", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(
			engine.Event{Type: engine.EventToolOutput, ToolCallID: "ghost", Output: "x"},
		)
		conversation.apply(
			engine.Event{Type: engine.EventToolResult, ToolCallID: "ghost", Text: "x"},
		)

		require.Empty(t, conversation.entries)
	})

	t.Run("loads a stored conversation", func(t *testing.T) {
		conversation := transcript{}
		conversation.load([]session.Entry{
			messageEntry(llm.RoleUser, llm.Block{Type: llm.BlockText, Text: "list the files"}),
			messageEntry(llm.RoleAssistant,
				llm.Block{Type: llm.BlockThinking, Thinking: "let me "},
				llm.Block{Type: llm.BlockThinking, Thinking: "check"},
				llm.Block{Type: llm.BlockText, Text: "sure"},
				llm.Block{
					Type:              llm.BlockToolCall,
					ToolCallID:        "call_1",
					ToolCallName:      "shell",
					ToolCallArguments: json.RawMessage(`{"command":"ls"}`),
				},
			),
			messageEntry(llm.RoleUser, llm.Block{
				Type:             llm.BlockToolResult,
				ToolResultCallID: "call_1",
				ToolResult: []llm.Block{
					{Type: llm.BlockText, Text: "a.txt"},
				},
			}),
		}, "coder")

		require.Len(t, conversation.entries, 4)
		require.Equal(t, entryUser, conversation.entries[0].kind)
		require.Equal(t, "list the files", conversation.entries[0].text())
		require.Equal(t, entryThinking, conversation.entries[1].kind)
		require.Equal(t, "let me check", conversation.entries[1].text())
		require.Equal(t, entryAssistant, conversation.entries[2].kind)
		require.Equal(t, "sure", conversation.entries[2].text())

		invocation := conversation.entries[3]
		require.Equal(t, entryTool, invocation.kind)
		require.Equal(t, "shell", invocation.toolName)
		require.Equal(t, "a.txt", invocation.text())
		require.True(t, invocation.toolDone)
		require.False(t, invocation.toolError)
	})

	t.Run("loads failed invocations", func(t *testing.T) {
		conversation := transcript{}
		conversation.load([]session.Entry{
			messageEntry(llm.RoleAssistant,
				llm.Block{Type: llm.BlockToolCall, ToolCallID: "call_1", ToolCallName: "shell"},
			),
			messageEntry(llm.RoleUser, llm.Block{
				Type:              llm.BlockToolResult,
				ToolResultCallID:  "call_1",
				ToolResultIsError: true,
				ToolResult: []llm.Block{
					{Type: llm.BlockText, Text: "boom"},
				},
			}),
		}, "coder")

		require.Len(t, conversation.entries, 1)
		require.True(t, conversation.entries[0].toolDone)
		require.True(t, conversation.entries[0].toolError)
		require.Equal(t, "boom", conversation.entries[0].text())
	})

	t.Run("tracks the entries that changed", func(t *testing.T) {
		conversation := transcript{}
		conversation.markRendered()

		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "one"})
		require.Equal(t, 0, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: " and two"})
		require.Equal(t, 0, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		require.Equal(t, 0, conversation.changedFrom(), "the entry before it changes too")

		conversation.markRendered()
		conversation.apply(engine.Event{
			Type:       engine.EventToolOutput,
			ToolCallID: "call_1",
			Output:     "a.txt",
		})
		require.Equal(t, 1, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "a.txt",
		})
		require.Equal(t, 1, conversation.changedFrom())

		conversation.markRendered()
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		require.Equal(t, 1, conversation.changedFrom(), "the entry before it changes too")
	})
}

// messageEntry builds a stored entry that carries one message, the shape the
// transcript loads from a session file.
func messageEntry(role llm.Role, blocks ...llm.Block) session.Entry {
	return session.Entry{Message: llm.Message{Role: role, Blocks: blocks}}
}

// messageEntryAt builds a stored message entry appended at the given moment,
// the shape a session file carries.
func messageEntryAt(moment time.Time, role llm.Role, blocks ...llm.Block) session.Entry {
	entry := messageEntry(role, blocks...)
	entry.CreatedAt = moment
	return entry
}

// TestCutRunes verifies rune-safe truncation.
func TestCutRunes(t *testing.T) {
	t.Run("keeps short texts", func(t *testing.T) {
		require.Equal(t, "hello", cutRunes("hello", 10))
	})

	t.Run("never splits a rune", func(t *testing.T) {
		require.Equal(t, "añ", cutRunes("añejo", 3))
		require.Equal(t, "a", cutRunes("añejo", 2))
	})
}

// TestTranscriptCompaction verifies the checkpoint block of the conversation.
func TestTranscriptCompaction(t *testing.T) {
	t.Run("appends a checkpoint on the end event", func(t *testing.T) {
		var folded transcript

		folded.apply(engine.Event{Type: engine.EventCompactionEnd})

		require.Len(t, folded.entries, 1)
		require.Equal(t, entryCompaction, folded.entries[0].kind)
		require.Equal(t, compactionBody, folded.entries[0].text())
	})

	t.Run("folds a stored checkpoint into the transcript", func(t *testing.T) {
		var folded transcript

		folded.load([]session.Entry{
			{ID: "u1", Kind: session.KindMessage, Message: textMessage(llm.RoleUser, "hello")},
			{
				ID:                "c1",
				Kind:              session.KindCompaction,
				CompactionSummary: "the summary",
				CompactionKeptID:  "u1",
			},
		}, "coder")

		require.Len(t, folded.entries, 2)
		require.Equal(t, entryUser, folded.entries[0].kind)
		require.Equal(t, entryCompaction, folded.entries[1].kind)
		require.Equal(t, compactionBody, folded.entries[1].text())
	})

	t.Run("keeps a checkpoint out of the stops the reader jumps between", func(t *testing.T) {
		require.False(t, isTurn(entryCompaction))
	})
}

// TestTranscriptSwitch verifies that a selection of what the branch runs is
// shown in the conversation as a line of metadata between two turns.
func TestTranscriptSwitch(t *testing.T) {
	t.Run("folds a stored selection into the transcript", func(t *testing.T) {
		var folded transcript

		folded.load([]session.Entry{
			{ID: "u1", Kind: session.KindMessage, Message: textMessage(llm.RoleUser, "hello")},
			{
				ID:              "a1",
				Kind:            session.KindAgent,
				AgentID:         "reviewer",
				PreviousAgentID: "coder",
			},
			{
				ID:               "m1",
				Kind:             session.KindModel,
				ModelRef:         "fake/other-model",
				PreviousModelRef: "fake/test-model",
			},
		}, "coder")

		require.Len(t, folded.entries, 3)
		require.Equal(t, entrySwitch, folded.entries[1].kind)
		require.Equal(t, session.KindAgent, folded.entries[1].selectionKind)
		require.Equal(t, "coder → reviewer", folded.entries[1].text())
		require.Equal(t, entrySwitch, folded.entries[2].kind)
		require.Equal(t, session.KindModel, folded.entries[2].selectionKind)
		require.Equal(t, "fake/test-model → fake/other-model", folded.entries[2].text())
	})

	t.Run(
		"reads a selection without a recorded transition as the value it selects",
		func(t *testing.T) {
			var folded transcript

			folded.load([]session.Entry{
				{ID: "a1", Kind: session.KindAgent, AgentID: "reviewer"},
			}, "coder")

			require.Len(t, folded.entries, 1)
			require.Equal(t, "reviewer", folded.entries[0].text(),
				"a selection written before the transition was recorded still reads")
		},
	)

	t.Run("keeps the agent the branch runs at its end", func(t *testing.T) {
		var folded transcript

		folded.load([]session.Entry{
			{ID: "u1", Kind: session.KindMessage, Message: textMessage(llm.RoleUser, "hello")},
			{ID: "a1", Kind: session.KindAgent, AgentID: "reviewer"},
		}, "coder")

		require.Equal(t, "reviewer", folded.agent,
			"the answers that stream next are labeled with the agent that writes them")
	})

	t.Run("keeps a switch out of the stops the reader jumps between", func(t *testing.T) {
		require.False(t, isTurn(entrySwitch))
	})
}
