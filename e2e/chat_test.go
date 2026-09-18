//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestChatPersistsReasoning verifies that the reasoning of the model becomes a
// thinking block of the assistant message and never reaches standard output.
func TestChatPersistsReasoning(t *testing.T) {
	turn := harness.Text("the answer")
	turn.Thinking = "let me think about it"

	app := newApp(t, turn)
	result := app.Run(t, "run", "-a", "coder", "-p", "think")

	result.RequireSuccess(t)
	require.Equal(t, "the answer\n", result.Stdout)
	require.NotContains(t, result.Stdout, "let me think")
	require.NotContains(t, result.Stderr, "let me think")

	session := app.Session(t, result.SessionID(t))
	blocks := session.Entries[1].Blocks
	require.Equal(t, []string{"thinking", "text"}, blockTypes(session.Entries[1]))
	require.Equal(t, "let me think about it", blocks[0].Thinking)
	require.Equal(t, "the answer", blocks[1].Text)
}

// TestChatAccountsUsage verifies that every token count the provider reports is
// stored with the assistant message.
func TestChatAccountsUsage(t *testing.T) {
	turn := harness.Text("accounted")
	turn.Usage = &harness.Usage{
		InputTokens:     120,
		OutputTokens:    30,
		ReasoningTokens: 12,
		CacheReadTokens: 80,
	}

	app := newApp(t, turn)
	result := app.Run(t, "run", "-a", "coder", "-p", "count")

	result.RequireSuccess(t)
	require.Equal(t, &harness.SessionUsage{
		InputTokens:     120,
		OutputTokens:    30,
		ReasoningTokens: 12,
		CacheReadTokens: 80,
	}, app.Session(t, result.SessionID(t)).Entries[1].ResponseUsage)
}

// TestChatWithoutUsageStoresNone verifies that a provider reporting no usage
// leaves the assistant message without accounting.
func TestChatWithoutUsageStoresNone(t *testing.T) {
	app := newApp(t, harness.Turn{Text: "plain"})

	result := app.Run(t, "run", "-a", "coder", "-p", "count")

	result.RequireSuccess(t)
	require.Nil(t, app.Session(t, result.SessionID(t)).Entries[1].ResponseUsage)
}

// TestChatAssemblesStreamedArguments verifies that tool arguments arriving in
// fragments run as the complete payload.
func TestChatAssemblesStreamedArguments(t *testing.T) {
	turn := harness.ToolCall("shell", map[string]any{"command": "printf 'assembled'"})
	turn.Chunks = 8

	app := newApp(t, turn, harness.Text("done"))
	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, "assembled")

	calls := app.Session(t, result.SessionID(t)).ToolCalls()
	require.Len(t, calls, 1)
	require.JSONEq(t, `{"command": "printf 'assembled'"}`, string(calls[0].ToolCallArguments))
}

// TestChatRunsEveryToolCallOfATurn verifies that a turn requesting several
// invocations runs all of them in order and answers them together.
func TestChatRunsEveryToolCallOfATurn(t *testing.T) {
	app := newApp(t,
		harness.ToolCalls(
			harness.Call{Name: "shell", Arguments: map[string]any{"command": "echo first"}},
			harness.Call{Name: "shell", Arguments: map[string]any{"command": "echo second"}},
		),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run both")

	result.RequireSuccess(t)
	require.Equal(t, "done\n", result.Stdout)
	require.Less(t, strings.Index(result.Stderr, "first"), strings.Index(result.Stderr, "second"))

	session := app.Session(t, result.SessionID(t))
	require.Len(t, session.ToolCalls(), 2)
	require.Len(t, session.ToolResults(), 2)
	require.Equal(t, []string{"user", "assistant", "user", "assistant"}, roles(session))
	require.Len(t, session.Entries[2].Blocks, 2)

	// The second request answers both invocations in one user turn, one tool
	// message each, as the protocol requires.
	chat := app.Provider().Requests()[1].Chat(t)
	require.Equal(t, []string{"system", "user", "assistant", "tool", "tool"}, chat.Roles())
	require.Len(t, chat.Messages[2].ToolCalls, 2)
	require.Equal(t, chat.Messages[2].ToolCalls[0].ID, chat.Messages[3].ToolCallID)
	require.Equal(t, chat.Messages[2].ToolCalls[1].ID, chat.Messages[4].ToolCallID)
}
