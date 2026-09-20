// Package engine runs one agent over one session.
//
// A run appends the user prompt to the session and then loops: it streams a
// model response, persists it, runs the tool calls the response requested,
// persists their results, and repeats until the model answers without tool
// calls, the caller cancels the context, or a failure aborts it.
//
// The session is the source of truth: every request is rebuilt from the stored
// history, so a run can stop and resume at any time. A run always leaves that
// history valid for every provider:
//
//   - A partial model response is never persisted, so a failed or canceled
//     stream leaves the history ending in a user message.
//   - Persisting a completed model response is not cancelable: the response is
//     already complete when cancellation arrives.
//   - Canceling while tools run persists the results of the calls that
//     finished together with interrupted placeholders for the rest, so no
//     tool call is ever left without its result.
//   - Tool arguments persist as a JSON object. Calls whose streamed arguments
//     are not a JSON object are not executed and receive an error result that
//     explains why.
//
// Streaming a model response is retried with exponential backoff while the
// failure is transient, so a dropped connection or an overloaded provider
// stays invisible. A transient failure that arrives after part of the response
// reached the caller is retried too: the retry reports Discard so the consumer
// drops the partial response before the next attempt streams it again, and the
// restarted answer is never shown twice. Only a failure that repeating cannot
// overcome, or the exhaustion of the attempts, ends the run.
//
// Consumers follow a run through the channel returned by Run, which always
// closes after a run_end event.
package engine
