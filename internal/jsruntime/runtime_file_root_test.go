package jsruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFileRoot verifies the confined file scope: with a FileRoot set, the
// ctx.file paths resolve against it and can touch nothing else; without one,
// the workspace behavior of every extension is unchanged.
func TestFileRoot(t *testing.T) {
	t.Run("resolves relative paths against the root", func(t *testing.T) {
		ctx, opts := runCtx(t)
		root := t.TempDir()
		opts.FileRoot = root

		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("cache/m.json", "{}")`)
		require.NoError(t, err)
		//nolint:gosec // the test selects the path it created.
		data, readErr := os.ReadFile(filepath.Join(root, "cache", "m.json"))
		require.NoError(t, readErr)
		require.Equal(t, "{}", string(data))
	})

	t.Run("refuses absolute paths", func(t *testing.T) {
		ctx, opts := runCtx(t)
		opts.FileRoot = t.TempDir()

		abs := filepath.Join(filepath.Dir(opts.FileRoot), "elsewhere.txt")
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write(`+"`"+abs+"`"+`, "x")`)
		require.Error(t, err)
		require.NoFileExists(t, abs)
	})

	t.Run("refuses traversal out of the root", func(t *testing.T) {
		ctx, opts := runCtx(t)
		root := t.TempDir()
		opts.FileRoot = root

		escaped := filepath.Join(filepath.Dir(root), "escaped.txt")
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("../escaped.txt", "x")`)
		require.Error(t, err)
		require.NoFileExists(t, escaped)
	})

	t.Run("stays inside the root for nested names", func(t *testing.T) {
		ctx, opts := runCtx(t)
		root := t.TempDir()
		opts.FileRoot = root

		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("a/../b", "x")`)
		require.NoError(t, err)
		require.FileExists(t, filepath.Join(root, "b"))
	})

	t.Run("unconfined extensions keep the workspace behavior", func(t *testing.T) {
		ctx, opts := runCtx(t)
		inside := filepath.Join(opts.Workdir, "relative.txt")
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("relative.txt", "x")`)
		require.NoError(t, err)
		require.FileExists(t, inside)
	})
}

// TestCompileSource verifies that embedded sources compile and run like file
// modules, under their declared name.
func TestCompileSource(t *testing.T) {
	module, err := CompileSource(
		"opencode-go",
		`module.exports = { run: function(ctx) { return 1; } };`,
	)
	require.NoError(t, err)
	require.Equal(t, "opencode-go", module.Name())

	ctx, opts := runCtx(t)
	var got any
	require.NoError(t, module.Invoke(ctx, opts, nil, func(rt *Runtime, exports *Exports) error {
		run, ok := exports.Field("run")
		require.True(t, ok)
		value, callErr := run.Call(ctx, rt.vm.Get("ctx"))
		if callErr != nil {
			return callErr
		}
		got, _ = value.JSON()
		return nil
	}))
	require.Equal(t, float64(1), got)

	t.Run("reports a syntax error without a path", func(t *testing.T) {
		_, err := CompileSource("broken", "%%^")
		require.Error(t, err)
		require.Contains(t, err.Error(), `extension "broken"`)
		require.NotContains(t, err.Error(), "/")
	})
}
