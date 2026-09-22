package compaction

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
)

// userEntry builds a user message entry carrying text.
func userEntry(id, text string) session.Entry {
	return session.Entry{
		ID:   id,
		Kind: session.KindMessage,
		Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: text}},
		},
	}
}

// assistantEntry builds an assistant message entry carrying text.
func assistantEntry(id, text string) session.Entry {
	return session.Entry{
		ID:   id,
		Kind: session.KindMessage,
		Message: llm.Message{
			Role:   llm.RoleAssistant,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: text}},
		},
	}
}

// toolResultEntry builds the user message that carries a tool result.
func toolResultEntry(id string) session.Entry {
	return session.Entry{
		ID:   id,
		Kind: session.KindMessage,
		Message: llm.Message{
			Role: llm.RoleUser,
			Blocks: []llm.Block{{
				Type:       llm.BlockToolResult,
				ToolResult: []llm.Block{{Type: llm.BlockText, Text: "output"}},
			}},
		},
	}
}

// compactionEntry builds a checkpoint entry keeping keptID.
func compactionEntry(id, keptID, summary string) session.Entry {
	return session.Entry{
		ID:                     id,
		Kind:                   session.KindCompaction,
		CompactionSummary:      summary,
		CompactionKeptID:       keptID,
		CompactionTokensBefore: 1,
	}
}

// TestPrepare verifies the cut point of a compaction.
func TestPrepare(t *testing.T) {
	t.Run("refuses an empty branch", func(t *testing.T) {
		_, ok := Prepare(nil, Settings{KeepRecentTokens: 10})

		require.False(t, ok)
	})

	t.Run("refuses a branch that already ends in a compaction", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "one"),
			assistantEntry("a1", "two"),
			compactionEntry("c1", "u1", "summary"),
		}

		_, ok := Prepare(branch, Settings{KeepRecentTokens: 10})

		require.False(t, ok)
	})

	t.Run("cuts at the exact boundary when the budget lands on it", func(t *testing.T) {
		// Each message is 1 byte of text, so 4 bytes and one token.
		branch := []session.Entry{
			userEntry("u1", "aaaa"),
			assistantEntry("a1", "aaaa"),
			userEntry("u2", "aaaa"),
			assistantEntry("a2", "aaaa"),
		}

		prep, ok := Prepare(branch, Settings{KeepRecentTokens: 2})

		require.True(t, ok)
		require.Equal(t, "u2", prep.KeptID)
		require.Len(t, prep.Messages, 2)
		require.Equal(t, branch[0].Message, prep.Messages[0])
		require.Equal(t, branch[1].Message, prep.Messages[1])
		require.Equal(t, 2, prep.TokensBefore)
		require.Empty(t, prep.PreviousSummary)
	})

	t.Run("snaps forward to the newer turn boundary", func(t *testing.T) {
		// The walk reaches the budget inside the second turn, so the cut snaps
		// forward onto the user message that opens it.
		branch := []session.Entry{
			userEntry("u1", "aaaaaaaa"),
			assistantEntry("a1", "aaaaaaaa"),
			userEntry("u2", "aaaaaaaa"),
			assistantEntry("a2", "aaaaaaaa"),
			userEntry("u3", "aaaaaaaa"),
			assistantEntry("a3", "aaaaaaaa"),
		}

		prep, ok := Prepare(branch, Settings{KeepRecentTokens: 3})

		require.True(t, ok)
		require.Equal(t, "u3", prep.KeptID)
		require.Len(t, prep.Messages, 4)
	})

	t.Run("never cuts between a tool call and its result", func(t *testing.T) {
		// The result is a user message without text, so it is not a boundary
		// and the cut lands on the prompt that opened the turn.
		branch := []session.Entry{
			userEntry("u1", "aaaaaaaa"),
			assistantEntry("a1", "aaaaaaaa"),
			userEntry("u2", "go"),
			{
				ID:   "a2",
				Kind: session.KindMessage,
				Message: llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{
					Type:         llm.BlockToolCall,
					ToolCallName: "shell",
				}}},
			},
			toolResultEntry("r2"),
		}

		prep, ok := Prepare(branch, Settings{KeepRecentTokens: 1})

		require.True(t, ok)
		require.Equal(t, "u2", prep.KeptID)
		require.Len(t, prep.Messages, 2)
	})

	t.Run("refuses when nothing is left to summarize", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "one"),
			assistantEntry("a1", "two"),
		}

		_, ok := Prepare(branch, Settings{KeepRecentTokens: 1000})

		require.False(t, ok)
	})

	t.Run("refuses a branch whose leaf is the checkpoint", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "one"),
			compactionEntry("c1", "u1", "summary"),
		}

		_, ok := Prepare(branch, Settings{KeepRecentTokens: 0})

		require.False(t, ok)
	})

	t.Run("refuses a branch with no turn boundary at all", func(t *testing.T) {
		branch := []session.Entry{
			toolResultEntry("r1"),
			assistantEntry("a1", "two"),
			toolResultEntry("r2"),
		}

		_, ok := Prepare(branch, Settings{KeepRecentTokens: 1})

		require.False(t, ok)
	})

	t.Run("resumes from the previous kept entry and carries its summary", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "aaaaaaaa"),
			assistantEntry("a1", "aaaaaaaa"),
			compactionEntry("c1", "u2", "the first summary"),
			userEntry("u2", "aaaaaaaa"),
			assistantEntry("a2", "aaaaaaaa"),
			userEntry("u3", "aaaaaaaa"),
			assistantEntry("a3", "aaaaaaaa"),
		}

		prep, ok := Prepare(branch, Settings{KeepRecentTokens: 3})

		require.True(t, ok)
		require.Equal(t, "the first summary", prep.PreviousSummary)
		require.Equal(t, "u3", prep.KeptID)
		require.Len(t, prep.Messages, 2)
		require.Equal(t, branch[3].Message, prep.Messages[0])
	})

	t.Run("keeps an oversized newest turn without failing", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "aaaa"),
			assistantEntry("a1", "aaaa"),
			userEntry("u2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			assistantEntry("a2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		}

		prep, ok := Prepare(branch, Settings{KeepRecentTokens: 1})

		require.True(t, ok)
		require.Equal(t, "u2", prep.KeptID)
		require.Len(t, prep.Messages, 2)
		require.Equal(t, 2, prep.TokensBefore)
	})

	t.Run("counts the summarized range with the token estimate", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "hello world"),
			assistantEntry("a1", "done"),
			userEntry("u2", "next"),
			assistantEntry("a2", "answer"),
		}

		prep, ok := Prepare(branch, Settings{KeepRecentTokens: 2})

		require.True(t, ok)
		require.Equal(
			t,
			tokens.OfMessage(branch[0].Message)+tokens.OfMessage(branch[1].Message),
			prep.TokensBefore,
		)
	})
}

// TestClassify verifies the reason a branch holds nothing to compact.
func TestClassify(t *testing.T) {
	t.Run("reports nothing for a branch that can be compacted", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "aaaa"),
			assistantEntry("a1", "aaaa"),
			userEntry("u2", "aaaa"),
			assistantEntry("a2", "aaaa"),
		}

		refusal := Classify(branch, Settings{KeepRecentTokens: 2})

		require.Equal(t, RefusalNone, refusal.Kind)
	})

	t.Run("reports an empty branch", func(t *testing.T) {
		refusal := Classify(nil, Settings{KeepRecentTokens: 20000})

		require.Equal(t, RefusalEmpty, refusal.Kind)
		require.Zero(t, refusal.Needed)
	})

	t.Run("reports a branch that already ends in a compaction", func(t *testing.T) {
		branch := []session.Entry{
			userEntry("u1", "one"),
			compactionEntry("c1", "u1", "summary"),
		}

		refusal := Classify(branch, Settings{KeepRecentTokens: 1})

		require.Equal(t, RefusalCompacted, refusal.Kind)
		require.Zero(t, refusal.Needed)
	})

	t.Run("reports the tokens a short branch still lacks", func(t *testing.T) {
		// Two messages of four bytes each are two tokens, against a budget of
		// ten: eight tokens of history are missing.
		branch := []session.Entry{
			userEntry("u1", "aaaa"),
			assistantEntry("a1", "aaaa"),
		}

		refusal := Classify(branch, Settings{KeepRecentTokens: 10})

		require.Equal(t, RefusalShort, refusal.Kind)
		require.Equal(t, 8, refusal.Needed)
	})

	t.Run("reports nothing lacking for a small range with no earlier turn", func(t *testing.T) {
		// The range is the whole conversation, which is smaller than the
		// budget, but its only turn is the one the walk starts at, so there is
		// never anything older to summarize however much history is added.
		branch := []session.Entry{
			userEntry("u1", "aaaa"),
			assistantEntry("a1", "aaaa"),
		}

		refusal := Classify(branch, Settings{KeepRecentTokens: 1})

		require.Equal(t, RefusalShort, refusal.Kind)
		require.Zero(t, refusal.Needed)
	})

	t.Run("reports a branch with no turn boundary at all", func(t *testing.T) {
		branch := []session.Entry{
			toolResultEntry("r1"),
			assistantEntry("a1", "two"),
			toolResultEntry("r2"),
		}

		refusal := Classify(branch, Settings{KeepRecentTokens: 1})

		require.Equal(t, RefusalNoTurn, refusal.Kind)
		require.Zero(t, refusal.Needed)
	})

	t.Run("agrees with Prepare for every case", func(t *testing.T) {
		// The explanation is only meaningful when it cannot disagree with the
		// boolean, so every classification is checked against Prepare itself.
		cases := []struct {
			name     string
			branch   []session.Entry
			settings Settings
		}{
			{name: "empty", branch: nil, settings: Settings{KeepRecentTokens: 10}},
			{
				name:     "compacted",
				branch:   []session.Entry{userEntry("u1", "one"), compactionEntry("c1", "u1", "s")},
				settings: Settings{KeepRecentTokens: 1},
			},
			{
				name:     "short",
				branch:   []session.Entry{userEntry("u1", "aaaa"), assistantEntry("a1", "aaaa")},
				settings: Settings{KeepRecentTokens: 10},
			},
			{
				name:     "no turn",
				branch:   []session.Entry{toolResultEntry("r1"), assistantEntry("a1", "two")},
				settings: Settings{KeepRecentTokens: 1},
			},
			{
				name: "compactable",
				branch: []session.Entry{
					userEntry("u1", "aaaa"),
					assistantEntry("a1", "aaaa"),
					userEntry("u2", "aaaa"),
					assistantEntry("a2", "aaaa"),
				},
				settings: Settings{KeepRecentTokens: 2},
			},
		}

		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				_, ok := Prepare(test.branch, test.settings)
				refusal := Classify(test.branch, test.settings)

				require.Equal(t, ok, refusal.Kind == RefusalNone)
			})
		}
	})
}
