package workdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkdir(t *testing.T) {
	t.Run("resolve relative against base", func(t *testing.T) {
		require.Equal(t, filepath.Join("/base", "sub"), Resolve("/base", "sub"))
	})
	t.Run("keep absolute", func(t *testing.T) {
		require.Equal(t, "/other", Resolve("/base", "/other"))
	})
	t.Run("clean path", func(t *testing.T) {
		require.Equal(t, "/base", Resolve("/base", "sub/.."))
	})
	t.Run("empty returns base", func(t *testing.T) {
		require.Equal(t, "/base", Resolve("/base", ""))
	})
	t.Run("escape and back", func(t *testing.T) {
		require.Equal(t, "/other", Resolve("/base", "../other"))
		require.Equal(t, filepath.Join("/base", "x"), Resolve("/base", "../base/x"))
	})
	t.Run("base prefers context", func(t *testing.T) {
		dir := t.TempDir()
		ctx := WithWorkdir(t.Context(), dir)
		base, err := Base(ctx, t.TempDir())
		require.NoError(t, err)
		require.Equal(t, dir, base)
	})
	t.Run("base falls back to configured", func(t *testing.T) {
		dir := t.TempDir()
		base, err := Base(t.Context(), dir)
		require.NoError(t, err)
		require.Equal(t, dir, base)
	})
	t.Run("base falls back to process cwd", func(t *testing.T) {
		cwd, err := os.Getwd()
		require.NoError(t, err)
		base, err := Base(t.Context(), "")
		require.NoError(t, err)
		require.Equal(t, cwd, base)
	})
	t.Run("base rejects missing", func(t *testing.T) {
		_, err := Base(t.Context(), filepath.Join(t.TempDir(), "missing"))
		require.Error(t, err)
	})
	t.Run("base rejects file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "f")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
		_, err := Base(t.Context(), f)
		require.ErrorContains(t, err, "not a directory")
	})
	t.Run("round trip context", func(t *testing.T) {
		ctx := WithWorkdir(t.Context(), "/tmp")
		dir, ok := FromContext(ctx)
		require.True(t, ok)
		require.Equal(t, "/tmp", dir)
		_, ok = FromContext(t.Context())
		require.False(t, ok)
	})
}
