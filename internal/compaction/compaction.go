package compaction

import (
	"slices"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// Settings configures the cut point of a compaction. Only the size of the kept
// tail lives here: whether a compaction runs at all and how much room the
// answer needs belong to the caller that decides to compact.
type Settings struct {
	// KeepRecentTokens is the budget of the tail kept verbatim. The walk
	// accumulates it backwards from the newest entry and then snaps to a turn
	// boundary, so the kept tail is never larger than the budget found.
	KeepRecentTokens int
}

// Preparation is the outcome of Prepare: what to summarize, the boundary of
// the kept tail and what the summarized range measured.
type Preparation struct {
	// Messages holds the messages to summarize, in conversation order.
	Messages []llm.Message

	// PreviousSummary is the summary of an earlier compaction the range
	// resumes from, empty when there is none. It is inserted into the new
	// summarization prompt as text.
	PreviousSummary string

	// KeptID identifies the first entry kept verbatim after the compaction.
	KeptID string

	// TokensBefore is the estimated size of the summarized range, the sum of
	// the token estimate of every entry it replaces.
	TokensBefore int
}

// Deps is what Compact needs to issue the summarization call.
type Deps struct {
	// Client generates the summary. It is required.
	Client llm.Client

	// Model is the model identifier the summary is generated with.
	Model string

	// Prompt is the summarization prompt.
	Prompt string

	// MaxTokens caps the generated summary when greater than zero.
	MaxTokens int
}

// Result is the checkpoint Compact produced. The caller persists it; the
// package never touches a store.
type Result struct {
	// Summary is the checkpoint text.
	Summary string

	// KeptID identifies the first entry kept verbatim after the compaction.
	KeptID string

	// TokensBefore is the size of the summarized range.
	TokensBefore int

	// Model names the model that produced the summary.
	Model string

	// Usage reports the token consumption of the summarization call.
	Usage llm.Usage
}

// newestCompaction returns the index of the newest compaction entry of a
// branch and whether the branch holds one.
func newestCompaction(entries []session.Entry) (int, bool) {
	for index, entry := range slices.Backward(entries) {
		if entry.Kind == session.KindCompaction {
			return index, true
		}
	}
	return 0, false
}
