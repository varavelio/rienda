package compaction

import "github.com/varavelio/rienda/internal/session"

// RefusalKind discriminates why a branch holds nothing to compact. It exists so
// the interface can explain a refusal instead of only knowing that one
// happened.
type RefusalKind int

const (
	// RefusalNone marks a branch that can be compacted. It is the zero value.
	RefusalNone RefusalKind = iota
	// RefusalEmpty marks a branch that holds no conversation yet.
	RefusalEmpty
	// RefusalCompacted marks a branch that already ends in a compaction.
	RefusalCompacted
	// RefusalNoTurn marks a branch that holds no turn the kept tail could
	// start at, which only a hand-edited or truncated file can produce.
	RefusalNoTurn
	// RefusalShort marks a branch whose considerable conversation is smaller
	// than the tail it must keep verbatim.
	RefusalShort
)

// Refusal explains why a branch holds nothing to compact. The zero value marks
// a branch that can be compacted.
type Refusal struct {
	// Kind is the reason the branch holds nothing to compact.
	Kind RefusalKind

	// Needed is the tokens of history the branch still lacks before it holds
	// something to summarize. It is positive for RefusalShort only, and zero
	// when the range is small but holds nothing older than its first turn.
	Needed int
}

// Classify returns why a branch holds nothing to compact. It never disagrees
// with Prepare: the reason is classified only once Prepare refused, so the
// boolean and the explanation are the same decision seen twice.
func Classify(entries []session.Entry, settings Settings) Refusal {
	if _, ok := Prepare(entries, settings); ok {
		return Refusal{}
	}

	switch {
	case len(entries) == 0:
		return Refusal{Kind: RefusalEmpty}
	case entries[len(entries)-1].Kind == session.KindCompaction:
		return Refusal{Kind: RefusalCompacted}
	}

	start := rangeStart(entries)
	if !hasTurnBoundary(entries, start) {
		return Refusal{Kind: RefusalNoTurn}
	}
	// A boundary exists, so the cut landed before the first one: the range is
	// the whole conversation kept verbatim. The tokens it still lacks are what
	// the user has to add before anything can be summarized.
	return Refusal{
		Kind:   RefusalShort,
		Needed: max(0, settings.KeepRecentTokens-tokensBefore(entries[start:])),
	}
}
