// Package state stores the global user state of Rienda: the small document
// that remembers where the user left off across sessions and processes.
//
// The document lives at "~/.rienda/state.json" and holds the last model
// reference used and, per model reference, the last thinking level picked
// for it. It only seeds the choice of a new session: every running session
// keeps its own model and thinking in its file, so the state is a default,
// never a source of truth. Multiple Rienda instances rewrite it freely: the
// last writer defines the next default, and the atomic write keeps the file
// intact while the races resolve.
//
// Writes always follow a fresh read and replace the file atomically, so a
// rewrite cannot lose another process's change and a crash cannot truncate
// the document. Nothing here talks to a session file; the boundaries are the
// ones the harness defines.
package state
