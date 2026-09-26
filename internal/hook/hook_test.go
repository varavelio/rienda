package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/jsruntime"
)

// writeHook writes a hook module into dir.
func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name, "index.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func discoverHooks(t *testing.T, dir string) Registry {
	t.Helper()
	registry, diagnostics := Discover(dir, jsruntime.Options{Workdir: t.TempDir()})
	require.Empty(t, diagnostics)
	return registry
}

func hookContext() context.Context {
	return engine.WithNotice(context.Background(), func(string) {})
}

func TestDiscover(t *testing.T) {
	t.Run("yields an empty registry for a missing directory", func(t *testing.T) {
		registry, diagnostics := Discover(
			filepath.Join(t.TempDir(), "missing"),
			jsruntime.Options{},
		)
		require.Empty(t, registry.Names())
		require.Empty(t, diagnostics)
	})

	t.Run("loads extensions in sorted order", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "beta", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		writeHook(t, dir, "alpha", `module.exports = {afterRun: function(ctx, ev) {}};`)
		registry, diagnostics := Discover(dir, jsruntime.Options{Workdir: t.TempDir()})
		require.Empty(t, diagnostics)
		require.Equal(t, []string{"alpha", "beta"}, registry.Names())
	})

	t.Run("skips what is not an extension", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "good", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		writeHook(t, dir, ".hidden", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		require.NoError(t, os.Mkdir(filepath.Join(dir, "empty"), 0o750))
		registry, diagnostics := Discover(dir, jsruntime.Options{Workdir: t.TempDir()})
		require.Empty(t, diagnostics)
		require.Equal(t, []string{"good"}, registry.Names())
	})

	t.Run("reports one diagnostic per problem", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "good", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		writeHook(t, dir, "broken", `module.exports = {;`)
		writeHook(t, dir, "empty", `module.exports = {};`)
		writeHook(t, dir, "no good", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		writeHook(
			t,
			dir,
			"mixed",
			`module.exports = {beforeRun: "nope", afterRun: function(ctx, ev) {}, extra: 1};`,
		)
		registry, diagnostics := Discover(dir, jsruntime.Options{Workdir: t.TempDir()})
		require.Equal(t, []string{"good", "mixed"}, registry.Names())
		require.Len(t, diagnostics, 5)
		for _, diagnostic := range diagnostics {
			require.Contains(t, diagnostic, "hook \"")
		}
	})

	t.Run("keeps a valid sibling", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "broken", `module.exports = {;`)
		writeHook(t, dir, "good", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		registry, _ := Discover(dir, jsruntime.Options{Workdir: t.TempDir()})
		require.Equal(t, []string{"good"}, registry.Names())
	})
}

func TestResolve(t *testing.T) {
	t.Run("returns hooks in declaration order", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "a", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		writeHook(t, dir, "b", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		hooks, err := discoverHooks(t, dir).Resolve([]string{"b", "a"})
		require.NoError(t, err)
		require.Equal(t, []string{"b", "a"}, hooks.order)
	})

	t.Run("collapses duplicates", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "a", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		hooks, err := discoverHooks(t, dir).Resolve([]string{"a", "a"})
		require.NoError(t, err)
		require.Equal(t, []string{"a"}, hooks.order)
	})

	t.Run("fails on an unknown name", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(t, dir, "a", `module.exports = {beforeRun: function(ctx, ev) {}};`)
		_, err := discoverHooks(t, dir).Resolve([]string{"ghost"})
		require.ErrorContains(t, err, `unknown hook "ghost"`)
		require.ErrorContains(t, err, "a")
	})
}

func resolveSingle(t *testing.T, body string) *Hooks {
	t.Helper()
	dir := t.TempDir()
	writeHook(t, dir, "probe", body)
	hooks, err := discoverHooks(t, dir).Resolve([]string{"probe"})
	require.NoError(t, err)
	return hooks
}

func TestHookContracts(t *testing.T) {
	t.Run("no opinion leaves behavior unchanged", func(t *testing.T) {
		hooks := resolveSingle(t, `module.exports = {beforeToolExecute: function(ctx, call) {}};`)
		result := hooks.BeforeToolExecute(hookContext(), engine.BeforeToolExecuteHook{
			ID: "c", Name: "shell", Arguments: json.RawMessage(`{"a":1}`),
		})
		require.Nil(t, result.Allow)
		require.Empty(t, result.Arguments)
	})

	t.Run("refuses with the reason", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {beforeToolExecute: function(ctx, call) { return {allow: false, reason: "no"}; }};`,
		)
		result := hooks.BeforeToolExecute(hookContext(), engine.BeforeToolExecuteHook{
			ID: "c", Name: "shell", Arguments: json.RawMessage(`{}`),
		})
		require.NotNil(t, result.Allow)
		require.False(t, *result.Allow)
		require.Equal(t, "no", result.Reason)
	})

	t.Run("chains arguments in order", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(
			t,
			dir,
			"first",
			`module.exports = {beforeToolExecute: function(ctx, call) { return {arguments: {n: 1}}; }};`,
		)
		writeHook(
			t,
			dir,
			"second",
			`module.exports = {beforeToolExecute: function(ctx, call) { call.arguments.n++; return {arguments: call.arguments}; }};`,
		)
		hooks, err := discoverHooks(t, dir).Resolve([]string{"first", "second"})
		require.NoError(t, err)
		result := hooks.BeforeToolExecute(hookContext(), engine.BeforeToolExecuteHook{
			ID: "c", Name: "shell", Arguments: json.RawMessage(`{"n":0}`),
		})
		require.JSONEq(t, `{"n":2}`, string(result.Arguments))
	})

	t.Run("stops at the first refusal", func(t *testing.T) {
		dir := t.TempDir()
		writeHook(
			t,
			dir,
			"first",
			`module.exports = {beforeToolExecute: function(ctx, call) { return {allow: false, reason: "first"}; }};`,
		)
		writeHook(
			t,
			dir,
			"second",
			`module.exports = {beforeToolExecute: function(ctx, call) { return {allow: true}; }};`,
		)
		hooks, err := discoverHooks(t, dir).Resolve([]string{"first", "second"})
		require.NoError(t, err)
		result := hooks.BeforeToolExecute(hookContext(), engine.BeforeToolExecuteHook{
			ID: "c", Name: "shell", Arguments: json.RawMessage(`{}`),
		})
		require.NotNil(t, result.Allow)
		require.False(t, *result.Allow)
		require.Equal(t, "first", result.Reason)
	})

	t.Run("rewrites the tool result", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {afterToolExecute: function(ctx, call, result) { return {text: result.text + "!"}; }};`,
		)
		result := hooks.AfterToolExecute(hookContext(), engine.AfterToolExecuteHook{
			Call:   engine.HookToolCall{ID: "c", Name: "shell"},
			Result: engine.HookResult{Text: "raw"},
		})
		require.True(t, result.TextSet)
		require.Equal(t, "raw!", result.Text)
		require.Nil(t, result.IsError)
	})

	t.Run("rewrites the system prompt", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {beforeModelRequest: function(ctx, req) { return {system: req.system + "!"}; }};`,
		)
		result := hooks.BeforeModelRequest(
			hookContext(),
			engine.BeforeModelRequestHook{System: "base"},
		)
		require.True(t, result.Set)
		require.Equal(t, "base!", result.System)
	})

	t.Run("rewrites the prose", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {afterModelResponse: function(ctx, res) { return {text: "new"}; }};`,
		)
		result := hooks.AfterModelResponse(
			hookContext(),
			engine.AfterModelResponseHook{Text: "old"},
		)
		require.True(t, result.TextSet)
		require.Equal(t, "new", result.Text)
		require.False(t, result.ThinkingSet)
	})

	t.Run("a throw is a notice and no opinion", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {beforeToolExecute: function(ctx, call) { throw new Error("kaput"); }};`,
		)
		var notices []string
		ctx := engine.WithNotice(
			context.Background(),
			func(text string) { notices = append(notices, text) },
		)
		result := hooks.BeforeToolExecute(ctx, engine.BeforeToolExecuteHook{ID: "c", Name: "shell"})
		require.Nil(t, result.Allow)
		require.Len(t, notices, 1)
		require.Contains(t, notices[0], "kaput")
	})

	t.Run("a wrong-typed value is a notice", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {afterToolExecute: function(ctx, call, result) { return "nope"; }};`,
		)
		var notices []string
		ctx := engine.WithNotice(
			context.Background(),
			func(text string) { notices = append(notices, text) },
		)
		result := hooks.AfterToolExecute(ctx, engine.AfterToolExecuteHook{})
		require.False(t, result.TextSet)
		require.Len(t, notices, 1)
	})

	t.Run("a wrong-typed field is ignored alone", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {afterToolExecute: function(ctx, call, result) { return {text: 42, isError: true}; }};`,
		)
		var notices []string
		ctx := engine.WithNotice(
			context.Background(),
			func(text string) { notices = append(notices, text) },
		)
		result := hooks.AfterToolExecute(ctx, engine.AfterToolExecuteHook{})
		require.False(t, result.TextSet)
		require.NotNil(t, result.IsError)
		require.True(t, *result.IsError)
		require.Len(t, notices, 1)
	})

	t.Run("ctx.log reaches the notice sink", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {beforeRun: function(ctx, ev) { ctx.log("starting " + ev.agentId); }};`,
		)
		var notices []string
		ctx := engine.WithNotice(
			context.Background(),
			func(text string) { notices = append(notices, text) },
		)
		hooks.BeforeRun(ctx, engine.BeforeRunHook{AgentID: "coder"})
		require.Equal(t, []string{"starting coder"}, notices)
	})

	t.Run("advisory return values are ignored with a notice", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {beforeRun: function(ctx, ev) { return "hi"; }};`,
		)
		var notices []string
		ctx := engine.WithNotice(
			context.Background(),
			func(text string) { notices = append(notices, text) },
		)
		hooks.BeforeRun(ctx, engine.BeforeRunHook{})
		require.Len(t, notices, 1)
	})

	t.Run("works without a notice sink", func(t *testing.T) {
		hooks := resolveSingle(
			t,
			`module.exports = {beforeRun: function(ctx, ev) { ctx.log("hi"); throw new Error("x"); }};`,
		)
		hooks.BeforeRun(context.Background(), engine.BeforeRunHook{})
		hooks.AfterRun(context.Background(), engine.AfterRunHook{})
	})
}
