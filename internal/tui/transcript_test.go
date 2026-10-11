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

	t.Run("keeps an answer one block while the reasoning interleaves it", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(
			engine.Event{Type: engine.EventThinkingDelta, Text: "Let me gather this "},
		)
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Invest"})
		conversation.apply(engine.Event{Type: engine.EventThinkingDelta, Text: " more"})
		conversation.apply(
			engine.Event{Type: engine.EventTextDelta, Text: "igo where the error is born."},
		)

		require.Len(t, conversation.entries, 2)
		require.Equal(t, entryThinking, conversation.entries[0].kind)
		require.Equal(t, "Let me gather this  more", conversation.entries[0].text())
		require.Equal(t, entryAssistant, conversation.entries[1].kind)
		require.Equal(t, "Investigo where the error is born.", conversation.entries[1].text())
	})

	t.Run("starts a new answer after a tool result", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Invest"})
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
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "igo further."})

		require.Len(t, conversation.entries, 3)
		require.Equal(t, "Invest", conversation.entries[0].text())
		require.Equal(t, entryTool, conversation.entries[1].kind)
		require.Equal(t, entryAssistant, conversation.entries[2].kind)
		require.Equal(t, "igo further.", conversation.entries[2].text())
	})

	t.Run("starts a new answer when a run streams after a finished message", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "Invest"})
		conversation.apply(engine.Event{Type: engine.EventMessageEnd})
		conversation.apply(engine.Event{Type: engine.EventTextDelta, Text: "igo further."})

		require.Len(t, conversation.entries, 2)
		require.Equal(t, "Invest", conversation.entries[0].text())
		require.Equal(t, "igo further.", conversation.entries[1].text())
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
		}, "coder", nil)

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
		}, "coder", nil)

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
		}, "coder", nil)

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

	t.Run("folds the diagnostics of a workspace into notices", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{
			Type: engine.EventRunStart,
			Diagnostics: []string{
				"skill ./.agents/skills/broken/SKILL.md: the description is missing or empty",
				"skill ./.agents/skills/other/SKILL.md: the name is missing or empty",
			},
		})

		require.Len(t, conversation.entries, 2)
		require.Equal(t, entryNotice, conversation.entries[0].kind)
		require.Equal(t,
			"skill ./.agents/skills/broken/SKILL.md: the description is missing or empty",
			conversation.entries[0].text(),
		)
		require.Equal(t, entryNotice, conversation.entries[1].kind)
	})

	t.Run("folds a run start without diagnostics into nothing", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventRunStart})

		require.Empty(t, conversation.entries)
	})

	t.Run("ignores diagnostics reported outside a run start", func(t *testing.T) {
		conversation := transcript{}
		conversation.apply(engine.Event{Type: engine.EventContext, Diagnostics: []string{"late"}})

		require.Empty(t, conversation.entries)
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
		}, "coder", nil)

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
		}, "coder", nil)

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

// TestSelectionText verifies the transition a selection reads as: the values it
// recorded, named the way the interface names them. A model is named through
// the namer the caller supplies, and named by its reference when there is none,
// so a caller without a configuration still reads the transition.
func TestSelectionText(t *testing.T) {
	// displayName names a model the way a configured provider would.
	displayName := func(ref string) string {
		return map[string]string{
			"fake/test-model":  "Fake Provider · Test Model",
			"fake/other-model": "Fake Provider · Other Model",
		}[ref]
	}

	t.Run("names both models of a switch through the namer", func(t *testing.T) {
		selection := session.Entry{
			Kind:             session.KindModel,
			ModelRef:         "fake/other-model",
			PreviousModelRef: "fake/test-model",
		}

		require.Equal(
			t,
			"Fake Provider · Test Model → Fake Provider · Other Model",
			selectionText(selection, displayName),
		)
	})

	t.Run("names a model by its reference without a namer", func(t *testing.T) {
		selection := session.Entry{
			Kind:             session.KindModel,
			ModelRef:         "fake/other-model",
			PreviousModelRef: "fake/test-model",
		}

		require.Equal(t, "fake/test-model → fake/other-model", selectionText(selection, nil))
	})

	t.Run("reads a selection with no previous value as the value it selects", func(t *testing.T) {
		selection := session.Entry{Kind: session.KindModel, ModelRef: "fake/other-model"}

		require.Equal(t, "Fake Provider · Other Model", selectionText(selection, displayName))
	})

	t.Run("leaves an agent switch on the agent identifiers", func(t *testing.T) {
		selection := session.Entry{
			Kind:            session.KindAgent,
			AgentID:         "reviewer",
			PreviousAgentID: "coder",
		}

		require.Equal(t, "coder → reviewer", selectionText(selection, displayName))
	})

	t.Run("names the thinking levels of a switch by their mode", func(t *testing.T) {
		on := session.Entry{Kind: session.KindThinking, ThinkingLevel: "high"}
		require.Equal(t, "off → high", selectionText(on, displayName),
			"the empty level the branch ran before reads as off")

		off := session.Entry{
			Kind:                  session.KindThinking,
			ThinkingLevel:         "",
			PreviousThinkingLevel: "high",
		}
		require.Equal(t, "high → off", selectionText(off, displayName),
			"turning the thinking off reads as a transition of its own")

		move := session.Entry{
			Kind:                  session.KindThinking,
			ThinkingLevel:         "low",
			PreviousThinkingLevel: "high",
		}
		require.Equal(t, "high → low", selectionText(move, displayName))
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
		}, "coder", nil)

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
		}, "coder", nil)

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
			}, "coder", nil)

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
		}, "coder", nil)

		require.Equal(t, "reviewer", folded.agent,
			"the answers that stream next are labeled with the agent that writes them")
	})

	t.Run("keeps a switch out of the stops the reader jumps between", func(t *testing.T) {
		require.False(t, isTurn(entrySwitch))
	})

	t.Run("folds the switches that follow one another into one", func(t *testing.T) {
		var folded transcript

		folded.load([]session.Entry{
			switchEntry("m1", "u0", session.KindModel, "fake/a", "fake/b"),
			switchEntry("m2", "m1", session.KindModel, "fake/b", "fake/c"),
			switchEntry("m3", "m2", session.KindModel, "fake/c", "fake/d"),
		}, "coder", nil)

		require.Len(t, folded.entries, 1)
		require.Equal(t, "fake/a → fake/d", folded.entries[0].text(),
			"a model cycled through three options is one change in effect")
	})
}

// switchEntry builds a switch of the given kind that hangs from parent and
// moves the branch from left to right, the way the session records one.
func switchEntry(id, parent string, kind session.Kind, left, to string) session.Entry {
	entry := session.Entry{ID: id, ParentID: parent, Kind: kind}
	if kind == session.KindThinking {
		entry.PreviousThinkingLevel = left
		entry.ThinkingLevel = to
		return entry
	}
	entry.PreviousModelRef = left
	entry.ModelRef = to
	return entry
}

func TestCollapseSwitches(t *testing.T) {
	t.Run("folds a run of model switches into the change it amounts to", func(t *testing.T) {
		folded := collapseSwitches([]session.Entry{
			switchEntry("m1", "u0", session.KindModel, "fake/a", "fake/b"),
			switchEntry("m2", "m1", session.KindModel, "fake/b", "fake/c"),
			switchEntry("m3", "m2", session.KindModel, "fake/c", "fake/d"),
		})

		require.Len(t, folded, 1)
		require.Equal(t, "m1", folded[0].ID)
		require.Equal(t, "fake/a", folded[0].PreviousModelRef)
		require.Equal(t, "fake/d", folded[0].ModelRef)
	})

	t.Run("drops a run that returns to where it started", func(t *testing.T) {
		folded := collapseSwitches([]session.Entry{
			switchEntry("m1", "u0", session.KindModel, "fake/a", "fake/b"),
			switchEntry("m2", "m1", session.KindModel, "fake/b", "fake/a"),
			switchEntry("t1", "m2", session.KindThinking, "", "high"),
		})

		require.Len(t, folded, 1)
		require.Equal(t, "t1", folded[0].ID, "the run that changed nothing leaves no switch")
		require.Equal(t, "u0", folded[0].ParentID,
			"the turn after a dropped run hangs from the entry the run hung from")
	})

	t.Run("keeps switches of different kinds apart", func(t *testing.T) {
		folded := collapseSwitches([]session.Entry{
			switchEntry("m1", "u0", session.KindModel, "fake/a", "fake/b"),
			switchEntry("t1", "m1", session.KindThinking, "", "high"),
			switchEntry("m2", "t1", session.KindModel, "fake/b", "fake/c"),
		})

		require.Len(t, folded, 3, "a thinking switch between two model ones breaks the run")
	})

	t.Run("starts a new run when a turn comes between", func(t *testing.T) {
		folded := collapseSwitches([]session.Entry{
			switchEntry("m1", "u0", session.KindModel, "fake/a", "fake/b"),
			{
				ID:       "u1",
				ParentID: "m1",
				Kind:     session.KindMessage,
				Message:  textMessage(llm.RoleUser, "hi"),
			},
			switchEntry("m2", "u1", session.KindModel, "fake/b", "fake/c"),
		})

		require.Len(t, folded, 3)
		require.Equal(t, "fake/a", folded[0].PreviousModelRef)
		require.Equal(t, "fake/b", folded[0].ModelRef, "a switch away from a turn is not folded")
	})

	t.Run("folds a run of thinking switches", func(t *testing.T) {
		folded := collapseSwitches([]session.Entry{
			switchEntry("t1", "u0", session.KindThinking, "", "high"),
			switchEntry("t2", "t1", session.KindThinking, "high", "low"),
		})

		require.Len(t, folded, 1)
		require.Equal(t, "", folded[0].PreviousThinkingLevel)
		require.Equal(t, "low", folded[0].ThinkingLevel)
	})

	t.Run("re-hangs the turns that followed a folded run", func(t *testing.T) {
		folded := collapseSwitches([]session.Entry{
			switchEntry("m1", "u0", session.KindModel, "fake/a", "fake/b"),
			switchEntry("m2", "m1", session.KindModel, "fake/b", "fake/c"),
			{
				ID:       "u1",
				ParentID: "m2",
				Kind:     session.KindMessage,
				Message:  textMessage(llm.RoleUser, "hi"),
			},
		})

		require.Len(t, folded, 2)
		require.Equal(t, "m1", folded[1].ParentID, "the turn hangs from the switch that remains")
	})
}

func TestDisplayAgent(t *testing.T) {
	agentEntry := func(id, parent, agent string) session.Entry {
		return session.Entry{ID: id, ParentID: parent, Kind: session.KindAgent, AgentID: agent}
	}

	t.Run("reads the agent the session was created with when it chose none", func(t *testing.T) {
		branch := []session.Entry{
			{ID: "m1", Kind: session.KindMessage},
		}

		require.Equal(t, "coder", displayAgent(branch, branch, "coder"))
	})

	t.Run("reads the selection a compaction dropped", func(t *testing.T) {
		branch := []session.Entry{
			{ID: "m1", Kind: session.KindMessage},
			agentEntry("a1", "m1", "reviewer"),
			{ID: "m2", ParentID: "a1", Kind: session.KindMessage},
		}
		displayed := branch[2:]

		require.Equal(t, "reviewer", displayAgent(branch, displayed, "coder"),
			"the turns after the checkpoint run on the dropped selection")
	})

	t.Run("leaves the selections the conversation shows to the fold", func(t *testing.T) {
		branch := []session.Entry{
			{ID: "m1", Kind: session.KindMessage},
			agentEntry("a1", "m1", "reviewer"),
			agentEntry("a2", "a1", "coder"),
			{ID: "m2", ParentID: "a2", Kind: session.KindMessage},
		}

		require.Equal(t, "coder", displayAgent(branch, branch[1:], "coder"),
			"the seed stops where the displayed branch starts")
	})

	t.Run("labels the turns after a checkpoint with the agent the branch runs", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		require.NoError(t, stored.store.SetAgent(t.Context(), "reviewer"))
		_, err := stored.store.Append(
			t.Context(),
			session.Entry{Message: textMessage(llm.RoleUser, "again")},
		)
		require.NoError(t, err)
		kept := stored.store.Branch()[len(stored.store.Branch())-1]
		_, err = stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			kept.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)

		m.reloadTranscript()

		require.Equal(t, "reviewer", m.identity.agent,
			"the header reads the agent the branch runs")
		for _, entry := range m.transcript.entries {
			if entry.kind == entryAssistant || entry.kind == entryUser {
				require.Equal(t, "reviewer", entry.agent,
					"a turn the checkpoint spared runs on the dropped selection")
			}
		}
	})
}
