//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestRunAnswersAPrompt verifies the plain conversation: the answer reaches
// standard output, the session identifier reaches standard error, and the
// request carries the system prompt and the user message.
func TestRunAnswersAPrompt(t *testing.T) {
	app := newApp(t, harness.Text("hello"))

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.Equal(t, "hello\n", result.Stdout)
	require.Contains(t, result.Stderr, "session: ")
	require.NotEmpty(t, result.SessionID(t))

	request := app.Provider().LastRequest(t)
	require.Equal(t, http.MethodPost, request.Method)
	require.Equal(t, "/v1/chat/completions", request.Path)
	require.Equal(t, "application/json", request.Header.Get("Content-Type"))
	require.Equal(t, "text/event-stream", request.Header.Get("Accept"))
	require.Equal(t, "Bearer "+harness.TestAPIKey, request.Header.Get("Authorization"))

	chat := request.Chat(t)
	require.Equal(t, harness.DefaultModelID, chat.Model)
	require.True(t, chat.Stream)
	require.NotNil(t, chat.StreamOptions)
	require.True(t, chat.StreamOptions.IncludeUsage)
	require.Equal(t, []string{"system", "user"}, chat.Roles())
	require.Equal(t, "You answer briefly.", chat.Messages[0].Text())
	require.Equal(t, "say hello", chat.Messages[1].Text())
}

// TestRunStreamsTheAnswer verifies that a fragmented answer arrives complete
// and in order, exactly once.
func TestRunStreamsTheAnswer(t *testing.T) {
	answer := "first line\nsecond line, plus a tail long enough to arrive in many fragments"
	turn := harness.Text(answer)
	turn.Chunks = 40

	app := newApp(t, turn)
	result := app.Run(t, "run", "-a", "coder", "-p", "talk to me")

	result.RequireSuccess(t)
	require.Equal(t, answer+"\n", result.Stdout)
}

// TestRunExecutesTools verifies the tool loop: the invocation reaches standard
// error together with its output, its result travels back to the model, and
// the answer of the second turn is the one printed.
func TestRunExecutesTools(t *testing.T) {
	app := newApp(t,
		harness.ToolCall("shell", map[string]any{"command": "echo rienda-e2e"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Equal(t, "done\n", result.Stdout)
	require.Contains(t, result.Stderr, "tool: shell")
	require.Contains(t, result.Stderr, "rienda-e2e")

	requests := app.Provider().Requests()
	require.Len(t, requests, 2)

	chat := requests[1].Chat(t)
	require.Equal(t, []string{"system", "user", "assistant", "tool"}, chat.Roles())
	require.Empty(t, chat.Messages[2].Text())
	require.Len(t, chat.Messages[2].ToolCalls, 1)

	call := chat.Messages[2].ToolCalls[0]
	require.Equal(t, "function", call.Type)
	require.Equal(t, "shell", call.Function.Name)
	require.JSONEq(t, `{"command": "echo rienda-e2e"}`, call.Function.Arguments)

	toolMessage := chat.Messages[3]
	require.Equal(t, call.ID, toolMessage.ToolCallID)
	require.Contains(t, toolMessage.Text(), "rienda-e2e")
}

// TestRunReportsToolFailures verifies that a failing command is reported on
// standard error, travels back to the model as a failed result, and does not
// fail the run.
func TestRunReportsToolFailures(t *testing.T) {
	app := newApp(t,
		harness.ToolCall("shell", map[string]any{"command": "exit 3"}),
		harness.Text("recovered"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "fail")

	result.RequireSuccess(t)
	require.Equal(t, "recovered\n", result.Stdout)
	require.Contains(t, result.Stderr, "tool failed")
	require.Contains(t, result.Stderr, "command exited with code 3")

	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "command exited with code 3")
}

// TestRunRejectsUnknownTools verifies that a model calling a tool the agent
// does not declare receives an error result instead of reaching the host.
func TestRunRejectsUnknownTools(t *testing.T) {
	app := newApp(t,
		harness.ToolCall("ghost", map[string]any{}),
		harness.Text("understood"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "call the ghost")

	result.RequireSuccess(t)
	require.Equal(t, "understood\n", result.Stdout)
	require.Contains(t, result.Stderr, "unknown tool")
	require.Contains(t, result.Stderr, "ghost")

	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "unknown tool")
}

// TestRunRejectsMalformedToolArguments verifies that arguments which are not a
// JSON object are rejected without reaching the tool.
func TestRunRejectsMalformedToolArguments(t *testing.T) {
	app := newApp(t,
		harness.ToolCalls(harness.Call{
			Name:         "shell",
			RawArguments: `"not an object"`,
		}),
		harness.Text("understood"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "break it")

	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, "tool failed")
	require.Contains(t, result.Stderr, "the tool was not executed")

	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "the tool was not executed")
}

// TestRunRunsToolInTheRequestedWorkdir verifies that the workspace of the
// invocation, absolute or relative, is the working directory of the tools it
// runs and the workspace the session records.
func TestRunRunsToolInTheRequestedWorkdir(t *testing.T) {
	marker := harness.ToolCall("shell", map[string]any{"command": "cat marker.txt"})

	t.Run("with an absolute workspace", func(t *testing.T) {
		app := newApp(t, marker, harness.Text("read"))
		dir := filepath.Join(app.Workdir(), "sub")
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("from-sub"), 0o600),
		)

		result := app.Run(t, "run", "-a", "coder", "-p", "read the marker", "-C", dir)

		result.RequireSuccess(t)
		require.Contains(t, result.Stderr, "from-sub")
		require.Equal(t, dir, app.Session(t, result.SessionID(t)).Header.Workdir)
	})

	t.Run("with a relative workspace", func(t *testing.T) {
		app := newApp(t, marker, harness.Text("read"))
		dir := filepath.Join(app.Workdir(), "sub")
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("from-sub"), 0o600),
		)

		result := app.Run(t, "run", "-a", "coder", "-p", "read the marker", "-C", "sub")

		result.RequireSuccess(t)
		require.Contains(t, result.Stderr, "from-sub")
		require.Equal(t, dir, app.Session(t, result.SessionID(t)).Header.Workdir)
	})

	t.Run("with a workspace that does not exist", func(t *testing.T) {
		app := newApp(t, marker)

		result := app.Run(t, "run", "-a", "coder", "-p", "read the marker", "-C", "ghost")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "ghost")
		require.Empty(t, app.Provider().Requests())
	})
}

// TestRunHonorsToolTimeouts verifies that the timeout a model requests for a
// command is enforced and reported back to the model.
func TestRunHonorsToolTimeouts(t *testing.T) {
	app := newApp(t,
		harness.ToolCall("shell", map[string]any{
			"command":    "sleep 30",
			"timeout_ms": 200,
		}),
		harness.Text("stopped"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "hang")

	result.RequireSuccess(t)
	require.Equal(t, "stopped\n", result.Stdout)
	require.Contains(t, result.Stderr, "tool failed")
	require.Contains(t, result.Stderr, "command timed out after 200ms")
}

// TestRunRejectsUnknownAgents verifies that a run naming an agent the
// installation does not define fails and points at the missing definition.
func TestRunRejectsUnknownAgents(t *testing.T) {
	app := newApp(t)

	result := app.Run(t, "run", "-a", "ghost", "-p", "hi")

	require.Equal(t, 1, result.Code)
	require.Empty(t, result.Stdout)
	require.Contains(t, result.Stderr, app.AgentPath("ghost"))
}

// TestRunInterruptsARun verifies that an interrupt signal ends the run, leaves
// the session stored and readable, and reports the interruption.
func TestRunInterruptsARun(t *testing.T) {
	streaming := harness.Text(strings.Repeat("fragment ", 40))
	streaming.Chunks = 40
	streaming.ChunkDelay = 50 * time.Millisecond

	app := newApp(t, streaming)
	process := app.Start(t, "run", "-a", "coder", "-p", "keep talking")

	app.Provider().WaitRequest(t)
	process.Interrupt(t)

	result := process.Wait(t)
	require.Equal(t, 1, result.Code)
	require.Contains(t, result.Stderr, "the run was interrupted")

	sessions := app.Sessions(t)
	require.Len(t, sessions, 1)
	require.Equal(t, "keep talking", sessions[0].Entries[0].Text())
}

// TestRunReportsProviderFailures verifies that a failed provider response ends
// the run with a failure instead of an empty answer.
func TestRunReportsProviderFailures(t *testing.T) {
	app := newApp(t, harness.Text("hello"))

	first := app.Run(t, "run", "-a", "coder", "-p", "hi")
	first.RequireSuccess(t)

	// The script holds a single turn, so the second run finds no response to
	// serve and the provider fails.
	second := app.Run(t, "run", "-a", "coder", "-p", "again")

	require.Equal(t, 1, second.Code)
	require.Empty(t, second.Stdout)
	require.Contains(t, second.Stderr, "status 500")
	require.Contains(t, second.Stderr, "the script ran out of turns")
}

// TestRunRejectsEmptyAnswers verifies that an answer with no content fails the
// run instead of ending it silently.
func TestRunRejectsEmptyAnswers(t *testing.T) {
	app := newApp(t, harness.Text(""))

	result := app.Run(t, "run", "-a", "coder", "-p", "say nothing")

	require.Equal(t, 1, result.Code)
	require.Empty(t, result.Stdout)
	require.Contains(t, result.Stderr, "the model returned an empty response")
}
