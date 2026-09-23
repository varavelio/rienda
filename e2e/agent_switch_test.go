//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// plannerAgent returns an agent definition with its own body and no tools, so
// a run that switched agent is recognizable by the system prompt it sends.
func plannerAgent() harness.Agent {
	return harness.Agent{
		ID:           "planner",
		Description:  "Plans the work",
		Model:        harness.FakeModelRef,
		SystemPrompt: "You plan the work.",
	}
}

// TestAgentSwitchDrivesTheWholeFeature verifies changing the agent of a
// conversation end to end: a stored session continues on another agent, the
// selection lands in the session file bound to the branch that wrote it, the
// request that follows carries the definition of the new agent, and a branch
// that returns to a turn before the selection runs on the agent that was in
// effect there.
func TestAgentSwitchDrivesTheWholeFeature(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{
			harness.Text("planned"),
			harness.Text("implemented"),
			harness.Text("branched"),
		},
		Agents: []harness.Agent{coderAgent(), plannerAgent()},
	})

	first := app.Run(t, "run", "-a", "coder", "-p", "plan it")
	first.RequireSuccess(t)
	sessionID := first.SessionID(t)

	// The header still names the agent the session was created with, and the
	// first request carries its definition.
	stored := app.Session(t, sessionID)
	require.Equal(t, "coder", stored.Header.Agent)
	require.Empty(t, stored.Agents)
	require.Len(t, app.Provider().Requests(), 1)
	require.Equal(t, "You answer briefly.", app.Provider().Requests()[0].Chat(t).Messages[0].Text())

	// Continuing the session on another agent appends the selection and sends
	// the definition of the new agent.
	second := app.Run(t, "run", "-s", sessionID, "-a", "planner", "-p", "implement it")
	second.RequireSuccess(t)

	stored = app.Session(t, sessionID)
	require.Equal(t, "coder", stored.Header.Agent, "the header keeps the owner it was created with")
	require.Len(t, stored.Agents, 1)
	selection := stored.Agents[0]
	require.Equal(t, "planner", selection.AgentID)
	require.Equal(t, "coder", selection.PreviousAgentID,
		"the selection records the agent it replaced, so the transition reads off the file")
	require.NotEmpty(t, selection.ID)
	require.Contains(
		t,
		entryIDs(stored),
		selection.ParentID,
		"the selection hangs from a stored entry",
	)

	requests := app.Provider().Requests()
	require.Len(t, requests, 2)
	require.Equal(t, "You plan the work.", requests[1].Chat(t).Messages[0].Text())
	require.NotContains(t, requests[1].Chat(t).Messages[0].Text(), "answer briefly")
	require.Contains(t, joinedMessages(requests[1].Chat(t)), "plan it",
		"the conversation the session holds is replayed")

	// The selection is bound to the branch: continuing the session without an
	// agent keeps running the one the branch selected.
	third := app.Run(t, "run", "-s", sessionID, "-p", "keep going")
	third.RequireSuccess(t)
	require.Len(t, app.Session(t, sessionID).Agents, 1, "no second selection is written")
	require.Equal(t, "You plan the work.",
		app.Provider().Requests()[2].Chat(t).Messages[0].Text())
}

// TestAgentSwitchLeavesAnUnknownAgentRunnable verifies that a session whose
// agent selection names a definition that is gone keeps its conversation and
// refuses to run, and that selecting an agent that exists makes it run again.
func TestAgentSwitchLeavesAnUnknownAgentRunnable(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{
			harness.Text("planned"),
			harness.Text("recovered"),
		},
		Agents: []harness.Agent{coderAgent(), plannerAgent()},
	})

	first := app.Run(t, "run", "-a", "coder", "-p", "plan it")
	first.RequireSuccess(t)
	sessionID := first.SessionID(t)

	// The definition the session selects disappears from the agent directory.
	require.NoError(t, os.Remove(filepath.Join(app.AgentsDir(), plannerAgent().FileName())))

	blocked := app.Run(t, "run", "-s", sessionID, "-a", "planner", "-p", "implement it")

	require.Equal(t, 1, blocked.Code)
	require.Contains(t, blocked.Stderr, "not defined in the agent directory")
	require.Len(t, app.Provider().Requests(), 1, "nothing reached the provider")
	require.Empty(t, app.Session(t, sessionID).Agents, "nothing was written")

	// Selecting the agent that still exists takes the conversation forward on
	// top of the turns it already holds.
	recovered := app.Run(t, "run", "-s", sessionID, "-a", "coder", "-p", "implement it")
	recovered.RequireSuccess(t)
	require.Contains(t, recovered.Stdout, "recovered")
	require.Contains(t, joinedMessages(app.Provider().Requests()[1].Chat(t)), "plan it")
}

// entryIDs returns the identifiers of every entry of a session, so a test can
// assert that a selection hangs from a stored entry.
func entryIDs(stored harness.Session) []string {
	ids := make([]string, 0, len(stored.Entries))
	for _, entry := range stored.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}
