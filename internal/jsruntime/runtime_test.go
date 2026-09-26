package jsruntime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeScript writes index.js into a named extension directory.
func writeScript(t *testing.T, kind, name, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), kind, name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	path := filepath.Join(dir, "index.js")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func compileScript(t *testing.T, body string) *Module {
	t.Helper()
	module, err := Compile(writeScript(t, "tools", "probe", body))
	require.NoError(t, err)
	return module
}

func TestRuntime(t *testing.T) {
	t.Run("reports a syntax error without a host path", func(t *testing.T) {
		path := writeScript(t, "tools", "broken", "module.exports = {;")
		_, err := Compile(path)
		require.Error(t, err)
		require.NotContains(t, err.Error(), path)
		require.Contains(t, err.Error(), "broken")
	})

	t.Run("shares nothing between invocations", func(t *testing.T) {
		module := compileScript(
			t,
			`module.exports = { get: function() { return globalThis.n || 0; }, set: function() { globalThis.n = 42; } };`,
		)
		var first any
		require.NoError(
			t,
			module.Invoke(t.Context(), Options{}, nil, func(rt *Runtime, exports *Exports) error {
				set, ok := exports.Field("set")
				require.True(t, ok)
				_, err := set.Call(t.Context())
				require.NoError(t, err)
				get, ok := exports.Field("get")
				require.True(t, ok)
				got, err := get.Call(t.Context())
				require.NoError(t, err)
				first, _ = got.JSON()
				return nil
			}),
		)
		require.Equal(t, float64(42), first)
		require.NoError(
			t,
			module.Invoke(t.Context(), Options{}, nil, func(rt *Runtime, exports *Exports) error {
				get, ok := exports.Field("get")
				require.True(t, ok)
				got, err := get.Call(t.Context())
				require.NoError(t, err)
				value, _ := got.JSON()
				require.Equal(t, float64(0), value)
				return nil
			}),
		)
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		module := compileScript(t, `module.exports = { run: function() { return 1; } };`)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_ = module.Invoke(
					context.Background(),
					Options{},
					nil,
					func(rt *Runtime, exports *Exports) error {
						run, ok := exports.Field("run")
						if !ok {
							return nil
						}
						_, _ = run.Call(context.Background())
						return nil
					},
				)
			})
		}
		wg.Wait()
	})

	t.Run("surfaces a throw without a host path", func(t *testing.T) {
		path := writeScript(
			t,
			"tools",
			"thrower",
			`module.exports = { run: function() { throw new Error("boom"); } };`,
		)
		module, err := Compile(path)
		require.NoError(t, err)
		err = module.Invoke(t.Context(), Options{}, nil, func(rt *Runtime, exports *Exports) error {
			run, _ := exports.Field("run")
			_, callErr := run.Call(t.Context())
			require.ErrorContains(t, callErr, "boom")
			require.NotContains(t, callErr.Error(), path)
			return callErr
		})
		require.Error(t, err)
	})

	t.Run("streams output with the right stream", func(t *testing.T) {
		module := compileScript(t, `module.exports = { run: function(ctx) { ctx.log("hello"); } };`)
		var got []byte
		require.NoError(
			t,
			module.Invoke(t.Context(), Options{Workdir: t.TempDir()}, func(s Stream, data []byte) {
				require.Equal(t, StreamStdout, s)
				got = append(got, data...)
			}, func(rt *Runtime, exports *Exports) error {
				run, _ := exports.Field("run")
				_, err := run.Call(t.Context(), rt.vm.Get("ctx"))
				return err
			}),
		)
		require.Equal(t, "hello", string(got))
	})

	t.Run("cancellation raises", func(t *testing.T) {
		module := compileScript(t, `module.exports = { run: function(ctx) { ctx.sleep(5000); } };`)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := module.Invoke(
			ctx,
			Options{Workdir: t.TempDir()},
			nil,
			func(rt *Runtime, exports *Exports) error {
				run, _ := exports.Field("run")
				_, err := run.Call(ctx, rt.vm.Get("ctx"))
				return err
			},
		)
		require.Error(t, err)
	})
}
