// Package engine runs one agent over one session.
//
// Each turn resolves the agent and model from the session branch, builds the
// request from the stored history, streams the model response, runs the
// requested tools, and persists the results until the model answers without
// tool calls. The session file is the source of truth, so a run can stop and
// resume at any time.
//
// The engine keeps the conversation inside the model context window through
// the injected Compactor, retries transient provider failures, and reports
// progress through the event channel returned by Run.
package engine
