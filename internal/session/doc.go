// Package session stores conversations as append-only trees of entries.
//
// A session lives in a JSONL file: a header line with its metadata followed
// by one line per entry. Entries hang from their predecessor through
// ParentID, so the history is the path from the root to the active leaf.
// Branching appends beside existing entries, so every branch stays
// recoverable.
//
// Marker entries carry the session state outside the conversation itself:
// the active leaf, tags, title, agent and model selections, and compaction
// checkpoints summarizing the oldest turns.
package session
