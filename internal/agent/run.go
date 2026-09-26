package agent

import (
	"context"
)

// Run identifies the run an invocation belongs to: the session that owns it,
// the provider model identifier the provider receives and the definition of
// the agent the turn runs.
type Run struct {
	// SessionID is the session that owns the run.
	SessionID string

	// ModelID is the provider model identifier sent on the wire.
	ModelID string

	// Agent is the definition the turn runs.
	Agent Agent
}

// runKey is the context key carrying the run identity of an invocation.
type runKey struct{}

// WithRun attaches run to ctx.
func WithRun(ctx context.Context, run Run) context.Context {
	return context.WithValue(ctx, runKey{}, run)
}

// RunFromContext returns the run identity attached to ctx, if any.
func RunFromContext(ctx context.Context) (Run, bool) {
	run, ok := ctx.Value(runKey{}).(Run)
	if !ok {
		return Run{}, false
	}
	return run, true
}
