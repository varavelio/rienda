//go:build e2e

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// catalogEnvVar names the environment variable that points the model catalog
// of the binary at the fake endpoint of the instance, so no test in the suite
// reaches models.dev.
const catalogEnvVar = "RIENDA_CATALOG_URL"

// fakeCatalog is a fake models.dev endpoint serving the context windows the
// instances resolve, together with a fresh cache written where the binary
// reads it. Serving the endpoint keeps every refresh local, and the cache
// written up front makes the first run deterministic: the refresh loop is a
// background best effort that a short-lived invocation may not wait for.
type fakeCatalog struct {
	server *httptest.Server
}

// newFakeCatalog starts the fake catalog of one test, writes the fresh cache
// of the instance and registers the shutdown of the server.
func newFakeCatalog(t *testing.T, home string, models map[string]int) *fakeCatalog {
	t.Helper()

	catalog := &fakeCatalog{}
	catalog.server = httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, catalogPayload(models))
		}),
	)
	t.Cleanup(catalog.server.Close)

	catalog.writeCache(t, home, models)
	return catalog
}

// URL returns the endpoint the binary is pointed at through the environment.
func (c *fakeCatalog) URL() string {
	return c.server.URL + "/models.json"
}

// writeCache stores the reduced cache the binary reads, fresh so its first run
// resolves the window without waiting for the background refresh.
func (c *fakeCatalog) writeCache(t *testing.T, home string, models map[string]int) {
	t.Helper()

	reduced := make(map[string]int, len(models))
	for id, context := range models {
		reduced[lastSegment(id)] = context
	}

	document := map[string]any{
		"refreshedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"models":      reduced,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("harness: encode catalog cache: %v", err)
	}

	path := filepath.Join(home, riendaDirName, "cache", "models.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("harness: create catalog cache directory: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("harness: write catalog cache: %v", err)
	}
}

// catalogPayload renders a models.dev document carrying the context window of
// every model of the instance.
func catalogPayload(models map[string]int) string {
	var payload strings.Builder
	payload.WriteString("{")
	first := true
	for id, context := range models {
		if !first {
			payload.WriteString(",")
		}
		first = false
		payload.WriteString(`"` + id + `":{"limit":{"context":`)
		payload.WriteString(strconv.Itoa(context))
		payload.WriteString("}}")
	}
	payload.WriteString("}")
	return payload.String()
}

// lastSegment returns the part of a model identifier that follows its last
// slash, which is the key the reduced cache uses. The suite reproduces the
// contract as an external reader instead of importing the application package.
func lastSegment(modelID string) string {
	if index := strings.LastIndex(modelID, "/"); index >= 0 {
		return modelID[index+1:]
	}
	return modelID
}
