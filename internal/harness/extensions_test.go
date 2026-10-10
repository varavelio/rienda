package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/jsruntime"
)

// writeExtension writes an index.js extension into dir.
func writeExtension(t *testing.T, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, name, "index.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(script), 0o600))
}

// writeRawAgent writes an agent definition with the given frontmatter lines.
func writeRawAgent(t *testing.T, env *testEnvironment, id, frontmatter, prompt string) {
	t.Helper()
	definition := "---\n" + frontmatter + "---\n" + prompt + "\n"
	require.NoError(t, os.WriteFile(env.agentPath(id), []byte(definition), 0o600))
}

// namedToolScript builds the streaming chunks of a tool call with any name.
func namedToolScript(id, name, arguments string) []string {
	return []string{
		roleChunk,
		`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":` +
			`[{"index":0,"id":"` + id + `","type":"function",` +
			`"function":{"name":"` + name + `","arguments":""}}]}}]}`,
		`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":` +
			`[{"index":0,"function":{"arguments":` + strconv.Quote(arguments) + `}}]}}]}`,
		finishChunk("tool_calls"),
	}
}

// extensionOptions returns the environment options with tool and hook
// directories.
func extensionOptions(env *testEnvironment, toolsDir, hooksDir string) Options {
	options := env.options()
	options.ToolsDir = toolsDir
	options.HooksDir = hooksDir
	return options
}

// runStartDiagnostics drains a run and returns its start diagnostics.
func runStartDiagnostics(t *testing.T, session *Session, prompt string) []string {
	t.Helper()
	for event := range session.Run(t.Context(), prompt) {
		if event.Type == engine.EventRunStart {
			return event.Diagnostics
		}
	}
	t.Fatal("the run emitted no start event")
	return nil
}

const wordCountScript = `module.exports = {
  description: "Counts words.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return "three"; },
};`

func TestExtensions(t *testing.T) {
	t.Run("loads a user tool", func(t *testing.T) {
		env := newTestEnvironment(
			t,
			namedToolScript("call_1", "word-count", `{}`),
			textScript("done"),
		)
		toolsDir := filepath.Join(t.TempDir(), "tools")
		writeExtension(t, toolsDir, "word-count", wordCountScript)
		writeRawAgent(
			t,
			env,
			"coder",
			"description: A test agent\ntools: [word-count]\n",
			"You answer briefly.",
		)

		options := extensionOptions(env, toolsDir, filepath.Join(t.TempDir(), "hooks"))
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		events := collectEvents(prepared.Run(t.Context(), "count"))
		require.Equal(t, "done", joinedText(events))
	})

	t.Run("shadows a built-in with a diagnostic", func(t *testing.T) {
		env := newTestEnvironment(t, namedToolScript("call_1", "shell", `{}`), textScript("done"))
		toolsDir := filepath.Join(t.TempDir(), "tools")
		writeExtension(t, toolsDir, "shell", `module.exports = {
  description: "User shell.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return "user shell"; },
};`)

		options := extensionOptions(env, toolsDir, filepath.Join(t.TempDir(), "hooks"))
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		diagnostics := runStartDiagnostics(t, prepared, "go")
		require.Contains(t, diagnostics, `tool "shell" replaces a built-in tool`)
	})

	t.Run("reports a broken tool once per run", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("hi"))
		toolsDir := filepath.Join(t.TempDir(), "tools")
		writeExtension(t, toolsDir, "broken", `module.exports = {;`)

		options := extensionOptions(env, toolsDir, filepath.Join(t.TempDir(), "hooks"))
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		diagnostics := runStartDiagnostics(t, prepared, "hi")
		require.Len(t, diagnostics, 1)
		require.Contains(t, diagnostics[0], `tool "broken"`)
	})

	t.Run("runs a hook", func(t *testing.T) {
		env := newTestEnvironment(
			t,
			namedToolScript("call_1", "shell", `{"command":"echo hi"}`),
			textScript("done"),
		)
		hooksDir := filepath.Join(t.TempDir(), "hooks")
		writeExtension(t, hooksDir, "guard", `module.exports = {
  beforeToolExecute: function(ctx, call) { return {arguments: {command: "echo hooked"}}; },
};`)
		writeRawAgent(
			t,
			env,
			"coder",
			"description: A test agent\ntools: [shell]\nhooks: [guard]\n",
			"You answer briefly.",
		)

		options := extensionOptions(env, filepath.Join(t.TempDir(), "tools"), hooksDir)
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		events := collectEvents(prepared.Run(t.Context(), "go"))
		require.Equal(t, "done", joinedText(events))
		require.Len(t, env.provider.requests, 2)
	})

	t.Run("refuses an unknown hook at run time", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("hi"))
		writeRawAgent(
			t,
			env,
			"coder",
			"description: A test agent\nhooks: [ghost]\n",
			"You answer briefly.",
		)

		options := extensionOptions(
			env,
			filepath.Join(t.TempDir(), "tools"),
			filepath.Join(t.TempDir(), "hooks"),
		)
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		events := collectEvents(prepared.Run(t.Context(), "hi"))
		last := events[len(events)-1]
		require.Equal(t, engine.EndReasonError, last.Reason)
	})

	t.Run("confirm refuses without an interactor", func(t *testing.T) {
		env := newTestEnvironment(t, namedToolScript("call_1", "asker", `{}`), textScript("done"))
		toolsDir := filepath.Join(t.TempDir(), "tools")
		writeExtension(t, toolsDir, "asker", `module.exports = {
  description: "Asks.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return "approved=" + ctx.confirm({title: "t", body: "b"}); },
};`)
		writeRawAgent(
			t,
			env,
			"coder",
			"description: A test agent\ntools: [asker]\n",
			"You answer briefly.",
		)

		options := extensionOptions(env, toolsDir, filepath.Join(t.TempDir(), "hooks"))
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		collectEvents(prepared.Run(t.Context(), "ask"))
		require.Len(t, env.provider.requests, 2)
	})

	t.Run("auto approve answers confirmations", func(t *testing.T) {
		env := newTestEnvironment(t, namedToolScript("call_1", "asker", `{}`), textScript("done"))
		toolsDir := filepath.Join(t.TempDir(), "tools")
		writeExtension(t, toolsDir, "asker", `module.exports = {
  description: "Asks.",
  parameters: {type: "object"},
  execute: function(ctx, args) { return "approved=" + ctx.confirm({title: "t", body: "b"}); },
};`)
		writeRawAgent(
			t,
			env,
			"coder",
			"description: A test agent\ntools: [asker]\n",
			"You answer briefly.",
		)

		options := extensionOptions(env, toolsDir, filepath.Join(t.TempDir(), "hooks"))
		options.AutoApprove = true
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		collectEvents(prepared.Run(t.Context(), "ask"))
		require.Len(t, env.provider.requests, 2)
	})

	t.Run("shares one interactor between tools and hooks", func(t *testing.T) {
		env := newTestEnvironment(t, namedToolScript("call_1", "asker", `{}`), textScript("done"))
		toolsDir := filepath.Join(t.TempDir(), "tools")
		writeExtension(t, toolsDir, "asker", `module.exports = {
  description: "Asks.",
  parameters: {type: "object"},
  execute: function(ctx, args) { ctx.notify({title: "asker", body: "asking"}); return "ok"; },
};`)
		writeRawAgent(
			t,
			env,
			"coder",
			"description: A test agent\ntools: [asker]\n",
			"You answer briefly.",
		)

		interactor := &recordingInteractor{}
		options := extensionOptions(env, toolsDir, filepath.Join(t.TempDir(), "hooks"))
		options.Interactor = interactor
		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		collectEvents(prepared.Run(t.Context(), "ask"))
		require.Len(t, interactor.notices, 1)
		require.Equal(t, "asker", interactor.notices[0].Title)
	})
}

// recordingInteractor records the notices of a run.
type recordingInteractor struct {
	notices []jsruntime.Notification
}

// Confirm refuses every question.
func (r *recordingInteractor) Confirm(
	ctx context.Context,
	_ jsruntime.ConfirmRequest,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("harness: confirm canceled: %w", err)
	}
	return false, nil
}

// Notify records the notice.
func (r *recordingInteractor) Notify(_ context.Context, note jsruntime.Notification) {
	r.notices = append(r.notices, note)
}
