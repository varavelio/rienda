// Package harness wires the pieces of Rienda into runnable sessions.
//
// Preparing a session loads the configuration file, the whole roster of agent
// definitions and the model of the session, builds the provider client and the
// built-in tools, and opens a session file under the sessions directory of the
// project the working directory belongs to. Running the session then streams
// the events of one prompt.
//
// Neither the agent nor the model is fixed for the life of a session: the agent
// is resolved from the branch on every turn, and the model is resolved through
// the modelResolver this package injects into the engine, which turns a
// provider/model reference into a model and one cached client per reference.
// Every client it hands out carries the identity of the session, so every call
// identifies itself with it, a summarization as much as a conversation turn,
// and a provider that routes by session never refuses one of them.
// The model of a branch is described to a front end through ModelInfo, which
// reports the wire identifier a reference resolves to and the extended thinking
// level the configuration declares for it.
//
// That is what lets a conversation switch agent through SetAgent, or model
// through SetModel, without a new session, a new engine or a rewritten file:
// the selection is appended to the branch that wrote it, so another branch of
// the same session keeps what it was running, and the header records what the
// session was created with. An agent or a model the roster does not hold leaves
// the branch with nothing to run until another selection names one that exists,
// which a front end reads through Runnable: the conversation still opens and is
// read, and the run refuses on the same check, so the reason a front end
// explains never disagrees with what a run would do.
//
// Preparing a session also resolves the context window of its model, from the
// configuration, the model catalog or a conservative fallback, and builds the
// compactor that summarizes the conversation when it grows too large. The
// summarization runs on the session model unless the configuration declares
// compaction.model, and its prompt is the built-in one unless the user
// overrides it.
//
// The package owns the wiring only: how sessions are presented to a user is
// left to the callers, so the headless command and future interactive front
// ends share the same preparation and execution path.
package harness
