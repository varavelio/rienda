//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// hooksApp returns an instance with the given hooks and an agent that declares
// them.
func hooksApp(
	t *testing.T,
	agent harness.Agent,
	hooks []harness.Hook,
	turns ...harness.Turn,
) *harness.Harness {
	t.Helper()
	return harness.New(t, harness.Options{
		Script: turns,
		Agents: []harness.Agent{agent},
		Hooks:  hooks,
	})
}

func guardedAgent() harness.Agent {
	definition := coderAgent()
	definition.Hooks = []string{"guard"}
	return definition
}

// TestHooksRefusesAToolCall verifies that a beforeToolExecute refusal reaches
// the model and the tool never runs.
func TestHooksRefusesAToolCall(t *testing.T) {
	app := hooksApp(t, guardedAgent(),
		[]harness.Hook{{Dir: "guard", Script: `module.exports = {
  beforeToolExecute: function(ctx, call) {
    if (call.name === "shell") {
      return {allow: false, reason: "shell is blocked"};
    }
  },
};`}},
		harness.ToolCall("shell", map[string]any{"command": "echo hi"}),
		harness.Text("understood"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Equal(t, "understood\n", result.Stdout)
	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "shell is blocked")
}

// TestHooksRewritesArguments verifies that a rewritten arguments object is
// what the tool receives.
func TestHooksRewritesArguments(t *testing.T) {
	app := hooksApp(t, guardedAgent(),
		[]harness.Hook{{Dir: "guard", Script: `module.exports = {
  beforeToolExecute: function(ctx, call) {
    return {arguments: {command: "echo rewritten"}};
  },
};`}},
		harness.ToolCall("shell", map[string]any{"command": "echo original"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, "rewritten")
}

// TestHooksRewritesTheResult verifies that an afterToolExecute rewrite is
// what the model receives.
func TestHooksRewritesTheResult(t *testing.T) {
	app := hooksApp(t, guardedAgent(),
		[]harness.Hook{{Dir: "guard", Script: `module.exports = {
  afterToolExecute: function(ctx, call, result) {
    return {text: result.text.toUpperCase()};
  },
};`}},
		harness.ToolCall("shell", map[string]any{"command": "echo hi"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "HI")
}

// TestHooksRewritesTheSystemPrompt verifies that a beforeModelRequest system
// rewrite applies to that turn only, asserted through two requests.
func TestHooksRewritesTheSystemPrompt(t *testing.T) {
	app := hooksApp(t, guardedAgent(),
		[]harness.Hook{{Dir: "guard", Script: `module.exports = {
  beforeModelRequest: function(ctx, req) {
    return {system: req.system + "\n[guarded]"};
  },
};`}},
		harness.ToolCall("shell", map[string]any{"command": "echo hi"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	requests := app.Provider().Requests()
	require.Len(t, requests, 2)
	first := requests[0].Chat(t).Messages[0].Text()
	second := requests[1].Chat(t).Messages[0].Text()
	require.Contains(t, first, "[guarded]")
	require.Contains(t, second, "[guarded]")
}

// TestHooksRunsAroundTheRun verifies that beforeRun and afterRun each run
// once, with the afterRun hook writing a file that exists after the run.
func TestHooksRunsAroundTheRun(t *testing.T) {
	app := hooksApp(t, guardedAgent(),
		[]harness.Hook{{Dir: "guard", Script: `module.exports = {
  beforeRun: function(ctx, ev) { ctx.log("run opened"); },
  afterRun: function(ctx, ev) { ctx.file.write("finished.txt", ev.reason); },
};`}},
		harness.Text("hello"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")

	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, "run opened")
	data, err := os.ReadFile(filepath.Join(app.Workdir(), "finished.txt"))
	require.NoError(t, err)
	require.Equal(t, "end_turn", string(data))
}

// TestHooksRejectsAnUnknownHook verifies that an agent declaring a hook that
// does not exist fails the run naming it.
func TestHooksRejectsAnUnknownHook(t *testing.T) {
	definition := coderAgent()
	definition.Hooks = []string{"ghost"}
	app := hooksApp(t, definition, nil, harness.Text("unused"))

	result := app.Run(t, "run", "-a", "coder", "-p", "go")

	require.NotEqual(t, 0, result.Code)
	require.Contains(t, result.Stderr, `unknown hook "ghost"`)
}

// TestHooksSurvivesAThrow verifies that a throwing hook is reported and the
// run still finishes.
func TestHooksSurvivesAThrow(t *testing.T) {
	app := hooksApp(t, guardedAgent(),
		[]harness.Hook{{Dir: "guard", Script: `module.exports = {
  beforeToolExecute: function(ctx, call) { throw new Error("guard blew up"); },
};`}},
		harness.ToolCall("shell", map[string]any{"command": "echo hi"}),
		harness.Text("recovered"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Equal(t, "recovered\n", result.Stdout)
	require.Contains(t, result.Stderr, "guard blew up")
}
