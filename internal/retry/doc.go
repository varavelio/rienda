// Package retry repeats provider calls that fail with a transient error.
//
// Rienda applies one fixed retry policy to every model call, whichever the
// mode of the program: five attempts with exponential backoff and full jitter,
// capped at eight seconds between waits. Only the failures a repeated call may
// overcome are retried (see IsTransient); the ones the provider attributes to
// the request, the credentials or the account are returned at once.
//
// The policy is a sane default, not a setting: no caller configures it, so
// every mode behaves the same way.
//
// A caller whose failure already delivered output it cannot take back wraps
// the error with Permanent before returning it, because repeating the call
// would repeat what the user already saw. A caller that can retract what it
// delivered, as the engine does with a streamed response, retries instead and
// reports the retraction to its own consumer.
//
// A producer whose failure IsTransient does not recognize on its own, such as
// an answer that streamed no content, wraps it with Transient so the call is
// repeated. The providers already mark a stream they cut before the response
// completed with llm.ErrIncompleteStream, which IsTransient recognizes.
package retry
