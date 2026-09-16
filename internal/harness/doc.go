// Package harness wires the pieces of Rienda into runnable sessions.
//
// Preparing a session loads the configuration file, the agent definition and
// the model the agent runs, builds the provider client and the built-in tools,
// and opens a session file under the sessions directory of the project the
// working directory belongs to. Running the session then streams the events of
// one prompt.
//
// The package owns the wiring only: how sessions are presented to a user is
// left to the callers, so the headless command and future interactive front
// ends share the same preparation and execution path.
package harness
