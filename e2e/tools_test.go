//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// wordCountTool is a user tool that counts the words of a file.
const wordCountTool = `module.exports = {
  description: "Counts the words of a file in the workspace.",
  parameters: {
    type: "object",
    properties: {path: {type: "string"}},
    required: ["path"],
    additionalProperties: false,
  },
  execute: function(ctx, args) {
    ctx.log("counting " + args.path);
    const text = ctx.file.read(args.path);
    return String(text.trim().split(/\s+/).length);
  },
};`

// toolsApp returns an instance with the given user tools and an agent that
// declares them.
func toolsApp(
	t *testing.T,
	agent harness.Agent,
	tools []harness.Tool,
	hooks []harness.Hook,
	turns ...harness.Turn,
) *harness.Harness {
	t.Helper()
	return harness.New(t, harness.Options{
		Script: turns,
		Agents: []harness.Agent{agent},
		Tools:  tools,
		Hooks:  hooks,
	})
}

func wordCountAgent() harness.Agent {
	definition := coderAgent()
	definition.Tools = []string{"word-count"}
	return definition
}

// TestToolsRunsAUserTool verifies the whole user tool path: the script runs,
// its result travels back to the model, and its streamed output reaches the
// front end.
func TestToolsRunsAUserTool(t *testing.T) {
	app := toolsApp(t, wordCountAgent(),
		[]harness.Tool{{Dir: "word-count", Script: wordCountTool}},
		nil,
		harness.ToolCall("word-count", map[string]any{"path": "note.txt"}),
		harness.Text("three words"),
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(app.Workdir(), "note.txt"), []byte("one two three"), 0o600),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "count it")

	result.RequireSuccess(t)
	require.Equal(t, "three words\n", result.Stdout)
	require.Contains(t, result.Stderr, "counting note.txt")

	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "3")
}

// TestToolsReportsAnErrorResult verifies that {text, isError: true} reaches
// the model marked as a failed result.
func TestToolsReportsAnErrorResult(t *testing.T) {
	app := toolsApp(t, wordCountAgent(),
		[]harness.Tool{{Dir: "word-count", Script: `module.exports = {
  description: "Always fails.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return {text: "cannot count", isError: true}; },
};`}},
		nil,
		harness.ToolCall("word-count", map[string]any{}),
		harness.Text("understood"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "count it")

	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, "tool failed")
	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "cannot count")
}

// TestToolsSurvivesAThrow verifies that a throwing execute becomes an error
// result and the run still finishes.
func TestToolsSurvivesAThrow(t *testing.T) {
	app := toolsApp(t, wordCountAgent(),
		[]harness.Tool{{Dir: "word-count", Script: `module.exports = {
  description: "Always throws.",
  parameters: {type: "object"},
  execute: function(ctx, args) { throw new Error("kaput"); },
};`}},
		nil,
		harness.ToolCall("word-count", map[string]any{}),
		harness.Text("recovered"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "count it")

	result.RequireSuccess(t)
	require.Equal(t, "recovered\n", result.Stdout)
	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "kaput")
}

// TestToolsShadowABuiltin verifies that a user tool named shell wins: the
// model sees the user description and the user code runs.
func TestToolsShadowABuiltin(t *testing.T) {
	definition := coderAgent()
	definition.Tools = []string{"shell"}
	app := toolsApp(t, definition,
		[]harness.Tool{{Dir: "shell", Script: `module.exports = {
  description: "User shell replacement.",
  parameters: {type: "object", properties: {command: {type: "string"}}, required: ["command"], additionalProperties: false},
  execute: function(ctx, args) { return "user says " + args.command; },
};`}},
		nil,
		harness.ToolCall("shell", map[string]any{"command": "echo hi"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "run it")

	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, `tool "shell" replaces a built-in tool`)
	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "user says echo hi")
}

// TestToolsStopsARepeatedCall verifies that a model stuck on the same tool
// call with the same arguments and the same result does not loop forever: the
// run stops and the failure names the loop.
func TestToolsStopsARepeatedCall(t *testing.T) {
	definition := coderAgent()
	definition.Tools = []string{"shell"}
	// The model asks for the very same call over and over, which the shell
	// tool rejects with the same error every time.
	script := make([]harness.Turn, 0, 6)
	for range 6 {
		script = append(script, harness.ToolCall("shell", map[string]any{}))
	}
	script = append(script, harness.Text("never reached"))
	app := toolsApp(t, definition, nil, nil, script...)

	result := app.Run(t, "run", "-a", "coder", "-p", "loop")

	require.NotEqual(t, 0, result.Code, "the run fails instead of looping forever")
	require.Contains(t, result.Stderr, "kept requesting the same tool call")
	require.Less(
		t,
		len(app.Provider().Requests()),
		len(script),
		"the run stops well before the script is exhausted",
	)
}

// TestToolsSkipsABrokenTool verifies that a broken tool the agent does not
// declare is reported on standard error while the run succeeds without it. A
// declared tool that cannot load fails the run like an unknown tool, because
// declarations are strict.
func TestToolsSkipsABrokenTool(t *testing.T) {
	app := toolsApp(t, coderAgent(),
		[]harness.Tool{{Dir: "word-count", Script: `module.exports = {;`}},
		nil,
		harness.Text("no tools needed"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "hello")

	result.RequireSuccess(t)
	require.Equal(t, "no tools needed\n", result.Stdout)
	require.Contains(t, result.Stderr, `tool "word-count"`)
}

// TestToolsRejectsABrokenDeclaredTool verifies that an agent declaring a tool
// that cannot load fails the run naming it.
func TestToolsRejectsABrokenDeclaredTool(t *testing.T) {
	app := toolsApp(t, wordCountAgent(),
		[]harness.Tool{{Dir: "word-count", Script: `module.exports = {;`}},
		nil,
		harness.Text("unused"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "hello")

	require.NotEqual(t, 0, result.Code)
	require.Contains(t, result.Stderr, `unknown tool "word-count"`)
}

// TestToolsRejectsAnUnknownTool verifies that an agent declaring a tool that
// does not exist fails the run naming it.
func TestToolsRejectsAnUnknownTool(t *testing.T) {
	definition := coderAgent()
	definition.Tools = []string{"ghost"}
	app := toolsApp(t, definition, nil, nil, harness.Text("unused"))

	result := app.Run(t, "run", "-a", "coder", "-p", "go")

	require.NotEqual(t, 0, result.Code)
	require.Contains(t, result.Stderr, `unknown tool "ghost"`)
}

// TestToolsConfirmDefaultsToFalse verifies that ctx.confirm in a headless run
// answers false with no prompt and no failure.
func TestToolsConfirmDefaultsToFalse(t *testing.T) {
	definition := coderAgent()
	definition.Tools = []string{"asker"}
	app := toolsApp(t, definition,
		[]harness.Tool{{Dir: "asker", Script: `module.exports = {
  description: "Asks the user.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return "approved=" + ctx.confirm({title: "asker", body: "proceed?"}); },
};`}},
		nil,
		harness.ToolCall("asker", map[string]any{}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "ask")

	result.RequireSuccess(t)
	chat := app.Provider().Requests()[1].Chat(t)
	require.Contains(t, chat.Messages[3].Text(), "approved=false")
}

// TestToolsAutoApprove verifies that --auto-approve and -y answer true.
func TestToolsAutoApprove(t *testing.T) {
	script := `module.exports = {
  description: "Asks the user.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return "approved=" + ctx.confirm({title: "asker", body: "proceed?"}); },
};`
	definition := coderAgent()
	definition.Tools = []string{"asker"}

	for _, flag := range []string{"--auto-approve", "-y"} {
		app := toolsApp(t, definition,
			[]harness.Tool{{Dir: "asker", Script: script}},
			nil,
			harness.ToolCall("asker", map[string]any{}),
			harness.Text("done"),
		)

		result := app.Run(t, "run", "-a", "coder", "-p", "ask", flag)

		result.RequireSuccess(t)
		chat := app.Provider().Requests()[1].Chat(t)
		require.Contains(t, chat.Messages[3].Text(), "approved=true", flag)
	}
}

// TestToolsNotifyPrintsNothing verifies that ctx.notify in a headless run
// succeeds and prints nothing.
func TestToolsNotifyPrintsNothing(t *testing.T) {
	definition := coderAgent()
	definition.Tools = []string{"notifier"}
	app := toolsApp(t, definition,
		[]harness.Tool{{Dir: "notifier", Script: `module.exports = {
  description: "Notifies.",
  parameters: {type: "object"},
  execute: function(ctx, args) { ctx.notify({title: "n", body: "hi"}); return "ok"; },
};`}},
		nil,
		harness.ToolCall("notifier", map[string]any{}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "notify")

	result.RequireSuccess(t)
	require.NotContains(t, result.Stderr, "hi")
}
