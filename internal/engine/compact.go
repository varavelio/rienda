package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
	"github.com/varavelio/rienda/internal/tool"
)

// errRunInFlight reports a manual compaction asked for while another run or
// compaction is in flight. A store is not safe for concurrent use, so the
// engine refuses instead of racing.
var errRunInFlight = errors.New("engine: a run is already in flight")

// Compactor summarizes the branch of a session into a checkpoint. The harness
// provides the production implementation over internal/compaction; the engine
// tests inject a no-op or a scripted one.
type Compactor interface {
	// Refusal reports why the branch holds nothing to compact, and false when
	// it holds something.
	Refusal(branch []session.Entry) (compaction.Refusal, bool)

	// Compact summarizes the branch. ok is false when there is nothing to
	// compact. modelRef is the provider/model reference the branch runs, which
	// the compactor may use to summarize with the model of the conversation
	// rather than with one of its own.
	Compact(
		ctx context.Context,
		branch []session.Entry,
		modelRef string,
	) (compaction.Result, bool, error)
}

// Compaction configures when the engine compacts automatically.
type Compaction struct {
	// Enabled switches the automatic compaction on and off. The manual one is
	// always available, because the user asked for it.
	Enabled bool

	// ReserveTokens is the room the threshold leaves in the window for the
	// answer, so the compaction arrives before the request would exceed it.
	ReserveTokens int
}

// CompactRefusal reports why the active branch holds nothing to compact, and
// false when it holds something. The manual command is offered only when it
// does, so the reason is what explains a command the interface cannot run.
func (e *Engine) CompactRefusal() (compaction.Refusal, bool) {
	if e.compactor == nil {
		return compaction.Refusal{Kind: compaction.RefusalEmpty}, true
	}
	return e.compactor.Refusal(e.store.Branch())
}

// CanCompact reports whether the active branch still holds something to
// summarize. It derives from CompactRefusal, so the readiness and the reason a
// front end explains can never disagree.
func (e *Engine) CanCompact() bool {
	_, refused := e.CompactRefusal()
	return !refused
}

// Compact summarizes the active branch on demand and returns the channel
// carrying its events. It ignores the threshold, because the user asked for
// it, and it uses the same code path, the same prompt and the same events as
// an automatic compaction.
//
// The channel closes with an EventRunEnd, or with an EventError when the
// summarization fails or when another run is in flight. As with Run, the
// caller must keep receiving from the channel until it closes.
func (e *Engine) Compact(ctx context.Context) <-chan Event {
	events := make(chan Event, eventBuffer)
	go func() {
		defer close(events)

		if !e.enter() {
			fail(events, errRunInFlight)
			return
		}
		defer e.leave()

		runCtx := ctx
		if e.workdir != "" {
			runCtx = tool.WithWorkdir(runCtx, e.workdir)
		}
		if err := e.compactBranch(runCtx, events); err != nil {
			fail(events, err)
			return
		}
		emit(events, Event{Type: EventRunEnd, Reason: EndReasonTurn})
	}()
	return events
}

// compactBranch summarizes the active branch, persists the checkpoint and
// emits the compaction events. It is the single code path of the automatic and
// the manual compaction.
//
// A failure is returned so the caller ends the run without writing an entry,
// because a half-built checkpoint is worse than a failed run. A compaction
// that fails emits no end event at all.
func (e *Engine) compactBranch(ctx context.Context, events chan<- Event) error {
	if e.compactor == nil {
		return nil
	}

	// The session identifier travels with the summarization exactly like with
	// an ordinary turn, so a provider that routes a call by session accepts the
	// summary the same way it accepts the conversation. Attaching it here, in
	// the single path of the automatic and the manual compaction, is what keeps
	// both in agreement.
	ctx = llm.WithSessionID(ctx, e.store.ID())

	branch := e.store.Branch()
	if _, refused := e.compactor.Refusal(branch); refused {
		return nil
	}

	emit(events, Event{Type: EventCompactionStart})
	result, ok, err := e.compactor.Compact(ctx, branch, e.store.ActiveModel())
	if err != nil {
		return fmt.Errorf("engine: compact branch: %w", err)
	}
	if !ok {
		// Defensive: Refusal and Compact answer from the same preparation, so
		// the two agree whenever the compactor is the real one.
		return nil
	}

	// Persisting a complete checkpoint is not cancelable: the summary is
	// already generated, so the work must survive an interrupt that arrives
	// right now.
	entry, err := e.store.AppendCompaction(
		context.WithoutCancel(ctx),
		result.Summary,
		result.KeptID,
		result.TokensBefore,
		result.Model,
		result.Usage,
	)
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}

	emit(events, Event{
		Type: EventCompactionEnd,
		Compaction: &CompactionInfo{
			Entry:        entry,
			TokensBefore: result.TokensBefore,
			TokensAfter:  e.contextTokens(),
			Usage:        usageFrom(result.Usage),
		},
	})
	return nil
}

// shouldCompact reports whether the request the turn is about to send crosses
// the compaction threshold: the estimate must exceed the window less the room
// reserved for the answer. The system prompt and the tool definitions are part
// of what is measured, because the model reads them too.
//
// A branch that already ends in a compaction is never compacted again: the
// checkpoint is the newest thing the branch holds, so there is nothing left to
// summarize.
func (e *Engine) shouldCompact(plan turnPlan) bool {
	if e.compactor == nil || !e.compaction.Enabled || plan.model.ContextWindow <= 0 {
		return false
	}
	if branch := e.store.Branch(); len(branch) > 0 &&
		branch[len(branch)-1].Kind == session.KindCompaction {
		return false
	}
	return tokens.OfRequest(plan.request) >
		plan.model.ContextWindow-e.compaction.ReserveTokens
}

// contextTokens returns the estimated size of the request the next turn would
// send, or zero when the estimate cannot be built. It is a best-effort figure
// reported by the compaction event, never a reason to fail a run.
func (e *Engine) contextTokens() int {
	report, err := e.Context()
	if err != nil {
		return 0
	}
	return report.Used
}
