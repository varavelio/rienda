//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// thinkingConfig returns the configuration of a thinking scenario: the default
// provider running a reasoning model whose modes are level-based, plus the
// session header the default config carries.
func thinkingConfig() *harness.Config {
	cfg := harness.DefaultConfig()
	cfg.Providers[0].Models[0].Thinking = []harness.Thinking{
		{Level: "low", MaxTokens: 4096},
		{Level: "high", MaxTokens: 8192},
	}
	return &cfg
}

// budgetThinkingConfig returns a configuration whose model runs a
// budget-based provider: the thinking mode carries only the budget, which the
// Anthropic protocol resolves to budget_tokens.
func budgetThinkingConfig() *harness.Config {
	cfg := harness.DefaultConfig()
	cfg.Providers[0].Protocol = harness.ProtocolAnthropic
	cfg.Providers[0].Models[0].Thinking = []harness.Thinking{{Level: "high", MaxTokens: 4096}}
	return &cfg
}

// TestThinkingReachesTheWireOnTheLevelProtocol verifies that a run that names
// a declared mode sends it as the reasoning effort of a chat completions
// request, and that a run without one sends nothing.
func TestThinkingReachesTheWireOnTheLevelProtocol(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("first"), harness.Text("second")},
		Agents: []harness.Agent{coderAgent()},
		Config: thinkingConfig(),
	})

	off := app.Run(t, "run", "-a", "coder", "-p", "hi")
	off.RequireSuccess(t)
	require.Empty(t, app.Provider().LastRequest(t).Chat(t).ReasoningEffort, "two scripted turns")

	mode := app.Run(t, "run", "-a", "coder", "--thinking", "high", "-p", "hi again")
	mode.RequireSuccess(t)
	require.Equal(t, "high", app.Provider().LastRequest(t).Chat(t).ReasoningEffort)
}

// TestThinkingReachesTheWireOnTheBudgetProtocol verifies the budget rendering
// on the Anthropic wire.
func TestThinkingReachesTheWireOnTheBudgetProtocol(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: budgetThinkingConfig(),
	})

	mode := app.Run(t, "run", "-a", "coder", "--thinking", "high", "-p", "hi")
	mode.RequireSuccess(t)

	anthropic := app.Provider().LastRequest(t).Anthropic(t)
	require.NotNil(t, anthropic.Thinking)
	require.Equal(t, 4096, anthropic.Thinking.BudgetTokens)
}
