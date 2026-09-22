// Package tokens estimates how much of a model context window a request
// consumes.
//
// The estimation counts the bytes of everything the model reads — the system
// prompt, the conversation and the tool definitions — and divides them by a
// fixed bytes-per-token ratio. It is a pure package: it depends on internal/llm
// only, touches no network and no file, and is therefore safe for any mode of
// the binary to call on every turn.
//
// The estimate is a guardrail rather than an accounting: it exists to leave
// room before the real limit of a model is hit, so it errs high where the
// ratio is uncertain.
package tokens
