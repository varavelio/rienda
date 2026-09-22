// Package compaction summarizes the oldest turns of a conversation into a
// single checkpoint, so a session stays inside the context window of its
// model.
//
// The procedure has two steps. Prepare decides what to summarize and where the
// kept tail begins, cutting only at turn boundaries, so the kept conversation
// always starts with a complete user turn and no provider-specific repair is
// ever needed. Compact then asks the model for one summary of the range through
// the injected llm.Client, reusing the retry policy of an ordinary turn.
// Classify returns why Prepare refused to compact a branch, so a caller can
// explain the refusal instead of only knowing that one happened.
//
// The package owns no store: the caller persists the resulting checkpoint,
// which is what keeps the procedure testable without a session file. It never
// assumes that the conversation is about code, files, tools or diffs, because a
// Rienda agent may be anything.
//
// The summarization prompt is the Markdown file embedded next to this package
// and can be replaced in full by a file of the user's own, so a reader can
// review and diff the prompt as prose.
package compaction
