// Package engine runs one agent over one session.
//
// A run appends the user prompt to the session and then loops: it streams a
// model response, persists it, runs the tool calls the response requested,
// persists their results, and repeats until the model answers without tool
// calls, a turn limit stops it, the caller cancels the context, or a failure
// aborts it.
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
// Consumers follow a run through the channel returned by Run, which always
// closes after a run_end event.
package engine
