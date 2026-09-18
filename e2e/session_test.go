//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestSessionFormat verifies the file a run stores: its header, the parent
// chain of its entries, the tool invocation and its result, and the metadata of
// the model responses.
func TestSessionFormat(t *testing.T) {
	app := newApp(t,
		harness.ToolCall("shell", map[string]any{"command": "echo rienda-e2e"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")
	result.RequireSuccess(t)

	session := app.Session(t, result.SessionID(t))
	require.Equal(t, 1, session.Header.Version)
	require.Equal(t, result.SessionID(t), session.Header.ID)
	require.Equal(t, "coder", session.Header.Agent)
	require.Equal(t, harness.FakeModelRef, session.Header.Model)
	require.Equal(t, app.Workdir(), session.Header.Workdir)
	require.False(t, session.Header.CreatedAt.IsZero())

	// The conversation holds the prompt, the answer with its tool invocation,
	// the result that travels back and the final answer.
	require.Equal(t, []string{"user", "assistant", "user", "assistant"}, roles(session))
	require.Len(t, session.Entries, 4)
	require.Equal(t, "run it", session.Entries[0].Text())
	require.Empty(t, session.Entries[0].ParentID)
	for index, entry := range session.Entries[1:] {
		require.Equal(t, session.Entries[index].ID, entry.ParentID)
	}

	// The first answer requests one tool invocation.
	first := session.Entries[1]
	require.Equal(t, []string{"tool_call"}, blockTypes(first))
	require.Equal(t, "tool_use", first.ResponseStopReason)
	require.Equal(t, harness.DefaultModelID, first.ResponseModel)
	require.Empty(t, first.ItemID)

	calls := session.ToolCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "shell", calls[0].ToolCallName)
	require.JSONEq(t, `{"command": "echo rienda-e2e"}`, string(calls[0].ToolCallArguments))

	// The result travels back to the model in a user turn of its own.
	results := session.ToolResults()
	require.Len(t, results, 1)
	require.Equal(t, calls[0].ToolCallID, results[0].ToolResultCallID)
	require.False(t, results[0].ToolResultIsError)
	require.Contains(t, results[0].ResultText(), "rienda-e2e")

	// The final answer closes the conversation with its accounting.
	last := session.Entries[3]
	require.Equal(t, "done", last.Text())
	require.Equal(t, "end_turn", last.ResponseStopReason)
	require.Equal(t, &harness.SessionUsage{
		InputTokens:  harness.DefaultInputTokens,
		OutputTokens: harness.DefaultOutputTokens,
	}, last.ResponseUsage)
}

// TestSessionOfEveryRun verifies that each invocation opens its own session,
// named by the identifier it reports.
func TestSessionOfEveryRun(t *testing.T) {
	app := newApp(t, harness.Text("first"), harness.Text("second"))

	first := app.Run(t, "run", "-a", "coder", "-p", "one")
	first.RequireSuccess(t)
	second := app.Run(t, "run", "-a", "coder", "-p", "two")
	second.RequireSuccess(t)

	require.NotEqual(t, first.SessionID(t), second.SessionID(t))
	require.Equal(t, "one", app.Session(t, first.SessionID(t)).Entries[0].Text())
	require.Equal(t, "two", app.Session(t, second.SessionID(t)).Entries[0].Text())
	require.Len(t, app.Sessions(t), 2)
}

// TestSessionWorkspaceIsolation verifies that sessions of different workspaces
// are stored apart, each one recording the workspace it belongs to.
func TestSessionWorkspaceIsolation(t *testing.T) {
	app := newApp(t, harness.Text("first"), harness.Text("second"))

	first := app.Run(t, "run", "-a", "coder", "-p", "here")
	first.RequireSuccess(t)

	other := t.TempDir()
	second := app.RunIn(t, other, "run", "-a", "coder", "-p", "there")
	second.RequireSuccess(t)

	sessions := app.Sessions(t)
	require.Len(t, sessions, 2)
	require.NotEqual(t, filepath.Dir(sessions[0].Path), filepath.Dir(sessions[1].Path))
	require.True(t, strings.HasPrefix(sessions[0].Path, app.Home()))

	require.Equal(t, app.Workdir(), app.Session(t, first.SessionID(t)).Header.Workdir)
	require.Equal(t, other, app.Session(t, second.SessionID(t)).Header.Workdir)
}
