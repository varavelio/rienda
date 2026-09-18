//go:build e2e

package e2e

import (
	"testing"

	"github.com/varavelio/rienda/e2e/harness"
)

// coderAgent returns the agent definition most tests run: it declares the
// built-in shell tool and runs the default model of the instance.
func coderAgent() harness.Agent {
	return harness.Agent{
		ID:           "coder",
		Description:  "A test agent",
		Model:        harness.FakeModelRef,
		Tools:        []string{"shell"},
		SystemPrompt: "You answer briefly.",
	}
}

// modelFor returns the coder agent definition running the given model
// reference.
func modelFor(ref string) harness.Agent {
	definition := coderAgent()
	definition.Model = ref
	return definition
}

// roles returns the roles of the entries of a session, in order.
func roles(session harness.Session) []string {
	names := make([]string, 0, len(session.Entries))
	for _, entry := range session.Entries {
		names = append(names, entry.Role)
	}
	return names
}

// blockTypes returns the types of the blocks of an entry, in order.
func blockTypes(entry harness.SessionEntry) []string {
	types := make([]string, 0, len(entry.Blocks))
	for _, block := range entry.Blocks {
		types = append(types, block.Type)
	}
	return types
}

// newApp returns an instance whose fake provider serves the given turns and
// that declares the coder agent.
func newApp(t *testing.T, turns ...harness.Turn) *harness.Harness {
	t.Helper()
	return harness.New(t, harness.Options{
		Script: turns,
		Agents: []harness.Agent{coderAgent()},
	})
}
