package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// scriptedCompactor is a scripted Compactor: it answers from the branch it is
// handed and records the calls it receives.
type scriptedCompactor struct {
	canCompact bool
	result     compaction.Result
	err        error
	compacted  int
	settings   compaction.Settings
}

// CanCompact reports the scripted readiness.
func (c *scriptedCompactor) CanCompact(branch []session.Entry) bool {
	if c.err != nil {
		return c.canCompact
	}
	_, ok := compaction.Prepare(branch, c.settings)
	return c.canCompact && ok
}

// Compact returns the scripted result.
func (c *scriptedCompactor) Compact(
	_ context.Context,
	branch []session.Entry,
) (compaction.Result, bool, error) {
	c.compacted++
	if c.err != nil {
		return compaction.Result{}, false, c.err
	}
	prep, ok := compaction.Prepare(branch, c.settings)
	if !ok {
		return compaction.Result{}, false, nil
	}
	result := c.result
	if result.KeptID == "" {
		result.KeptID = prep.KeptID
	}
	if result.TokensBefore == 0 {
		result.TokensBefore = prep.TokensBefore
	}
	if result.Summary == "" {
		result.Summary = "the summary"
	}
	if result.Model == "" {
		result.Model = "test-model"
	}
	return result, true, nil
}

// newCompactionEngine builds an engine whose conversation is grown past the
// threshold of the given compactor.
func newCompactionEngine(
	t *testing.T,
	compactor Compactor,
	compactionCfg Compaction,
	window int,
	scripts ...script,
) (*Engine, *session.Store, *fakeClient) {
	t.Helper()

	client := &fakeClient{scripts: scripts}
	engine, store := newTestEngine(t, Config{
		Client:     client,
		Agent:      agent.Agent{ID: "coder", SystemPrompt: "be brief"},
		Model:      Model{ID: "test-model", ContextWindow: window},
		Compactor:  compactor,
		Compaction: compactionCfg,
	})
	return engine, store, client
}

// grow appends a conversation of the given number of turns.
func grow(t *testing.T, store *session.Store, turns int) {
	t.Helper()

	for range turns {
		_, err := store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("x", 400)}},
		}})
		require.NoError(t, err)
		_, err = store.Append(t.Context(), session.Entry{Message: llm.Message{
			Role:   llm.RoleAssistant,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("y", 400)}},
		}})
		require.NoError(t, err)
	}
}

// TestAutomaticCompaction verifies the automatic compaction of the run loop.
func TestAutomaticCompaction(t *testing.T) {
	t.Run("compacts when the request crosses the threshold", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: true, ReserveTokens: 10},
			200,
			endTurn("done"),
		)
		grow(t, store, 4)

		events := collect(engine.Run(t.Context(), "next"))

		require.Equal(t, 1, compactor.compacted)
		require.Equal(
			t,
			[]EventType{
				EventRunStart,
				EventCompactionStart,
				EventCompactionEnd,
				EventTextDelta,
				EventMessageEnd,
				EventRunEnd,
			},
			eventTypes(events),
		)

		end := events[2].Compaction
		require.NotNil(t, end)
		require.Equal(t, session.KindCompaction, end.Entry.Kind)
		require.Equal(t, "the summary", end.Entry.CompactionSummary)
		require.Positive(t, end.TokensBefore)
		require.NotZero(t, end.TokensAfter)
		require.Less(t, end.TokensAfter, end.TokensBefore)
	})

	t.Run("compacts at most once per run", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: true, ReserveTokens: 10},
			200,
			toolTurn("call_1", "ghost", "{}"),
			endTurn("done"),
		)
		grow(t, store, 4)

		events := collect(engine.Run(t.Context(), "go"))

		require.Equal(t, 1, compactor.compacted)
		require.Equal(t, 1, countEvents(events, EventCompactionStart))
	})

	t.Run("skips a branch that already ends in a compaction", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: true, ReserveTokens: 10},
			200,
			endTurn("done"),
		)
		grow(t, store, 4)
		_, err := store.AppendCompaction(
			t.Context(),
			"an earlier summary",
			store.Branch()[0].ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)

		// Continuing from the checkpoint leaves the branch ending in it, which
		// is the state the guard refuses to compact again.
		events := collect(engine.Run(t.Context(), ""))

		require.Zero(t, compactor.compacted)
		require.Zero(t, countEvents(events, EventCompactionStart))
	})

	t.Run("compacts nothing when the setting is disabled", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: false, ReserveTokens: 10},
			200,
			endTurn("done"),
		)
		grow(t, store, 4)

		collect(engine.Run(t.Context(), "next"))

		require.Zero(t, compactor.compacted)
	})

	t.Run("compacts nothing below the threshold", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: true, ReserveTokens: 10},
			1_000_000,
			endTurn("done"),
		)
		grow(t, store, 2)

		collect(engine.Run(t.Context(), "next"))

		require.Zero(t, compactor.compacted)
	})

	t.Run("ends the run when the summarization fails", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true, err: errors.New("summary boom")}
		engine, store, client := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: true, ReserveTokens: 10},
			200,
			endTurn("done"),
		)
		grow(t, store, 4)

		events := collect(engine.Run(t.Context(), "next"))

		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
		require.Contains(t, eventError(events), "summary boom")
		require.Zero(
			t,
			countEvents(events, EventCompactionEnd),
			"a failed compaction emits no end event",
		)
		require.Zero(t, countEvents(events, EventMessageEnd), "the run never reached the model")
		require.Empty(t, client.requests)
		require.Equal(t, session.KindMessage, store.Branch()[len(store.Branch())-1].Kind)
	})

	t.Run("runs on without a compactor", func(t *testing.T) {
		engine, store, _ := newCompactionEngine(t,
			nil,
			Compaction{Enabled: true, ReserveTokens: 10},
			200,
			endTurn("done"),
		)
		grow(t, store, 4)

		events := collect(engine.Run(t.Context(), "next"))

		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)
		require.Zero(t, countEvents(events, EventCompactionStart))
	})
}

// TestManualCompaction verifies the on-demand compaction.
func TestManualCompaction(t *testing.T) {
	t.Run("compacts below the threshold", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: false, ReserveTokens: 10},
			1_000_000,
		)
		grow(t, store, 2)

		events := collect(engine.Compact(t.Context()))

		require.Equal(t, 1, compactor.compacted)
		require.Equal(
			t,
			[]EventType{EventCompactionStart, EventCompactionEnd, EventRunEnd},
			eventTypes(events),
		)
		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)
	})

	t.Run("works while the automatic compaction is disabled", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: false, ReserveTokens: 10},
			200,
		)
		grow(t, store, 2)

		events := collect(engine.Compact(t.Context()))

		require.Equal(t, 1, compactor.compacted)
		require.Equal(t, 1, countEvents(events, EventCompactionEnd))
	})

	t.Run("refuses while another run is in flight", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t,
			compactor,
			Compaction{Enabled: false},
			200,
		)
		grow(t, store, 2)

		require.True(t, engine.enter())
		events := collect(engine.Compact(t.Context()))
		engine.leave()

		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
		require.Contains(t, eventError(events), "already in flight")
		require.Zero(t, compactor.compacted)
	})

	t.Run("reports nothing to compact", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: false}
		engine, _, _ := newCompactionEngine(t, compactor, Compaction{}, 200)

		events := collect(engine.Compact(t.Context()))

		require.Equal(t, EndReasonTurn, events[len(events)-1].Reason)
		require.Zero(t, compactor.compacted)
	})

	t.Run("reports the readiness of the manual command", func(t *testing.T) {
		compactor := &scriptedCompactor{canCompact: true}
		engine, store, _ := newCompactionEngine(t, compactor, Compaction{}, 200)
		grow(t, store, 2)

		require.True(t, engine.CanCompact())

		empty, _, _ := newCompactionEngine(t, compactor, Compaction{}, 200)
		require.False(t, empty.CanCompact())

		none, _, _ := newCompactionEngine(t, nil, Compaction{}, 200)
		require.False(t, none.CanCompact())
	})

	t.Run("refuses a second run while one is in flight", func(t *testing.T) {
		engine, _, _ := newCompactionEngine(t, nil, Compaction{}, 200, endTurn("one"))

		require.True(t, engine.enter())
		events := collect(engine.Run(t.Context(), "hello"))
		engine.leave()

		require.Equal(t, EndReasonError, events[len(events)-1].Reason)
		require.Contains(t, eventError(events), "already in flight")
	})
}

// countEvents returns how many events of a type a list holds.
func countEvents(events []Event, kind EventType) int {
	count := 0
	for _, event := range events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

// eventError returns the failure carried by the first error event of a list.
func eventError(events []Event) string {
	for _, event := range events {
		if event.Type == EventError {
			return event.Error
		}
	}
	return ""
}
