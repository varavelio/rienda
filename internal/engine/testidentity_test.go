package engine

import (
	"context"

	"github.com/varavelio/rienda/internal/agent"
)

// agentRunOf reads the run identity attached to a hook context.
func agentRunOf(ctx context.Context) (agent.Run, bool) {
	return agent.RunFromContext(ctx)
}
