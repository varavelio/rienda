// Package engine runs one agent over one session.
//
// Neither the agent nor the model is fixed for the life of the engine. The
// engine holds the agent definitions it was given, and it resolves both the
// agent and the model a branch runs on every turn from the session through the
// injected Resolver. A conversation can therefore switch agent or model without
// a new engine and without rewriting its file: every turn is planned from the
// branch it runs, and the plan carries the agent, the model, the client and the
// tools of the turn together, so they can never disagree with each other.
//
// The tools of the turn come from the resolved agent, and the request is built
// with the generation settings and the context window of the resolved model, so
// a session that switched sends the tools of the agent that runs the turn and
// measures its context against the window of the model that runs it. A branch
// that names an agent or a model the harness cannot run fails before anything
// is written, so a session whose agent or model is gone keeps its conversation
// intact and runs again as soon as a selection, or the header, names one that
// exists.
//
// A run appends the user prompt to the session and then loops: it streams a
// model response, persists it, runs the tool calls the response requested,
// persists their results, and repeats until the model answers without tool
// calls, the caller cancels the context, or a failure aborts it.
//
// The session is the source of truth: every request is rebuilt from the stored
// history, so a run can stop and resume at any time. The system prompt is
// rebuilt on every turn too: the instructions of the project the session runs
// in, read from the first project instruction file that exists in the working
// directory (AGENTS.md, agents.md, AGENTS.MD, CLAUDE.md, claude.md or
// CLAUDE.MD, in that order), are appended to the system prompt of the agent
// and sent current, so an edit to that file applies to the next request even
// when earlier turns sent different content.
//
// A run always leaves that history valid for every provider:
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
// The engine keeps the conversation inside the context window of its model: at
// the top of every loop iteration, before the request of the turn is built, it
// measures the request against the window and asks the injected Compactor to
// summarize the oldest turns when the estimate crosses the configured
// threshold. At most one automatic compaction happens per run, a branch that
// already ends in a compaction is skipped, and a compaction that fails ends the
// run without writing an entry. The same procedure can be driven on demand
// through Compact, which ignores the threshold because the user asked for it.
//
// Consumers follow a run through the channel returned by Run, which always
// closes after a run_end event.
package engine
