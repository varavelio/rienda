//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// compactionTurn returns a turn whose answer is long enough that a couple of
// them grow the conversation past the threshold of the small catalog window.
func compactionTurn(text string) harness.Turn {
	return harness.Turn{Text: strings.Repeat(text, 20), Usage: harness.Text("").Usage}
}

// TestCompactionDrivesTheWholeFeature verifies context compaction end to end: a
// session that grows past the threshold compacts, the next request carries the
// checkpoint instead of the summarized turns, the session file holds the
// compaction entry and the CLI reports it.
func TestCompactionDrivesTheWholeFeature(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{
			compactionTurn("first answer "),
			harness.Text("the checkpoint"),
			compactionTurn("second answer "),
		},
		Agents: []harness.Agent{coderAgent()},
		// The catalog is served locally and the window is small enough that
		// the second prompt crosses the threshold, so the scenario never
		// reaches models.dev.
		Catalog: map[string]int{"gpt-test": 300},
		Config:  compactionConfig(),
	})

	first := app.Run(t, "run", "-a", "coder", "-p", "first prompt")
	first.RequireSuccess(t)
	sessionID := first.SessionID(t)

	second := app.Run(t, "run", "-s", sessionID, "-p", "second prompt")
	second.RequireSuccess(t)
	require.Contains(
		t,
		second.Stderr,
		"compaction: the conversation was compacted",
		"the CLI reports the compaction on standard error",
	)
	require.Contains(
		t,
		second.Stdout,
		"second answer",
		"standard output carries only the answer of the turn",
	)
	require.NotContains(t, second.Stdout, "the checkpoint")

	// The session file holds the compaction entry with its kept boundary and
	// the usage of the summarization call.
	stored := app.Session(t, sessionID)
	require.Len(t, stored.Compactions, 1)
	compaction := stored.Compactions[0]
	require.Equal(t, "the checkpoint", compaction.Summary)
	require.NotEmpty(t, compaction.KeptID)
	require.Positive(t, compaction.TokensBefore)
	require.Equal(t, harness.DefaultModelID, compaction.ResponseModel)
	require.NotNil(t, compaction.ResponseUsage)
	require.Positive(t, compaction.ResponseUsage.InputTokens)

	// The checkpoint points at an entry the file actually holds.
	kept := false
	for _, entry := range stored.Entries {
		if entry.ID == compaction.KeptID {
			kept = true
		}
	}
	require.True(t, kept, "the kept boundary resolves to a stored entry")

	// The summarization is a non-streamed call and the request that follows the
	// compaction carries the checkpoint instead of the summarized turns.
	requests := app.Provider().Requests()
	require.Len(t, requests, 3)

	summaryRequest := requests[1].Chat(t)
	require.False(t, summaryRequest.Stream, "the summary is not streamed")
	require.Contains(t, summaryRequest.Messages[0].Text(), "### Objective")
	require.Contains(t, summaryRequest.Messages[1].Text(), "[User]: first prompt")

	continuedRequest := requests[2].Chat(t)
	replayed := joinedMessages(continuedRequest)
	require.Contains(t, replayed, "The conversation before this point was compacted")
	require.Contains(t, replayed, "the checkpoint")
	require.NotContains(t, replayed, "first prompt", "the summarized turns are gone")
	require.Contains(t, replayed, "second prompt")

	// The window the run resolved came from the catalog file, whose model
	// value is a map of facts keyed by the normalized identifier.
	require.Contains(t, app.CatalogCache(t), `"context_window":300`)
	require.Contains(t, app.CatalogCache(t), `"gpt-test"`)
}

// compactionConfig returns the configuration of the scenario: the default
// provider with a compaction reserve and tail small enough that the second
// prompt of the conversation crosses the threshold.
func compactionConfig() *harness.Config {
	cfg := harness.DefaultConfig()
	cfg.Compaction = &harness.Compaction{ReserveTokens: 50, KeepRecentTokens: 1}
	return &cfg
}

// joinedMessages concatenates the text of every replayed message of a request.
func joinedMessages(request *harness.ChatRequest) string {
	var text strings.Builder
	for _, message := range request.Messages {
		text.WriteString(message.Text())
		text.WriteString("\n")
	}
	return text.String()
}
