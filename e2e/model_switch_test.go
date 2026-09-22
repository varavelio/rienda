//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// secondModelRef is the reference of the second model the scenario switches
// to, added to the default configuration of the instance.
const secondModelRef = harness.FakeProviderName + "/second"

// modelSwitchConfig returns the default configuration with a second model, so a
// run can switch the model of a session.
func modelSwitchConfig() *harness.Config {
	cfg := harness.DefaultConfig()
	cfg.Providers[0].Models = append(cfg.Providers[0].Models, harness.Model{
		Alias: "second",
		ID:    "gpt-second",
	})
	return &cfg
}

// TestModelSwitchDrivesTheWholeFeature verifies changing the model of a
// conversation end to end: a stored session continues on another model, the
// selection lands in the session file bound to the branch that wrote it, the
// request that follows carries the wire identifier of the new model, and the
// header records the model the session was created with.
func TestModelSwitchDrivesTheWholeFeature(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{
			harness.Text("first"),
			harness.Text("second"),
			harness.Text("third"),
		},
		Agents: []harness.Agent{coderAgent()},
		Config: modelSwitchConfig(),
	})

	first := app.Run(t, "run", "-a", "coder", "-p", "first prompt")
	first.RequireSuccess(t)
	sessionID := first.SessionID(t)

	stored := app.Session(t, sessionID)
	require.Equal(t, harness.FakeModelRef, stored.Header.Model)
	require.Empty(t, stored.Models)
	require.Equal(t, harness.DefaultModelID, app.Provider().Requests()[0].Chat(t).Model)

	// Continuing the session on another model appends the selection and sends
	// the wire identifier of the new model.
	second := app.Run(t, "run", "-s", sessionID, "-m", secondModelRef, "-p", "second prompt")
	second.RequireSuccess(t)

	stored = app.Session(t, sessionID)
	require.Equal(t, harness.FakeModelRef, stored.Header.Model,
		"the header keeps the model the session was created with")
	require.Len(t, stored.Models, 1)
	selection := stored.Models[0]
	require.Equal(t, secondModelRef, selection.ModelRef)
	require.NotEmpty(t, selection.ID)
	require.Contains(t, entryIDs(stored), selection.ParentID,
		"the selection hangs from a stored entry")

	requests := app.Provider().Requests()
	require.Len(t, requests, 2)
	require.Equal(t, "gpt-second", requests[1].Chat(t).Model)
	require.Contains(t, joinedMessages(requests[1].Chat(t)), "first prompt",
		"the conversation the session holds is replayed")

	// The selection is bound to the branch: continuing the session without a
	// model keeps running the one the branch selected, and only one selection
	// is written.
	third := app.Run(t, "run", "-s", sessionID, "-p", "keep going")
	third.RequireSuccess(t)
	require.Len(t, app.Session(t, sessionID).Models, 1, "no second selection is written")
	require.Equal(t, "gpt-second", app.Provider().Requests()[2].Chat(t).Model)
}

// TestModelSwitchRejectsAnUnknownModel verifies that a model the configuration
// does not hold fails the run before the session is touched, so nothing is
// written and the conversation stays exactly as it was.
func TestModelSwitchRejectsAnUnknownModel(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("first")},
		Agents: []harness.Agent{coderAgent()},
		Config: modelSwitchConfig(),
	})

	first := app.Run(t, "run", "-a", "coder", "-p", "first prompt")
	first.RequireSuccess(t)
	sessionID := first.SessionID(t)

	before := app.Session(t, sessionID)
	blocked := app.Run(t, "run", "-s", sessionID, "-m", "fake/ghost", "-p", "second prompt")

	require.Equal(t, 1, blocked.Code)
	require.Contains(t, blocked.Stderr, `model "fake/ghost" is not declared`)
	require.Len(t, app.Provider().Requests(), 1, "nothing reached the provider")
	require.Equal(t, len(before.Entries), len(app.Session(t, sessionID).Entries),
		"nothing was written to the session")
	require.Empty(t, app.Session(t, sessionID).Models)
}
