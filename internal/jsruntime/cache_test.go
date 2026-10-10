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

// cacheModule exercises the shared cache: it sets, gets back, asks for a
// missing entry and reports how long the entry it stored lasts, which is
// everything ctx.cache does.
const cacheModule = `
module.exports = function (ctx) {
  var expires = ctx.cache.set("models.dev", "hello", 60);
  return {
    get: ctx.cache.get("models.dev"),
    has: ctx.cache.has("models.dev"),
    expires: expires,
    missing: ctx.cache.get("nothing"),
    hasMissing: ctx.cache.has("nothing"),
  };
};
`

// TestCacheRoundTrip verifies the shared cache contract: a set stores the
// {expires_at, payload} envelope on disk and answers the getters, a missing
// name reads null and reports false.
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
				raw, callErr := exports.Self().Call(context.Background(), rt.ContextValue())
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

	// The getters answer what the set stored.
	require.Equal(t, true, result["has"])
	require.Equal(t, "hello", result["get"])
	require.Nil(t, result["missing"])
	require.Equal(t, false, result["hasMissing"])

	// The expiry is one minute out from the moment the entry was stored.
	stamped, ok := result["expires"].(string)
	require.True(t, ok, "the set returned the expiry it stored: %v", result["expires"])
	at, err := time.Parse(time.RFC3339, stamped)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(time.Minute), at, 5*time.Second)

	// The storage: one file of the cache directory per name, holding the
	// raw payload untouched.
	path := filepath.Join(dir, "models.dev.json")
	file, readErr := os.ReadFile(path) //nolint:gosec // the test stored the path itself.
	require.NoError(t, readErr)
	var entry struct {
		ExpiresAt string `json:"expires_at"`
		Payload   string `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(file, &entry))
	require.Equal(t, "hello", entry.Payload)
	require.Equal(t, stamped, entry.ExpiresAt)
}

// TestCacheExpiry verifies the time to live is the runtime's business: an
// expired entry answers as if it never existed, and it is gone from the
// store afterwards.
func TestCacheExpiry(t *testing.T) {
	dir := t.TempDir()
	seed := func(t *testing.T, expired bool) {
		t.Helper()
		entry := struct {
			ExpiresAt string `json:"expires_at"`
			Payload   string `json:"payload"`
		}{Payload: "stale"}
		if expired {
			entry.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		} else {
			entry.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		}
		encoded, err := json.Marshal(entry)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "stale.json"), encoded, 0o600))
	}

	// A live seed answers whole: the expires_at of the future keeps the
	// payload fresh.
	seed(t, false)
	require.Equal(t, "stale", runCacheGet(t, dir, "stale"))

	// An expired seed is nothing: get answers null, has answers false, and
	// the entry is removed so the next write starts clean.
	seed(t, true)
	require.Nil(t, runCacheGet(t, dir, "stale"))
	_, statErr := os.Stat(filepath.Join(dir, "stale.json"))
	require.True(t, os.IsNotExist(statErr), "the expired entry is gone from the store")
}

// runCacheGet runs one get through the cache module and returns what it
// answered.
func runCacheGet(t *testing.T, dir, name string) any {
	t.Helper()
	module, err := CompileSource(
		"cache-get",
		`module.exports = function (ctx) { return ctx.cache.get("`+name+`"); };`,
	)
	require.NoError(t, err)
	var answered any
	require.NoError(
		t,
		module.Invoke(
			context.Background(),
			Options{CacheDir: dir},
			nil,
			func(rt *Runtime, exports *Exports) error {
				raw, callErr := exports.Self().Call(context.Background(), rt.ContextValue())
				if callErr != nil {
					return callErr
				}
				if raw.Undefined() {
					return nil
				}
				plain, ok := raw.JSON()
				if ok {
					answered = plain
				}
				return nil
			},
		),
	)
	return answered
}

// TestCacheConfinement verifies the names the cache accepts: a name that
// would travel out of the cache directory is refused, nothing is written,
// and the primitive missing from an unconfigured run is gone entirely.
func TestCacheConfinement(t *testing.T) {
	// A name with a path in it travels nowhere.
	dir := t.TempDir()
	module, err := CompileSource(
		"bad",
		`module.exports = function (ctx) { ctx.cache.set("../escape", "x", 60); };`,
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

	// A cache without a time to live is refused: the whole point of the
	// cache is that its entries age out.
	module, err = CompileSource(
		"ttlless",
		`module.exports = function (ctx) { return ctx.cache.set("x", "y"); };`,
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
	require.Contains(t, err.Error(), "ttl_seconds is required")

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

// storeModule exercises the permanent store: it sets, gets back, has, dels
// and reports what a delete did to the entry, which is everything ctx.store
// does.
const storeModule = `
module.exports = function (ctx) {
  ctx.store.set("note", "remember me");
  var had = ctx.store.has("note");
  var read = ctx.store.get("note");
  var absent = ctx.store.del("absent") === undefined;
  return { had: had, read: read, absent: absent };
};
`

// TestStoreStoresForever verifies the permanent store: a set survives with no
// time to live, a del removes it, and deleting an absent entry is a no-op.
func TestStoreStoresForever(t *testing.T) {
	dir := t.TempDir()
	module, err := CompileSource("store", storeModule)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(
		t,
		module.Invoke(
			context.Background(),
			Options{StoreDir: dir},
			nil,
			func(rt *Runtime, exports *Exports) error {
				raw, callErr := exports.Self().Call(context.Background(), rt.ContextValue())
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

	require.Equal(t, true, result["had"])
	require.Equal(t, "remember me", result["read"])
	require.Equal(t, true, result["absent"], "deleting an absent entry is a no-op")

	// The file sits in its own directory, shaped like a cache entry without
	// an expiry.
	path := filepath.Join(dir, "note.json")
	file, readErr := os.ReadFile(path) //nolint:gosec // the concentric stores share the envelope.
	require.NoError(t, readErr)
	var entry struct {
		ExpiresAt string `json:"expires_at"`
		Payload   string `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(file, &entry))
	require.Empty(t, entry.ExpiresAt, "a store entry never expires")
	require.Equal(t, "remember me", entry.Payload)
}

// TestStoreRejectsATimeToLive verifies the split: the store has no expiry,
// so a ttl_seconds argument is refused instead of silently ignored.
func TestStoreRejectsATimeToLive(t *testing.T) {
	dir := t.TempDir()
	module, err := CompileSource(
		"store-ttl",
		`module.exports = function (ctx) { return ctx.store.set("x", "y", 60); };`,
	)
	require.NoError(t, err)
	err = module.Invoke(
		context.Background(),
		Options{StoreDir: dir},
		nil,
		func(rt *Runtime, exports *Exports) error {
			_, callErr := exports.Self().Call(context.Background(), rt.ContextValue())
			return callErr
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ttl_seconds must be omitted")
}
