// Package harness wires the pieces of Rienda into runnable sessions.
//
// Preparing a session loads the configuration file, the whole roster of agent
// definitions and the model of the session, builds the provider client and the
// built-in tools, and opens a session file under the sessions directory of the
// project the working directory belongs to. Running the session then streams
// the events of one prompt.
//
// The model comes from the header of the session, so reopening a session talks
// to the provider it was created on, while the agent is resolved from the
// branch on every turn. That is what lets a conversation switch agent through
// SetAgent without a new session, a new engine or a rewritten file: the
// selection is appended to the branch that wrote it, so another branch of the
// same session keeps the agent it was running. An agent the roster does not
// hold leaves the branch with nothing to run until another selection, or the
// header, names one that exists.
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
