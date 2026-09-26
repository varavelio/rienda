package jsruntime

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemPrimitives(t *testing.T) {
	t.Run("env get distinguishes unset from empty", func(t *testing.T) {
		ctx, opts := runCtx(t)
		t.Setenv("RIENDASMITH_TEST_VAR", "hello")
		t.Setenv("RIENDASMITH_EMPTY_VAR", "")
		got, err := invokeWithContext(t, ctx, opts, `ctx.env.get("RIENDASMITH_TEST_VAR")`)
		require.NoError(t, err)
		require.Equal(t, "hello", got)
		got, err = invokeWithContext(t, ctx, opts, `ctx.env.get("RIENDASMITH_EMPTY_VAR")`)
		require.NoError(t, err)
		require.Equal(t, "", got)
		got, err = invokeWithContext(t, ctx, opts, `ctx.env.get("RIENDASMITH_MISSING_VAR_xyz")`)
		require.NoError(t, err)
		require.Nil(t, got)
		_, err = invokeWithContext(t, ctx, opts, `ctx.env.get(42)`)
		require.Error(t, err)
	})

	t.Run("exec succeeds and streams", func(t *testing.T) {
		ctx, opts := runCtx(t)
		module := compileScript(
			t,
			`module.exports = { run: function(ctx) { return ctx.system.exec("echo out; echo err >&2"); } };`,
		)
		var streamed []byte
		var result any
		require.NoError(t, module.Invoke(ctx, opts, func(s Stream, data []byte) {
			streamed = append(streamed, data...)
		}, func(rt *Runtime, exports *Exports) error {
			run, _ := exports.Field("run")
			got, err := run.Call(ctx, rt.vm.Get("ctx"))
			if err != nil {
				return err
			}
			result, _ = got.JSON()
			return nil
		}))
		doc := requireObject(t, result)
		require.Equal(t, float64(0), doc["code"])
		require.Contains(t, requireString(t, doc, "stdout"), "out")
		require.Contains(t, requireString(t, doc, "stderr"), "err")
		require.Contains(t, string(streamed), "out")
	})

	t.Run("exec returns non-zero payload", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, `ctx.system.exec("exit 3")`)
		require.NoError(t, err)
		require.Equal(t, float64(3), requireNumber(t, got, "code"))
	})

	t.Run("exec raises on a missing binary", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(
			t,
			ctx,
			opts,
			`ctx.system.exec("exit 0", {cwd: "/nonexistent-rienda-dir"})`,
		)
		require.Error(t, err)
	})

	t.Run("exec honors cwd env and timeout", func(t *testing.T) {
		ctx, opts := runCtx(t)
		require.NoError(t, os.MkdirAll(opts.Workdir+"/sub", 0o750))
		got, err := invokeWithContext(t, ctx, opts, `ctx.system.exec("pwd", {cwd: "sub"})`)
		require.NoError(t, err)
		require.Contains(t, requireString(t, requireObject(t, got), "stdout"), "sub")
		got, err = invokeWithContext(
			t,
			ctx,
			opts,
			`ctx.system.exec("echo $RIENDASMITH_EXEC_VAR", {env: {RIENDASMITH_EXEC_VAR: "v"}})`,
		)
		require.NoError(t, err)
		require.Contains(t, requireString(t, requireObject(t, got), "stdout"), "v")
		got, err = invokeWithContext(t, ctx, opts, `ctx.system.exec("sleep 5", {timeout_ms: 100})`)
		require.NoError(t, err)
		require.NotEqual(t, float64(0), requireNumber(t, got, "code"))
	})

	t.Run("exec caps output", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, `ctx.system.exec("yes aaaa | head -n 200000")`)
		require.NoError(t, err)
		require.LessOrEqual(t, len(requireString(t, requireObject(t, got), "stdout")), 256<<10)
	})

	t.Run("onOutput is reserved", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.system.exec("echo hi", {onOutput: 1})`)
		require.ErrorContains(t, err, "onOutput")
	})

	t.Run("which finds and misses", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, `ctx.system.which("sh")`)
		require.NoError(t, err)
		text, ok := got.(string)
		require.True(t, ok, "expected a string")
		require.Contains(t, text, "sh")
		got, err = invokeWithContext(t, ctx, opts, `ctx.system.which("no-such-rienda-binary")`)
		require.NoError(t, err)
		require.Nil(t, got)
		_, err = invokeWithContext(t, ctx, opts, `ctx.system.which(1)`)
		require.Error(t, err)
	})

	t.Run("cancellation raises", func(t *testing.T) {
		ctx, opts := runCtx(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := invokeWithContext(t, canceled, opts, `ctx.system.exec("echo hi")`)
		require.Error(t, err)
	})
}
