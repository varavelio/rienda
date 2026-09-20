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
}
