//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// responsesProvider returns a declaration of a provider that speaks the
// Responses protocol against the fake provider.
func responsesProvider() harness.Provider {
	return harness.Provider{
		Name:     harness.FakeProviderName,
		Protocol: harness.ProtocolResponses,
		APIKey:   harness.TestAPIKey,
		Models:   []harness.Model{{Alias: harness.DefaultModelAlias, ID: harness.DefaultModelID}},
	}
}

// responsesApp returns an instance whose only provider speaks the Responses
// protocol against the fake provider.
func responsesApp(t *testing.T, turns ...harness.Turn) *harness.Harness {
	t.Helper()
	return harness.New(t, harness.Options{
		Script: turns,
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{responsesProvider()}},
	})
}

// TestResponsesAnswersAPrompt verifies the request and the answer of a plain
// conversation over the Responses protocol.
func TestResponsesAnswersAPrompt(t *testing.T) {
	app := responsesApp(t, harness.Text("hello"))

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.Equal(t, "hello\n", result.Stdout)

	request := app.Provider().LastRequest(t)
	require.Equal(t, "/v1/responses", request.Path)
	require.Equal(t, "Bearer "+harness.TestAPIKey, request.Header.Get("Authorization"))

	responses := request.Responses(t)
	require.Equal(t, harness.DefaultModelID, responses.Model)
	require.Equal(t, "You answer briefly.", responses.Instructions)
	require.True(t, responses.Stream)
	require.NotNil(t, responses.Store)
	require.False(t, *responses.Store)
	require.Equal(t, []string{"message"}, responses.InputTypes())
	require.Equal(t, "user", responses.Input[0].Role)
	require.Equal(t, "say hello", responses.Input[0].Text())
	require.Equal(t, []string{"shell"}, responses.ToolNames())
}

// TestResponsesUsesThePresetProtocol verifies that a provider declaring the
// OpenAI preset speaks the Responses protocol.
func TestResponsesUsesThePresetProtocol(t *testing.T) {
	provider := responsesProvider()
	provider.Preset = "openai"
	provider.Protocol = ""

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{provider}},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.Equal(t, "/v1/responses", app.Provider().LastRequest(t).Path)
}

// TestResponsesExecutesTools verifies the tool loop of the protocol: the
// conversation replays as typed input items and the tool result travels back as
// a function call output.
func TestResponsesExecutesTools(t *testing.T) {
	app := responsesApp(t,
		harness.ToolCall("shell", map[string]any{"command": "echo rienda-e2e"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Equal(t, "done\n", result.Stdout)
	require.Contains(t, result.Stderr, "rienda-e2e")

	requests := app.Provider().Requests()
	require.Len(t, requests, 2)

	second := requests[1].Responses(t)
	require.Equal(
		t,
		[]string{"message", "function_call", "function_call_output"},
		second.InputTypes(),
	)

	call := second.Input[1]
	require.Equal(t, "shell", call.Name)
	require.JSONEq(t, `{"command": "echo rienda-e2e"}`, call.Arguments)

	output := second.Input[2]
	require.Equal(t, call.CallID, output.CallID)
	require.Contains(t, output.Output, "rienda-e2e")
}

// TestResponsesPersistsItemIdentifiers verifies that the identifiers of the
// items a response produced are stored and replayed, so a stateless
// conversation keeps the references the provider requires.
func TestResponsesPersistsItemIdentifiers(t *testing.T) {
	first := harness.ToolCall("shell", map[string]any{"command": "echo replayed"})
	first.Text = "first answer"
	first.Thinking = "checking the marker"

	app := responsesApp(t, first, harness.Text("done"))
	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)

	// The stored answer keeps the identifiers of its reasoning and its text.
	session := app.Session(t, result.SessionID(t))
	firstAnswer := session.Entries[1]
	require.Equal(t, "msg_1", firstAnswer.ItemID)
	require.Equal(t, []string{"thinking", "text", "tool_call"}, blockTypes(firstAnswer))
	require.Equal(t, "rs_1", firstAnswer.Blocks[0].ThinkingID)
	require.Equal(t, "encrypted_1", firstAnswer.Blocks[0].ThinkingSignature)
	require.Equal(t, "first answer", firstAnswer.Blocks[1].Text)

	// The next request replays them as typed items.
	second := app.Provider().Requests()[1].Responses(t)
	require.Equal(
		t,
		[]string{"message", "reasoning", "message", "function_call", "function_call_output"},
		second.InputTypes(),
	)

	reasoning := second.Input[1]
	require.Equal(t, "rs_1", reasoning.ID)
	require.Equal(t, "encrypted_1", reasoning.EncryptedContent)
	require.Equal(t, "checking the marker", reasoning.SummaryText())

	answer := second.Input[2]
	require.Equal(t, "msg_1", answer.ID)
	require.Equal(t, "assistant", answer.Role)
	require.Equal(t, "first answer", answer.Text())

	require.Equal(t, "shell", second.Input[3].Name)
}

// TestResponsesAccountsUsage verifies that the usage of the completed response
// reaches the stored session.
func TestResponsesAccountsUsage(t *testing.T) {
	turn := harness.Text("accounted")
	turn.Usage = &harness.Usage{InputTokens: 70, OutputTokens: 15}

	app := responsesApp(t, turn)
	result := app.Run(t, "run", "-a", "coder", "-p", "count")

	result.RequireSuccess(t)
	require.Equal(t, &harness.SessionUsage{
		InputTokens:  70,
		OutputTokens: 15,
	}, app.Session(t, result.SessionID(t)).Entries[1].ResponseUsage)
}
