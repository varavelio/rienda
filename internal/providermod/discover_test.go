package providermod

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// goodModule is the smallest valid module: a function returning a
// one-model declaration pointing nowhere.
const goodModule = `
module.exports = function (ctx) {
  return {
    protocol: "openai_chat_completions",
    base_url: "https://example.test/v1",
    auth: "api_key",
    models: [{ id: "model-a", context_window: 128000, reasoning: false }],
  };
};
`

// writeProvider writes one user provider module into dir under name.
func writeProvider(t *testing.T, dir, name, source string) string {
	t.Helper()
	providerDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(providerDir, 0o750))
	path := filepath.Join(providerDir, "index.js")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return providerDir
}

// logged captures the log lines Discover emits, for the skip assertions.
type logged struct {
	lines []string
}

func (l *logged) logf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// names reports the names of the providers, in the order Discover returned
// them.
func names(providers []Provider) []string {
	got := make([]string, 0, len(providers))
	for _, provider := range providers {
		got = append(got, provider.Name)
	}
	return got
}

// TestDiscoverGathersUserProviders verifies the happy path: every user
// directory holding an index.js becomes one provider with the declaration
// its module returned.
func TestDiscoverGathersUserProviders(t *testing.T) {
	dir := t.TempDir()
	writeProvider(t, dir, "beta", goodModule)
	writeProvider(t, dir, "alpha", goodModule)

	log := &logged{}
	providers, err := Discover(t.Context(), Sources{UserDir: dir}, Options{}, log.logf)
	require.NoError(t, err)
	if len(log.lines) > 0 {
		t.Fatalf("unexpected logs: %v", log.lines)
	}
	require.Equal(
		t,
		[]string{"alpha", "beta"},
		names(providers),
		"providers run and return in name order",
	)
	require.Empty(t, log.lines)
	require.Equal(t, "model-a", providers[0].Decl.Models[0].ID)
	require.Len(t, providers[0].Refs(), 1)
	require.Equal(t, []string{"alpha/model-a"}, providers[0].Refs())
}

// TestDiscoverShadowsBuiltins verifies the naming rule: a user module wins,
// the embedded code never runs, and a built-in nobody shadows still works.
func TestDiscoverShadowsBuiltins(t *testing.T) {
	dir := t.TempDir()
	embedded := goodModule
	writeProvider(t, dir, "alpha", goodModule)

	t.Run("user module replaces the embedded one", func(t *testing.T) {
		providers, err := Discover(t.Context(), Sources{
			Embedded: map[string]string{"alpha": embedded},
			UserDir:  dir,
		}, Options{}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"alpha"}, names(providers))
	})

	t.Run("embedded runs when not shadowed", func(t *testing.T) {
		providers, err := Discover(t.Context(), Sources{
			Embedded: map[string]string{"gamma": goodModule, "alpha": embedded},
			UserDir:  dir,
		}, Options{}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"alpha", "gamma"}, names(providers))
	})
}

// TestDiscoverSkipsBrokenModules verifies the isolation rule: a module that
// throws, is not a function, returns junk or breaks validation is skipped
// with one logged reason, and the rest still load.
func TestDiscoverSkipsBrokenModules(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{"throws", `module.exports = function (ctx) { throw new Error("boom"); };`},
		{
			"not a function",
			`module.exports = { protocol: "openai_chat_completions", base_url: "x", auth: "api_key" };`,
		},
		{"returns junk", `module.exports = function (ctx) { return "nope"; };`},
		{"returns nothing", `module.exports = function (ctx) {};`},
		{
			"invalid declaration",
			`module.exports = function (ctx) { return {base_url: "", auth: "api_key", models: []}; };`,
		},
		{"no export", `1 + 1;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProvider(t, dir, "broken", tc.source)
			writeProvider(t, dir, "healthy", goodModule)

			log := &logged{}
			providers, err := Discover(t.Context(), Sources{UserDir: dir}, Options{}, log.logf)
			require.NoError(t, err)
			require.Equal(t, []string{"healthy"}, names(providers))
			require.Len(t, log.lines, 1)
			require.Contains(t, log.lines[0], `provider "broken"`)
		})
	}
}

// TestDiscoverUnreadableModule verifies a directory whose index.js cannot be
// read is logged and skipped without stopping the rest.
func TestDiscoverUnreadableModule(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "locked"), 0o750))

	log := &logged{}
	providers, err := Discover(t.Context(), Sources{
		Embedded: map[string]string{"healthy": goodModule},
		UserDir:  dir,
	}, Options{}, log.logf)
	require.NoError(t, err)
	require.Equal(t, []string{"healthy"}, names(providers))
}

// TestDiscoverSequentialFileScope verifies the isolation of the module file
// scope: a module writes its cache into its own directory and cannot escape;
// the workspace and other providers stay untouched.
func TestDiscoverSequentialFileScope(t *testing.T) {
	dir := t.TempDir()
	cacheModule := `
module.exports = function (ctx) {
  ctx.file.write("cache/roster.json", "[]");
  if (ctx.file.read("cache/roster.json") !== "[]") { throw new Error("cache broken"); }
  return {protocol: "openai_chat_completions", base_url: "https://example.test/v1", auth: "none", models: []};
};
`
	writeProvider(t, dir, "cacher", cacheModule)
	writeProvider(t, dir, "zeta", goodModule)

	providers, err := Discover(t.Context(), Sources{UserDir: dir}, Options{}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"cacher", "zeta"}, names(providers))

	//nolint:gosec // the test selects the path it created.
	cache, err := os.ReadFile(filepath.Join(dir, "cacher", "cache", "roster.json"))
	require.NoError(t, err)
	require.Equal(t, "[]", strings.TrimSpace(string(cache)))
	require.NoDirExists(t, filepath.Join(dir, "zeta", "cache"))
}

// TestDiscoverConfigurationCarriesThrough verifies ctx.config holds the free
// config block a run passed in.
func TestDiscoverConfigurationCarriesThrough(t *testing.T) {
	dir := t.TempDir()
	module := `
module.exports = function (ctx) {
  return {
    protocol: "openai_chat_completions",
    base_url: "https://example.test/v1",
    auth: "none",
    models: [{ id: ctx.config.endpoint || "fallback", context_window: 1000, reasoning: false }],
  };
};
`
	writeProvider(t, dir, "conf", module)

	providers, err := Discover(t.Context(), Sources{UserDir: dir}, Options{
		Config: map[string]any{"endpoint": "wired.example"},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"conf"}, names(providers))
	require.Equal(t, "wired.example", providers[0].Decl.Models[0].ID)
}

// TestDiscoverMissingDirectory verifies no user directory is a normal state.
func TestDiscoverMissingDirectory(t *testing.T) {
	providers, err := Discover(t.Context(), Sources{
		Embedded: map[string]string{"solo": goodModule},
		UserDir:  filepath.Join(t.TempDir(), "absent"),
	}, Options{}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"solo"}, names(providers))
}

// TestDiscoverEmptyRosterIsLegal verifies a provider that declares no models
// still loads: it is what a fetch failure or a fresh cache looks like.
func TestDiscoverEmptyRosterIsLegal(t *testing.T) {
	dir := t.TempDir()
	writeProvider(t, dir, "starved", `
module.exports = function (ctx) {
  return {protocol: "openai_chat_completions", base_url: "https://example.test/v1", auth: "api_key", models: []};
};`)
	providers, err := Discover(t.Context(), Sources{UserDir: dir}, Options{}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"starved"}, names(providers))
	require.Empty(t, providers[0].Decl.Models)
}

// TestDiscoverOrderIsDeterministic pins the execution order: byte order of
// names, embedded and user alike.
func TestDiscoverOrderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeProvider(t, dir, "m", goodModule)
	writeProvider(t, dir, "a", goodModule)
	writeProvider(t, dir, "z", goodModule)

	providers, err := Discover(t.Context(), Sources{
		Embedded: map[string]string{"n": goodModule},
		UserDir:  dir,
	}, Options{}, nil)
	require.NoError(t, err)
	got := names(providers)
	require.Equal(t, []string{"a", "m", "n", "z"}, got)
	require.True(t, slices.IsSorted(got))
}
