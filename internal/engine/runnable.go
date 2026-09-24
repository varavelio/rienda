package engine

// RunnableRefusalKind discriminates why the branch of a session holds nothing
// to run.
type RunnableRefusalKind int

const (
	// RunnableNone marks a branch that holds something to run. It is the zero
	// value.
	RunnableNone RunnableRefusalKind = iota

	// RunnableUnknownAgent marks a branch whose agent is not one of the
	// definitions the engine may run, which happens when the definition was
	// renamed or removed after the session ran on it.
	RunnableUnknownAgent

	// RunnableUnknownModel marks a branch whose model reference the
	// configuration no longer holds.
	RunnableUnknownModel
)

// RunnableRefusal explains why the branch of a session holds nothing to run.
// The zero value marks a branch that holds something to run.
type RunnableRefusal struct {
	// Kind is what the branch cannot run.
	Kind RunnableRefusalKind

	// ID is the agent identifier or the model reference the branch names,
	// which the roster no longer holds. It is empty for RunnableNone.
	ID string
}

// Runnable reports why the branch of the session holds nothing to run and
// false when it holds something: the agent the branch runs must be one of the
// definitions the engine was given and the model it runs one the resolver
// accepts.
//
// A branch that holds nothing to run still walks and reads: the conversation
// and its tree are intact, so a front end shows them and waits for a selection
// instead of refusing the session. A run fails on the same check, so the reason
// a front end explains can never disagree with what a run would do.
func (e *Engine) Runnable() (RunnableRefusal, bool) {
	if id := e.store.ActiveAgent(); !e.KnowsAgent(id) {
		return RunnableRefusal{Kind: RunnableUnknownAgent, ID: id}, true
	}
	if ref := e.store.ActiveModel(); !e.KnowsModel(ref) {
		return RunnableRefusal{Kind: RunnableUnknownModel, ID: ref}, true
	}
	return RunnableRefusal{}, false
}
