//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// chatProvider returns a declaration of a provider that speaks the Chat
// Completions protocol against the fake provider of the instance.
func chatProvider(name string, models ...harness.Model) harness.Provider {
	return harness.Provider{
		Name:     name,
		Protocol: harness.ProtocolChat,
		APIKey:   harness.TestAPIKey,
		Models:   models,
	}
}

// TestRunAppliesModelDefaults verifies that every generation setting declared
// for a model reaches the wire. The former environment/flag configuration
// tests live here too: the providers moved into the modules, so a test that
// needs a second roster writes it under its own providers directory and
// points RIENDA_PROVIDERS at it.

// TestRunSendsTheDeclaredHeaders verifies that the static headers of a provider
// travel with every request and that the session header carries the identifier
// of the session the run opened.
func TestRunSendsTheDeclaredHeaders(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{
			{
				Name:          harness.FakeProviderName,
				Protocol:      harness.ProtocolChat,
				APIKey:        harness.TestAPIKey,
				Headers:       map[string]string{"x-rienda-test": "static"},
				SessionHeader: new("x-session-id"),
				Models: []harness.Model{
					{ID: harness.DefaultModelID},
				},
			},
		}},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
	result.RequireSuccess(t)

	request := app.Provider().LastRequest(t)
	require.Equal(t, "static", request.Header.Get("x-rienda-test"))
	require.Equal(t, result.SessionID(t), request.Header.Get("x-session-id"))
}

// TestRunInheritsTheSessionHeader verifies that a provider that declares a
// session header sends it, and that a provider declaring none sends none.
func TestRunInheritsTheSessionHeader(t *testing.T) {
	t.Run("from the declaration", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Script: []harness.Turn{harness.Text("hello")},
			Agents: []harness.Agent{coderAgent()},
			Config: &harness.Config{Providers: []harness.Provider{
				{
					Name:          harness.FakeProviderName,
					Protocol:      harness.ProtocolChat,
					APIKey:        harness.TestAPIKey,
					SessionHeader: new("x-opencode-session"),
					Models: []harness.Model{
						{ID: harness.DefaultModelID},
					},
				},
			}},
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")
		result.RequireSuccess(t)

		require.Equal(
			t,
			result.SessionID(t),
			app.Provider().LastRequest(t).Header.Get("x-opencode-session"),
		)
	})

	t.Run("from a custom endpoint", func(t *testing.T) {
		app := newApp(t, harness.Text("hello"))

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")
		result.RequireSuccess(t)

		require.Empty(t, app.Provider().LastRequest(t).Header.Get("x-opencode-session"))
	})
}

// TestRunUsesTheProviderOfTheAgentModel verifies that an installation
// declaring several providers sends each run to the connection of the model its
// agent uses.
func TestRunUsesTheProviderOfTheAgentModel(t *testing.T) {
	primary := chatProvider("primary", harness.Model{ID: "primary-model"})
	primary.APIKey = "primary-key"
	secondary := chatProvider("secondary", harness.Model{ID: "secondary-model"})
	secondary.APIKey = "secondary-key"

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{primary, secondary}},
	})

	// Agents no longer name models; the run names the one it wants.
	result := app.Run(t, "run", "-a", "coder", "-m", "secondary/secondary-model", "-p", "hi")
	result.RequireSuccess(t)

	request := app.Provider().LastRequest(t)
	require.Equal(t, "Bearer secondary-key", request.Header.Get("Authorization"))
	require.Equal(t, "secondary-model", request.Chat(t).Model)
}

// TestRunDisablesTheSessionHeader verifies that a provider declaring an empty
// session header sends none.
func TestRunDisablesTheSessionHeader(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{
			{
				Name:          harness.FakeProviderName,
				Protocol:      harness.ProtocolChat,
				APIKey:        harness.TestAPIKey,
				SessionHeader: new(""),
				Models: []harness.Model{
					{ID: harness.DefaultModelID},
				},
			},
		}},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
	result.RequireSuccess(t)

	require.Empty(t, app.Provider().LastRequest(t).Header.Get("x-session-id"))
}

// TestRunAppliesModelDefaults verifies that every generation setting declared
// for a model reaches the wire.
func TestRunAppliesModelDefaults(t *testing.T) {
	model := harness.Model{
		ID:          "wire-model",
		MaxTokens:   1234,
		Temperature: new(0.3),
		TopP:        new(0.8),
		Thinking:    []harness.Thinking{{Level: "low", MaxTokens: 4096}},
	}

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{
			chatProvider(harness.FakeProviderName, model),
		}},
	})

	// Thinking is a branch selection: a run that names a mode sends it.
	result := app.Run(t, "run", "-a", "coder", "--thinking", "low", "-p", "hi")
	result.RequireSuccess(t)

	chat := app.Provider().LastRequest(t).Chat(t)
	require.Equal(t, "wire-model", chat.Model)
	require.Equal(t, 1234, chat.MaxCompletionTokens)
	require.Equal(t, 0.3, *chat.Temperature)
	require.Equal(t, 0.8, *chat.TopP)
	require.Equal(t, "low", chat.ReasoningEffort)
}

// TestRunReportsConfigurationFailures verifies that every way of configuring an
// unusable instance fails the run with a message that points at the cause.
func TestRunReportsConfigurationFailures(t *testing.T) {
	t.Run("with no providers at all", func(t *testing.T) {
		// The installation owns its directory and shadows the built-in with
		// an empty roster, exactly as a user module would.
		providerDir := filepath.Join(t.TempDir(), "providers")
		stub := "module.exports = function (ctx) { return {protocol: 'openai_chat_completions', base_url: 'https://example.invalid', auth: 'none', models: []}; };"
		writeTestModule(t, filepath.Join(providerDir, "opencode-go"), stub)
		writeTestModule(t, filepath.Join(providerDir, "z-ai"), stub)
		app := harness.New(t, harness.Options{
			Agents:         []harness.Agent{coderAgent()},
			SkipConfigFile: true,
			ProvidersDir:   providerDir,
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "no provider offers models")
	})

	t.Run("with a model reference no provider holds", func(t *testing.T) {
		app := newApp(t)

		result := app.Run(t, "run", "-a", "coder", "-m", "ghost/nope", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `model "ghost/nope"`)
	})

	t.Run("with a model reference the default roster does not hold", func(t *testing.T) {
		app := newApp(t)

		result := app.Run(t, "run", "-a", "coder", "-m", "fake/ghost", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `model "fake/ghost"`)
	})

	t.Run("with an old providers configuration block", func(t *testing.T) {
		// Providers moved into the modules, so the removed block refuses to
		// load and names the mechanism that replaced it.
		app := newApp(t)
		path := app.WithConfig(t, "providers:\n  fake:\n    protocol: openai_chat_completions\n")

		result := app.Run(t, "run", "-a", "coder", "-p", "hi", "--config", path)

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "providers are no longer declared here")
	})
}
