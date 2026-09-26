package tool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeDiscoveredTool writes a tool module into dir.
func writeDiscoveredTool(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name, "index.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func TestDiscoverScripts(t *testing.T) {
	t.Run("yields nothing for a missing directory", func(t *testing.T) {
		tools, diagnostics := DiscoverScripts(
			DiscoverOptions{Dir: filepath.Join(t.TempDir(), "missing")},
		)
		require.Empty(t, tools)
		require.Empty(t, diagnostics)
	})

	t.Run("loads valid tools in sorted order", func(t *testing.T) {
		dir := t.TempDir()
		writeDiscoveredTool(t, dir, "beta", probeModule)
		writeDiscoveredTool(t, dir, "alpha", probeModule)
		tools, diagnostics := DiscoverScripts(DiscoverOptions{Dir: dir, Workdir: t.TempDir()})
		require.Empty(t, diagnostics)
		require.Len(t, tools, 2)
		require.Equal(t, "alpha", tools[0].Definition().Name)
		require.Equal(t, "beta", tools[1].Definition().Name)
	})

	t.Run("skips what is not a tool", func(t *testing.T) {
		dir := t.TempDir()
		writeDiscoveredTool(t, dir, "good", probeModule)
		writeDiscoveredTool(t, dir, ".hidden", probeModule)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "lonely.js"), []byte(probeModule), 0o600),
		)
		require.NoError(t, os.Mkdir(filepath.Join(dir, "empty"), 0o750))
		tools, diagnostics := DiscoverScripts(DiscoverOptions{Dir: dir, Workdir: t.TempDir()})
		require.Len(t, tools, 1)
		require.Empty(t, diagnostics)
	})

	t.Run("reports one diagnostic per problem", func(t *testing.T) {
		dir := t.TempDir()
		writeDiscoveredTool(t, dir, "good", probeModule)
		writeDiscoveredTool(t, dir, "broken", `module.exports = {;`)
		writeDiscoveredTool(t, dir, "wrong", `module.exports = {description: "x"};`)
		writeDiscoveredTool(t, dir, "no good", probeModule)
		tools, diagnostics := DiscoverScripts(DiscoverOptions{Dir: dir, Workdir: t.TempDir()})
		require.Len(t, tools, 1)
		require.Len(t, diagnostics, 3)
		for _, diagnostic := range diagnostics {
			require.Contains(t, diagnostic, "tool \"")
		}
	})

	t.Run("never fails on an unreadable directory", func(t *testing.T) {
		tools, diagnostics := DiscoverScripts(
			DiscoverOptions{Dir: filepath.Join(t.TempDir(), "missing", "deep")},
		)
		require.Empty(t, tools)
		require.Empty(t, diagnostics)
	})
}
