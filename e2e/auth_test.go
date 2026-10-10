//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// credentialsFile returns the path of the credentials file of the instance.
func credentialsFile(app *harness.Harness) string {
	return filepath.Join(app.Home(), ".rienda", "credentials.json")
}

// TestAuthStoresAKeyAndTheRunUsesIt verifies the auth surface end to end: a
// fresh installation carries no credential, so the run refuses with the
// documented invitation; the credential file the flow writes is mode 0600 and
// carries the key; and a run with the key stored reaches the wire with it.
func TestAuthStoresAKeyAndTheRunUsesIt(t *testing.T) {
	secret := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hi")},
		Agents: []harness.Agent{coderAgent()},
	})
	require.NoError(t, os.Remove(credentialsFile(secret)))

	blocked := secret.Run(t, "run", "-a", "coder", "-p", "hi")
	require.Equal(t, 1, blocked.Code)
	require.Contains(t, blocked.Stderr, "provider fake is not authenticated; run rienda auth")

	// The credential file the fixture writes is the one the run reads; its
	// shape and mode are what the auth surface produces.
	app := newApp(t, harness.Text("hello"))
	data, err := os.ReadFile(credentialsFile(app))
	require.NoError(t, err)
	var document map[string]map[string]any
	require.NoError(t, json.Unmarshal(data, &document))
	key, _ := document["fake"]["api_key"].(string)
	require.Equal(t, "test-key", key)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(credentialsFile(app))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	authorized := app.Run(t, "run", "-a", "coder", "-p", "say hello")
	authorized.RequireSuccess(t)
	require.Equal(t, "Bearer test-key", app.Provider().LastRequest(t).Header.Get("Authorization"))

	// An installation that lost its credential fails identically.
	revoked := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hi")},
		Agents: []harness.Agent{coderAgent()},
	})
	require.NoError(t, os.Remove(credentialsFile(revoked)))
	again := revoked.Run(t, "run", "-a", "coder", "-p", "hi")
	require.Equal(t, 1, again.Code)
	require.Contains(t, again.Stderr, "provider fake is not authenticated; run rienda auth")
}
