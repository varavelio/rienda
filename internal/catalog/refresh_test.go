package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// endpointTimeout bounds how long a test waits for the fake endpoint to
// receive a request, so a broken loop fails instead of hanging the suite.
const endpointTimeout = 5 * time.Second

// catalogPayload is a model database covering the shapes the reduction must
// handle: path-style identifiers, entries without a limit and entries without
// a context.
const catalogPayload = `{
  "moonshotai/kimi-k2.6": {"limit": {"context": 262144}},
  "openai/gpt-5": {"limit": {"context": 400000}},
  "no-context": {"limit": {}},
  "no-limit": {}
}`

// catalogServer is a scripted models.dev endpoint that records the requests it
// receives.
type catalogServer struct {
	server *httptest.Server

	mu       sync.Mutex
	requests int
	status   int
	payload  string
	arrival  chan struct{}
}

// newCatalogServer starts a fake endpoint serving payload.
func newCatalogServer(t *testing.T, payload string) *catalogServer {
	t.Helper()

	scripted := &catalogServer{payload: payload, arrival: make(chan struct{}, 16)}
	scripted.server = httptest.NewServer(http.HandlerFunc(scripted.serve))
	t.Cleanup(scripted.server.Close)
	return scripted
}

// serve answers one request with the scripted status and payload.
func (s *catalogServer) serve(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.requests++
	status, payload := s.status, s.payload
	s.mu.Unlock()

	select {
	case s.arrival <- struct{}{}:
	default:
	}

	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, payload)
}

// count returns how many requests the endpoint received.
func (s *catalogServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// fail makes the endpoint answer every request with the given status.
func (s *catalogServer) fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// wait blocks until the endpoint receives a request.
func (s *catalogServer) wait(t *testing.T) {
	t.Helper()

	select {
	case <-s.arrival:
	case <-time.After(endpointTimeout):
		t.Fatal("the catalog endpoint received no request")
	}
}

// testClock is a catalog clock whose ticks the test drives.
type testClock struct {
	now   time.Time
	ticks chan time.Time
}

// Now returns the moment the clock stands at.
func (c *testClock) Now() time.Time { return c.now }

// After returns the channel the test drives the loop with.
func (c *testClock) After(time.Duration) <-chan time.Time { return c.ticks }

// scriptedCatalog builds a catalog over a fake endpoint and a temporary cache
// directory, filled from the options the test cares about.
func scriptedCatalog(
	t *testing.T,
	endpoint *catalogServer,
	now func() time.Time,
) (*Catalog, string) {
	t.Helper()

	cat, path := testCatalog(t, Options{
		Endpoint: endpoint.server.URL + "/models.json",
		Client:   endpoint.server.Client(),
		Now:      now,
	})
	return cat, path
}

// TestRefresh verifies the atomic replacement of the cache.
func TestRefresh(t *testing.T) {
	base := time.Date(2026, time.September, 22, 3, 0, 0, 0, time.UTC)

	t.Run("writes the reduced map", func(t *testing.T) {
		endpoint := newCatalogServer(t, catalogPayload)
		cat, path := scriptedCatalog(t, endpoint, func() time.Time { return base })

		require.NoError(t, cat.Refresh(t.Context()))

		data, err := os.ReadFile(path) //nolint:gosec // a test temporary path.
		require.NoError(t, err)
		var stored cache
		require.NoError(t, json.Unmarshal(data, &stored))
		require.Equal(t, base, stored.RefreshedAt)
		require.Equal(t, map[string]model{
			"kimi-k2.6": {ContextWindow: 262144},
			"gpt-5":     {ContextWindow: 400000},
		}, stored.Models)
	})

	t.Run("keeps the previous cache when the endpoint fails", func(t *testing.T) {
		endpoint := newCatalogServer(t, catalogPayload)
		endpoint.fail(http.StatusInternalServerError)
		cat, path := scriptedCatalog(t, endpoint, func() time.Time { return base })
		writeCache(t, path, base.Add(-time.Hour), map[string]int{"previous": 123})
		before, err := os.ReadFile(path) //nolint:gosec // a test temporary path.
		require.NoError(t, err)

		err = cat.Refresh(t.Context())

		require.ErrorContains(t, err, "unexpected status")
		after, err := os.ReadFile(path) //nolint:gosec // a test temporary path.
		require.NoError(t, err)
		require.Equal(t, before, after)
	})

	t.Run("keeps the previous cache when the payload is malformed", func(t *testing.T) {
		endpoint := newCatalogServer(t, "not json")
		cat, path := scriptedCatalog(t, endpoint, func() time.Time { return base })
		writeCache(t, path, base.Add(-time.Hour), map[string]int{"previous": 123})
		before, err := os.ReadFile(path) //nolint:gosec // a test temporary path.
		require.NoError(t, err)

		err = cat.Refresh(t.Context())

		require.ErrorContains(t, err, "decode database")
		after, err := os.ReadFile(path) //nolint:gosec // a test temporary path.
		require.NoError(t, err)
		require.Equal(t, before, after)
	})

	t.Run("leaves no cache behind when the first refresh fails", func(t *testing.T) {
		endpoint := newCatalogServer(t, catalogPayload)
		endpoint.fail(http.StatusBadGateway)
		cat, path := scriptedCatalog(t, endpoint, func() time.Time { return base })

		err := cat.Refresh(t.Context())

		require.Error(t, err)
		_, statErr := os.Stat(path)
		require.ErrorIs(t, statErr, os.ErrNotExist)

		entries, readErr := os.ReadDir(filepath.Dir(path))
		require.NoError(t, readErr)
		require.Empty(t, entries, "an interrupted refresh left a temporary file behind")
	})
}

// TestReduce verifies the reduction of the model database.
func TestReduce(t *testing.T) {
	t.Run("accepts a nested models wrapper", func(t *testing.T) {
		models, err := reduce([]byte(`{"models":{"vendor/model":{"limit":{"context":12}}}}`))

		require.NoError(t, err)
		require.Equal(t, map[string]model{"model": {ContextWindow: 12}}, models)
	})

	t.Run("skips entries without a usable context", func(t *testing.T) {
		models, err := reduce(
			[]byte(`{"a":{"limit":{"context":0}},"b":3,"c":{"limit":{"context":7}}}`),
		)

		require.NoError(t, err)
		require.Equal(t, map[string]model{"c": {ContextWindow: 7}}, models)
	})

	t.Run("normalizes the keys it stores", func(t *testing.T) {
		models, err := reduce(
			[]byte(`{"MoonshotAI/Kimi-K2.6":{"limit":{"context":12}}}`),
		)

		require.NoError(t, err)
		require.Equal(t, map[string]model{"kimi-k2.6": {ContextWindow: 12}}, models)
	})

	t.Run("rejects a document that is not a map", func(t *testing.T) {
		_, err := reduce([]byte("not json"))

		require.ErrorContains(t, err, "decode database")
	})
}

// TestRun verifies the background refresh loop.
func TestRun(t *testing.T) {
	base := time.Date(2026, time.September, 22, 3, 0, 0, 0, time.UTC)

	t.Run("refreshes immediately and stops with its context", func(t *testing.T) {
		endpoint := newCatalogServer(t, catalogPayload)
		clock := &testClock{now: base, ticks: make(chan time.Time)}
		cat, _ := testCatalog(t, Options{
			Endpoint: endpoint.server.URL + "/models.json",
			Client:   endpoint.server.Client(),
			Now:      clock.Now,
			After:    clock.After,
		})

		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			cat.Run(ctx)
			close(stopped)
		}()

		endpoint.wait(t)
		require.Equal(t, 1, endpoint.count())

		cancel()
		select {
		case <-stopped:
		case <-time.After(endpointTimeout):
			t.Fatal("the refresh loop did not stop with its context")
		}
	})

	t.Run("refreshes a stale cache and skips a fresh one", func(t *testing.T) {
		endpoint := newCatalogServer(t, catalogPayload)
		clock := &testClock{now: base, ticks: make(chan time.Time)}
		cat, path := testCatalog(t, Options{
			Endpoint: endpoint.server.URL + "/models.json",
			Client:   endpoint.server.Client(),
			Now:      clock.Now,
			After:    clock.After,
		})
		writeCache(t, path, base, map[string]int{"previous": 123})

		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			cat.Run(ctx)
			close(stopped)
		}()

		// The cache is fresh, so the immediate pass fetches nothing.
		clock.ticks <- base
		require.Zero(t, endpoint.count())

		// An expired cache is refreshed on the next pass.
		writeCache(t, path, base.Add(-cacheTTL-time.Hour), map[string]int{"previous": 123})
		clock.ticks <- base
		endpoint.wait(t)
		require.Equal(t, 1, endpoint.count())

		// The loop must stop before the test returns: the refresh it may still
		// be writing would otherwise race the cleanup of the temporary
		// directory.
		cancel()
		select {
		case <-stopped:
		case <-time.After(endpointTimeout):
			t.Fatal("the refresh loop did not stop with its context")
		}
	})
}
