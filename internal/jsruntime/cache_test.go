package jsruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// cacheModule exercises the shared cache primitive: it writes, reads back
// and reports, which is all ctx.cache does.
const cacheModule = `
module.exports = function (ctx) {
  ctx.cache.write("models.dev", { hello: "world" });
  var doc = ctx.cache.read("models.dev");
  return { date: doc.date, data: doc.data, missing: ctx.cache.read("nothing") };
};
`

// TestCacheRoundTrip verifies the shared cache contract: a write stores the
// {date, data} envelope on disk, the read returns it whole, a missing name
// reads null and a bad name is refused.
func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	module, err := CompileSource("cache", cacheModule)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(
		t,
		module.Invoke(
			context.Background(),
			Options{CacheDir: dir},
			nil,
			func(rt *Runtime, exports *Exports) error {
				main := exports.Self()
				raw, callErr := main.Call(context.Background(), rt.ContextValue())
				if callErr != nil {
					return callErr
				}
				plain, ok := raw.JSON()
				require.True(t, ok)
				data, err := json.Marshal(plain)
				require.NoError(t, err)
				return json.Unmarshal(data, &result)
			},
		),
	)

	// The envelope: a stamped document carrying the data whole.
	date, ok := result["date"].(string)
	require.True(t, ok, "the write returned the stamp it stored: %v", result["date"])
	saved, parseErr := time.Parse(time.RFC3339, date)
	require.NoError(t, parseErr)
	require.WithinDuration(t, time.Now(), saved, time.Minute)
	require.Equal(t, map[string]any{"hello": "world"}, result["data"])
	require.Nil(t, result["missing"], "a name the cache holds nothing for reads null")

	// The storage: one file of the cache directory per name.
	path := filepath.Join(dir, "models.dev.json")
	file, readErr := os.ReadFile(path) //nolint:gosec // the test stored the path itself.
	require.NoError(t, readErr)
	var doc struct {
		Date string `json:"date"`
		Data any    `json:"data"`
	}
	require.NoError(t, json.Unmarshal(file, &doc))
	require.Equal(t, result["date"], doc.Date)
}

// TestCacheConfinement verifies the names the cache accepts: a name that
// would travel out of the cache directory is refused, nothing is written,
// and the primitive missing from an unconfigured run is gone entirely.
func TestCacheConfinement(t *testing.T) {
	// A name with a path in it travels nowhere.
	dir := t.TempDir()
	module, err := CompileSource(
		"bad",
		`module.exports = function (ctx) { return ctx.cache.write("../escape", 1); };`,
	)
	require.NoError(t, err)
	err = module.Invoke(
		context.Background(),
		Options{CacheDir: dir},
		nil,
		func(rt *Runtime, exports *Exports) error {
			_, callErr := exports.Self().Call(context.Background(), rt.ContextValue())
			return callErr
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a single path segment")
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries, "the refused write stored nothing")

	// A run without a cache directory is a run without the primitive.
	module, err = CompileSource(
		"missing",
		`module.exports = function (ctx) { return ctx.cache === undefined; };`,
	)
	require.NoError(t, err)
	require.NoError(
		t,
		module.Invoke(
			context.Background(),
			Options{},
			nil,
			func(rt *Runtime, exports *Exports) error {
				_, callErr := exports.Self().Call(context.Background(), rt.ContextValue())
				return callErr
			},
		),
		"the missing cache reports undefined",
	)
}
