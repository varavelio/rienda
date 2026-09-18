//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// anthropicApp returns an instance whose only provider speaks the Anthropic
// Messages protocol against the fake provider.
func anthropicApp(t *testing.T, turns ...harness.Turn) *harness.Harness {
	t.Helper()
	return harness.New(t, harness.Options{
		Script: turns,
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{
			{
				Name:     harness.FakeProviderName,
				Protocol: harness.ProtocolAnthropic,
				APIKey:   harness.TestAPIKey,
				Models: []harness.Model{
					{Alias: harness.DefaultModelAlias, ID: harness.DefaultModelID},
				},
			},
		}},
	})
}

// TestAnthropicAnswersAPrompt verifies the request and the answer of a plain
// conversation over the Messages protocol.
func TestAnthropicAnswersAPrompt(t *testing.T) {
	app := anthropicApp(t, harness.Text("hello"))

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.Equal(t, "hello\n", result.Stdout)

	request := app.Provider().LastRequest(t)
	require.Equal(t, "/v1/messages", request.Path)
	require.Equal(t, harness.TestAPIKey, request.Header.Get("x-api-key"))
	require.Equal(t, "2023-06-01", request.Header.Get("anthropic-version"))
	require.Empty(t, request.Header.Get("Authorization"))

	anthropic := request.Anthropic(t)
	require.Equal(t, harness.DefaultModelID, anthropic.Model)
	require.Equal(t, 8192, anthropic.MaxTokens)
	require.Equal(t, "You answer briefly.", anthropic.System)
	require.True(t, anthropic.Stream)
	require.Equal(t, []string{"user"}, anthropic.Roles())
	require.Equal(t, "say hello", anthropic.Messages[0].Text())
	require.Equal(t, []string{"shell"}, anthropic.ToolNames())
}

// TestAnthropicExecutesTools verifies the tool loop of the protocol: the
// invocation travels as a tool use block and its result as a tool result block.
func TestAnthropicExecutesTools(t *testing.T) {
	app := anthropicApp(t,
		harness.ToolCall("shell", map[string]any{"command": "echo rienda-e2e"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Equal(t, "done\n", result.Stdout)
	require.Contains(t, result.Stderr, "rienda-e2e")

	requests := app.Provider().Requests()
	require.Len(t, requests, 2)

	second := requests[1].Anthropic(t)
	require.Equal(t, []string{"user", "assistant", "user"}, second.Roles())

	assistant := second.Messages[1]
	require.Equal(t, []string{"tool_use"}, assistant.BlockTypes())
	call := assistant.Blocks()[0]
	require.Equal(t, "shell", call.Name)
	require.NotEmpty(t, call.ID)
	require.JSONEq(t, `{"command": "echo rienda-e2e"}`, string(call.Input))

	answer := second.Messages[2]
	require.Equal(t, []string{"tool_result"}, answer.BlockTypes())
	resultBlock := answer.Blocks()[0]
	require.Equal(t, call.ID, resultBlock.ToolUseID)
	require.False(t, resultBlock.IsError)
	require.Contains(t, resultBlock.ContentText(), "rienda-e2e")
}

// TestAnthropicReportsToolFailures verifies that a failing command travels back
// as a failed tool result.
func TestAnthropicReportsToolFailures(t *testing.T) {
	app := anthropicApp(t,
		harness.ToolCall("shell", map[string]any{"command": "exit 3"}),
		harness.Text("recovered"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "fail")

	result.RequireSuccess(t)
	require.Equal(t, "recovered\n", result.Stdout)

	answer := app.Provider().Requests()[1].Anthropic(t).Messages[2]
	require.True(t, answer.Blocks()[0].IsError)
	require.Contains(t, answer.Blocks()[0].ContentText(), "command exited with code 3")
}

// TestAnthropicPersistsReasoning verifies that the thinking blocks of the
// protocol become reasoning the session stores.
func TestAnthropicPersistsReasoning(t *testing.T) {
	turn := harness.Text("the answer")
	turn.Thinking = "weighing the options"

	app := anthropicApp(t, turn)
	result := app.Run(t, "run", "-a", "coder", "-p", "think")

	result.RequireSuccess(t)
	require.Equal(t, "the answer\n", result.Stdout)

	session := app.Session(t, result.SessionID(t))
	require.Equal(t, []string{"thinking", "text"}, blockTypes(session.Entries[1]))
	require.Equal(t, "weighing the options", session.Entries[1].Blocks[0].Thinking)
}

// TestAnthropicAccountsUsage verifies that the token counts reported by the
// protocol reach the stored session, including the cache details.
func TestAnthropicAccountsUsage(t *testing.T) {
	turn := harness.Text("accounted")
	turn.Usage = &harness.Usage{InputTokens: 90, OutputTokens: 20, CacheReadTokens: 60}

	app := anthropicApp(t, turn)
	result := app.Run(t, "run", "-a", "coder", "-p", "count")

	result.RequireSuccess(t)
	require.Equal(t, &harness.SessionUsage{
		InputTokens:     90,
		OutputTokens:    20,
		CacheReadTokens: 60,
	}, app.Session(t, result.SessionID(t)).Entries[1].ResponseUsage)
}

// TestAnthropicAppliesTheThinkingBudget verifies that the thinking budget
// declared for a model reaches the Messages protocol and that the sampling
// temperature is dropped, since the API rejects both together.
func TestAnthropicAppliesTheThinkingBudget(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{
			{
				Name:     harness.FakeProviderName,
				Protocol: harness.ProtocolAnthropic,
				APIKey:   harness.TestAPIKey,
				Models: []harness.Model{{
					Alias:             harness.DefaultModelAlias,
					ID:                harness.DefaultModelID,
					Temperature:       new(0.4),
					ThinkingMaxTokens: 4096,
				}},
			},
		}},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "think hard")
	result.RequireSuccess(t)

	anthropic := app.Provider().LastRequest(t).Anthropic(t)
	require.NotNil(t, anthropic.Thinking)
	require.Equal(t, 4096, anthropic.Thinking.BudgetTokens)
	require.Nil(t, anthropic.Temperature)
}
