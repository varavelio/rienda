package session

import (
	"time"

	"github.com/varavelio/rienda/internal/llm"
)

// Kind discriminates the payload carried by an Entry.
type Kind string

const (
	// KindHeader opens every session file and carries the session metadata.
	KindHeader Kind = "header"
	// KindMessage carries a conversation turn.
	KindMessage Kind = "message"
	// KindLeaf moves the active leaf of the tree without carrying content, so
	// the next message continues from the entry it targets.
	KindLeaf Kind = "leaf"
	// KindTag labels the entry it targets, so a turn of the conversation can
	// be found again by what it is about.
	KindTag Kind = "tag"
	// KindTitle names the session, so the user finds it again in the list of
	// stored sessions by what it is about. The marker describes the whole
	// name, so the last one wins and an empty one removes it.
	KindTitle Kind = "title"
	// KindCompaction replaces every entry before the kept one with a summary
	// of the conversation, so a session stays inside the context window of its
	// model. The checkpoint hangs from the active leaf, so it belongs to the
	// branch that produced it and to no other.
	KindCompaction Kind = "compaction"
	// KindAgent selects the agent the branch runs from this entry onward, so
	// one conversation can plan with one agent and implement with another.
	// Like a compaction, it hangs from the active leaf and belongs to the
	// branch that wrote it, and it describes the whole selection, so the
	// newest one of a branch wins.
	KindAgent Kind = "agent"
	// KindModel selects the model the branch runs from this entry onward, so
	// one conversation can be planned by a strong model and implemented by a
	// cheap one. It behaves exactly like a KindAgent entry: it hangs from the
	// active leaf, it belongs to the branch that wrote it, and the newest one
	// of a branch wins.
	KindModel Kind = "model"
)

// Entry is a single node of the session tree. Only the fields valid for the
// entry Kind carry meaning; the rest must be left at their zero value.
//
// Field naming follows the role each field plays, so a flat struct stays
// self-describing: fields describing the model response that produced a
// message are prefixed Response, and fields of future entry kinds will be
// prefixed by the kind they belong to.
type Entry struct {
	// ID is the unique identifier of the entry.
	ID string

	// ParentID links the entry to the entry it follows. It is empty for the
	// first message of the session.
	ParentID string

	// CreatedAt is the moment the entry was appended.
	CreatedAt time.Time

	// Kind selects which fields of the entry carry meaning.
	Kind Kind

	// Message carries the conversation turn of KindMessage entries.
	Message llm.Message

	// ResponseModel names the model that produced the response of an
	// assistant message.
	ResponseModel string

	// ResponseStopReason explains why the assistant response ended.
	ResponseStopReason llm.StopReason

	// ResponseUsage reports the token consumption of the assistant response.
	ResponseUsage llm.Usage

	// Tag labels the entry, empty when it carries none. It is a property of
	// the message it labels, recorded by a marker of its own so the message
	// line never changes.
	Tag string

	// CompactionSummary is the checkpoint text of a KindCompaction entry: the
	// summary of everything the entry replaces.
	CompactionSummary string

	// CompactionKeptID identifies the first entry kept verbatim after the
	// compaction. It is a pointer rather than a copy, because the session file
	// is append-only and the entries it points at are always there.
	CompactionKeptID string

	// CompactionTokensBefore is what the summarized range measured before it
	// was compacted.
	CompactionTokensBefore int

	// AgentID is the identifier of the agent a KindAgent entry selects, empty
	// when it selects none. The entry carries no message: it only moves the
	// branch it hangs from onto another agent.
	AgentID string

	// ModelRef is the provider/model reference a KindModel entry selects,
	// empty when it selects none. The reference, never the resolved settings,
	// is what a session stores: credentials belong to the configuration of the
	// user and must never reach a session file. The entry carries no message:
	// it only moves the branch it hangs from onto another model.
	ModelRef string
}
