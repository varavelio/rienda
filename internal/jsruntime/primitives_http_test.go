package jsruntime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// fetchScript builds a fetch call against the test server.
func fetchScript(serverURL, target, opts string) string {
	call := `ctx.http.fetch("` + serverURL + target + `"`
	if opts != "" {
		call += ", " + opts
	}
	return call + `)`
}

func TestHttpFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("X-Multi", "a")
			w.Header().Add("X-Multi", "b")
			_, _ = w.Write([]byte("hello"))
		case "/missing":
			http.NotFound(w, r)
		case "/echo":
			body, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || r.Header.Get("X-A") != "b" || string(body) != "hi" {
				http.Error(w, "want POST with X-A and hi", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"method":"POST"}`))
		case "/slow":
			<-r.Context().Done()
		}
	}))
	t.Cleanup(server.Close)

	t.Run("returns status headers body", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, fetchScript(server.URL, "/ok", ""))
		require.NoError(t, err)
		doc := requireObject(t, got)
		require.Equal(t, float64(200), doc["status"])
		require.Equal(t, "a, b", requireString(t, requireObject(t, doc["headers"]), "x-multi"))
		require.Equal(t, "hello", requireString(t, doc, "body"))
	})

	t.Run("returns errors as payload", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, fetchScript(server.URL, "/missing", ""))
		require.NoError(t, err)
		require.Equal(t, float64(404), requireObject(t, got)["status"])
	})

	t.Run("sends method headers body", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(
			t,
			ctx,
			opts,
			fetchScript(server.URL, "/echo", `{method: "post", headers: {"X-A": "b"}, body: "hi"}`),
		)
		require.NoError(t, err)
		doc := requireObject(t, got)
		require.Equal(t, float64(200), doc["status"])
		require.Contains(t, requireString(t, doc, "body"), "POST")
	})

	t.Run("rejects bad input", func(t *testing.T) {
		ctx, opts := runCtx(t)
		for _, body := range []string{
			`ctx.http.fetch("relative/path")`,
			`ctx.http.fetch("http://[::1")`,
			fetchScript(server.URL, "/ok", "{body: 42}"),
			fetchScript(server.URL, "/ok", "{headers: {X: 1}}"),
			fetchScript(server.URL, "/ok", "{timeout_ms: -1}"),
		} {
			_, err := invokeWithContext(t, ctx, opts, body)
			require.Error(t, err, body)
		}
	})

	t.Run("transport failure raises", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.http.fetch("http://127.0.0.1:1/nope")`)
		require.Error(t, err)
	})

	t.Run("timeout and cancel raise", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(
			t,
			ctx,
			opts,
			fetchScript(server.URL, "/slow", "{timeout_ms: 50}"),
		)
		require.Error(t, err)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = invokeWithContext(t, canceled, opts, fetchScript(server.URL, "/ok", ""))
		require.Error(t, err)
	})

	t.Run("caps a large body", func(t *testing.T) {
		big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chunk := make([]byte, 32<<10)
			for i := range chunk {
				chunk[i] = 'a'
			}
			for range 16 {
				_, _ = w.Write(chunk)
			}
		}))
		t.Cleanup(big.Close)
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, fetchScript(big.URL, "", ""))
		require.NoError(t, err)
		require.LessOrEqual(t, len(requireString(t, requireObject(t, got), "body")), 256<<10)
	})
}
