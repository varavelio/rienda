package jsruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilePrimitives(t *testing.T) {
	t.Run("reads and writes", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("a/b.txt", "hello")`)
		require.NoError(t, err)
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.read("a/b.txt")`)
		require.NoError(t, err)
		require.Equal(t, "hello", got)
	})

	t.Run("write appends when asked", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("f.txt", "a")`)
		require.NoError(t, err)
		_, err = invokeWithContext(t, ctx, opts, `ctx.file.write("f.txt", "b", {append: true})`)
		require.NoError(t, err)
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.read("f.txt")`)
		require.NoError(t, err)
		require.Equal(t, "ab", got)
	})

	t.Run("read replaces invalid utf8", func(t *testing.T) {
		ctx, opts := runCtx(t)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(opts.Workdir, "bin"), []byte{0xff, 0xfe, 'a'}, 0o600),
		)
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.read("bin")`)
		require.NoError(t, err)
		text, ok := got.(string)
		require.True(t, ok, "expected a string")
		require.Contains(t, text, "a")
		require.NotContains(t, text, "\xff")
	})

	t.Run("read rejects a directory and a missing file", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.read("missing.txt")`)
		require.Error(t, err)
		_, err = invokeWithContext(t, ctx, opts, `ctx.file.read("")`)
		require.Error(t, err)
	})

	t.Run("read rejects other encodings", func(t *testing.T) {
		ctx, opts := runCtx(t)
		require.NoError(t, os.WriteFile(filepath.Join(opts.Workdir, "f.txt"), []byte("x"), 0o600))
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.read("f.txt", {encoding: "base64"})`)
		require.ErrorContains(t, err, "base64")
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.read("f.txt", {encoding: "utf8"})`)
		require.NoError(t, err)
		require.Equal(t, "x", got)
	})

	t.Run("rejects non-string data", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.file.write("f.txt", 42)`)
		require.Error(t, err)
	})

	t.Run("exists never raises", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.exists("nope")`)
		require.NoError(t, err)
		require.Equal(t, false, got)
		got, err = invokeWithContext(t, ctx, opts, `ctx.file.exists("")`)
		require.NoError(t, err)
		require.Equal(t, true, got)
	})

	t.Run("lists with defaults", func(t *testing.T) {
		ctx, opts := runCtx(t)
		require.NoError(t, os.MkdirAll(filepath.Join(opts.Workdir, "sub"), 0o750))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(opts.Workdir, "sub", "a.txt"), []byte("x"), 0o600),
		)
		require.NoError(t, os.WriteFile(filepath.Join(opts.Workdir, "top.txt"), []byte("x"), 0o600))
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.list("")`)
		require.NoError(t, err)
		items, ok := got.([]any)
		require.True(t, ok, "expected a list")
		byPath := entryMap(t, items)
		require.Contains(t, byPath, "top.txt")
		require.Contains(t, byPath, "sub/a.txt")
		require.Equal(t, "a.txt", requireString(t, requireObject(t, byPath["sub/a.txt"]), "name"))
		require.Equal(t, false, requireBool(t, requireObject(t, byPath["top.txt"]), "isDir"))
		require.Equal(t, true, requireBool(t, requireObject(t, byPath["sub"]), "isDir"))
	})

	t.Run("lists shallow and unfiltered", func(t *testing.T) {
		ctx, opts := runCtx(t)
		require.NoError(t, os.MkdirAll(filepath.Join(opts.Workdir, "sub"), 0o750))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(opts.Workdir, "sub", "a.txt"), []byte("x"), 0o600),
		)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(opts.Workdir, ".gitignore"), []byte("ignored.txt\n"), 0o600),
		)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(opts.Workdir, "ignored.txt"), []byte("x"), 0o600),
		)
		got, err := invokeWithContext(t, ctx, opts, `ctx.file.list("", {recursive: false})`)
		require.NoError(t, err)
		paths := entryPaths(t, got)
		require.Contains(t, paths, "sub")
		require.NotContains(t, paths, "ignored.txt")
		require.NotContains(t, paths, "sub/a.txt")
		got, err = invokeWithContext(
			t,
			ctx,
			opts,
			`ctx.file.list("", {recursive: false, respectIgnoreFiles: false})`,
		)
		require.NoError(t, err)
		require.Contains(t, entryPaths(t, got), "ignored.txt")
	})

	t.Run("cancellation raises", func(t *testing.T) {
		ctx, opts := runCtx(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		for _, body := range []string{`ctx.file.read("f")`, `ctx.file.exists("f")`, `ctx.file.list("")`} {
			_, err := invokeWithContext(t, canceled, opts, body)
			require.Error(t, err, body)
		}
	})
}

// requireBool returns the boolean field of a plain object.
func requireBool(t *testing.T, doc map[string]any, field string) bool {
	t.Helper()
	flag, ok := doc[field].(bool)
	require.True(t, ok, "expected a boolean at %q", field)
	return flag
}

// entryMap indexes list entries by path.
func entryMap(t *testing.T, items []any) map[string]any {
	t.Helper()
	byPath := make(map[string]any, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		require.True(t, ok, "expected an entry")
		path, ok := entry["path"].(string)
		require.True(t, ok, "expected a path")
		byPath[path] = entry
	}
	return byPath
}

// entryPaths returns the paths of a ctx.file.list result.
func entryPaths(t *testing.T, got any) []string {
	t.Helper()
	items, ok := got.([]any)
	require.True(t, ok, "expected a list")
	paths := make([]string, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		require.True(t, ok, "expected an entry")
		path, ok := entry["path"].(string)
		require.True(t, ok, "expected a path")
		paths = append(paths, path)
	}
	return paths
}
