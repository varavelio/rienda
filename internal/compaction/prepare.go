package compaction

import (
	"slices"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
)

// Prepare decides the cut point of a compaction over a branch: what to
// summarize, the boundary of the kept tail and what the range measured. The
// returned boolean is false for every refusal, which is an ordinary outcome
// and never a panic:
//
//   - the branch is empty;
//   - the branch already ends in a compaction;
//   - nothing is left to summarize, because the cut landed at the start, which
//     Classify explains as RefusalEmpty, RefusalShort or RefusalNoTurn.
//
// When a previous compaction exists, its summary becomes PreviousSummary and
// the range to consider starts at its kept entry, so the messages that survived
// the last compaction are folded into the next one instead of being orphaned.
// The chaining is deliberately literal: a previous summary is text that goes
// into the new prompt and the range is a slice of the branch, so repeated
// compactions compose with no code of their own.
func Prepare(entries []session.Entry, settings Settings) (Preparation, bool) {
	if len(entries) == 0 {
		return Preparation{}, false
	}
	if entries[len(entries)-1].Kind == session.KindCompaction {
		return Preparation{}, false
	}

	start := rangeStart(entries)
	cut, found := cutPoint(entries, start, settings.KeepRecentTokens)
	if !found || cut <= start {
		return Preparation{}, false
	}

	return Preparation{
		Messages:        messagesOf(entries[start:cut]),
		PreviousSummary: previousSummary(entries),
		KeptID:          entries[cut].ID,
		TokensBefore:    tokensBefore(entries[start:cut]),
	}, true
}

// rangeStart returns the index the range to consider begins at: the kept entry
// of the newest compaction, so the messages that survived it are folded into
// the next one instead of being orphaned, or the start of the branch when it
// holds none.
func rangeStart(entries []session.Entry) int {
	index, found := newestCompaction(entries)
	if !found {
		return 0
	}
	return indexOf(entries, entries[index].CompactionKeptID)
}

// previousSummary returns the summary of the newest compaction of a branch,
// empty when the branch holds none.
func previousSummary(entries []session.Entry) string {
	if index, found := newestCompaction(entries); found {
		return entries[index].CompactionSummary
	}
	return ""
}

// hasTurnBoundary reports whether a range holds at least one turn boundary, so
// a cut could ever land inside it.
func hasTurnBoundary(entries []session.Entry, start int) bool {
	return slices.ContainsFunc(entries[start:], isTurnBoundary)
}

// cutPoint returns the index of the first entry of the kept tail: the walk
// accumulates the token estimate backwards from the newest entry until the
// budget is reached, and the result then snaps to the nearest turn boundary,
// preferring the newer one so the kept tail is never larger than the walk
// found.
func cutPoint(entries []session.Entry, start, budget int) (int, bool) {
	sum := 0
	cut := start
	for index := len(entries) - 1; index >= start; index-- {
		sum += tokens.OfMessage(entries[index].Message)
		cut = index
		if sum >= budget {
			break
		}
	}

	for index := cut; index < len(entries); index++ {
		if isTurnBoundary(entries[index]) {
			return index, true
		}
	}
	// The newest turn alone exceeds the budget and the cut landed inside it,
	// so the boundary that opens it is the newest one available. The kept tail
	// is that turn: a degraded but valid compaction, and the deliberate trade
	// for not splitting a turn.
	for index := cut - 1; index >= start; index-- {
		if isTurnBoundary(entries[index]) {
			return index, true
		}
	}
	return 0, false
}

// isTurnBoundary reports whether an entry can open the kept tail: a user
// message that carries at least one text block. The rule is what keeps the
// kept tail valid for every provider: a tool result lives in the user message
// that follows its call and is not a boundary, so a cut never separates a call
// from its result.
func isTurnBoundary(entry session.Entry) bool {
	if entry.Kind != session.KindMessage || entry.Message.Role != llm.RoleUser {
		return false
	}
	for _, block := range entry.Message.Blocks {
		if block.Type == llm.BlockText {
			return true
		}
	}
	return false
}

// messagesOf returns the messages carried by a range of entries. A checkpoint
// that falls inside the range is skipped, because it is not a message.
func messagesOf(entries []session.Entry) []llm.Message {
	messages := make([]llm.Message, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != session.KindMessage {
			continue
		}
		messages = append(messages, entry.Message)
	}
	return messages
}

// tokensBefore returns the estimated size of a range of entries.
func tokensBefore(entries []session.Entry) int {
	sum := 0
	for _, entry := range entries {
		sum += tokens.OfMessage(entry.Message)
	}
	return sum
}

// indexOf returns the index of the entry identified by id, or zero when it is
// absent. The absence is defensive: a session Rienda wrote always resolves,
// because the decoder rejects a compaction whose kept entry is unknown.
func indexOf(entries []session.Entry, id string) int {
	for index, entry := range entries {
		if entry.ID == id {
			return index
		}
	}
	return 0
}
