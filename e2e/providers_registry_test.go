//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestShadowReplacesTheBuiltin verifies the naming rule end to end: a user
// module named like the built-in replaces it, and the embedded code never
// runs, which the starved roster of the shadow proves.
func TestShadowReplacesTheBuiltin(t *testing.T) {
	app := newApp(t, harness.Text("hello"))
	module := filepath.Join(app.ProvidersDir(), "opencode-go")
	require.NoError(t, os.MkdirAll(module, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(module, "index.js"), []byte(
		"module.exports = function (ctx) { return {protocol: 'openai_chat_completions', base_url: '"+app.Provider().
			BaseURL()+
			"', auth: 'api_key', models: [{id: 'shadow-model', context_window: 100000, reasoning: false, thinking_modes: []}]}; };",
	), 0o600))

	// The shadow needs its own key, stored the way the auth surface writes it.
	require.NoError(
		t,
		os.WriteFile(
			credentialsFile(app),
			[]byte(`{"opencode-go":{"api_key":"shadow-key"}}`),
			0o600,
		),
	)
	result := app.Run(t, "run", "-a", "coder", "-m", "opencode-go/shadow-model", "-p", "hi")
	result.RequireSuccess(t)
	require.Equal(t, "shadow-model", app.Provider().LastRequest(t).Chat(t).Model)
}

// TestSkippedBrokenModule verifies the isolation rule: a module that throws is
// skipped with a logged reason, and the rest of the providers still work.
func TestSkippedBrokenModule(t *testing.T) {
	app := newApp(t, harness.Text("hello"))
	broken := filepath.Join(app.ProvidersDir(), "broken")
	require.NoError(t, os.MkdirAll(broken, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(broken, "index.js"), []byte(
		"module.exports = function (ctx) { throw new Error('the module is broken'); };",
	), 0o600))

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
	result.RequireSuccess(t)
	require.Contains(t, result.Stderr, "provider \"broken\"")
}
