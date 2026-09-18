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

// TestRunHonorsConfigEnvironment verifies that RIENDA_CONFIG selects the
// configuration file of a run.
func TestRunHonorsConfigEnvironment(t *testing.T) {
	app := newApp(t, harness.Text("hello"))
	path := app.WriteConfig(t, t.TempDir(), harness.Config{Providers: []harness.Provider{
		chatProvider(
			harness.FakeProviderName,
			harness.Model{Alias: harness.DefaultModelAlias, ID: "env-model"},
		),
	}})

	result := app.RunEnv(t, []string{"RIENDA_CONFIG=" + path}, "run", "-a", "coder", "-p", "hi")

	result.RequireSuccess(t)
	require.Equal(t, "env-model", app.Provider().LastRequest(t).Chat(t).Model)
}

// TestRunPrefersTheConfigFlag verifies that --config wins over RIENDA_CONFIG.
func TestRunPrefersTheConfigFlag(t *testing.T) {
	app := newApp(t, harness.Text("hello"))
	dir := t.TempDir()
	path := app.WriteConfig(t, dir, harness.Config{Providers: []harness.Provider{
		chatProvider(
			harness.FakeProviderName,
			harness.Model{Alias: harness.DefaultModelAlias, ID: "flag-model"},
		),
	}})

	result := app.RunEnv(
		t,
		[]string{"RIENDA_CONFIG=" + filepath.Join(dir, "missing.yaml")},
		"run", "-a", "coder", "-p", "hi", "--config", path,
	)

	result.RequireSuccess(t)
	require.Equal(t, "flag-model", app.Provider().LastRequest(t).Chat(t).Model)
}

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
					{Alias: harness.DefaultModelAlias, ID: harness.DefaultModelID},
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

// TestRunInheritsThePresetSessionHeader verifies that a provider that declares
// a preset sends the session header the preset contributes, and that a custom
// endpoint sends none until it declares one.
func TestRunInheritsThePresetSessionHeader(t *testing.T) {
	t.Run("from the preset", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Script: []harness.Turn{harness.Text("hello")},
			Agents: []harness.Agent{coderAgent()},
			Config: &harness.Config{Providers: []harness.Provider{
				{
					Name:   harness.FakeProviderName,
					Preset: "opencode-go",
					APIKey: harness.TestAPIKey,
					Models: []harness.Model{
						{Alias: harness.DefaultModelAlias, ID: harness.DefaultModelID},
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
	primary := chatProvider("primary", harness.Model{Alias: "fast", ID: "primary-model"})
	primary.APIKey = "primary-key"
	secondary := chatProvider("secondary", harness.Model{Alias: "smart", ID: "secondary-model"})
	secondary.APIKey = "secondary-key"

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{modelFor("secondary/smart")},
		Config: &harness.Config{Providers: []harness.Provider{primary, secondary}},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
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
					{Alias: harness.DefaultModelAlias, ID: harness.DefaultModelID},
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
		Alias:         harness.DefaultModelAlias,
		ID:            "wire-model",
		MaxTokens:     1234,
		Temperature:   new(0.3),
		TopP:          new(0.8),
		ThinkingLevel: "low",
	}

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent()},
		Config: &harness.Config{Providers: []harness.Provider{
			chatProvider(harness.FakeProviderName, model),
		}},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
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
	t.Run("without a configuration file", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Agents:         []harness.Agent{coderAgent()},
			SkipConfigFile: true,
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "config.yaml")
		require.Contains(t, result.Stderr, "does not exist")
	})

	t.Run("with a missing configuration file", func(t *testing.T) {
		app := newApp(t)
		path := filepath.Join(t.TempDir(), "missing.yaml")

		result := app.Run(t, "run", "-a", "coder", "-p", "hi", "--config", path)

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "missing.yaml")
	})

	t.Run("with an unknown provider", func(t *testing.T) {
		app := harness.New(t, harness.Options{Agents: []harness.Agent{modelFor("ghost/model")}})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `unknown provider "ghost"`)
	})

	t.Run("with an unknown model", func(t *testing.T) {
		app := harness.New(t, harness.Options{Agents: []harness.Agent{modelFor("fake/ghost")}})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `unknown model "ghost"`)
	})

	t.Run("without a connection", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Agents: []harness.Agent{coderAgent()},
			Config: &harness.Config{Providers: []harness.Provider{{
				Name:   harness.FakeProviderName,
				Models: []harness.Model{{Alias: harness.DefaultModelAlias}},
			}}},
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "preset or protocol is required")
	})

	t.Run("with a connection that declares both a preset and a protocol", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Agents: []harness.Agent{coderAgent()},
			Config: &harness.Config{Providers: []harness.Provider{{
				Name:     harness.FakeProviderName,
				Preset:   "openai",
				Protocol: harness.ProtocolChat,
				Models:   []harness.Model{{Alias: harness.DefaultModelAlias}},
			}}},
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, "preset and protocol are mutually exclusive")
	})

	t.Run("with an unknown preset", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Agents: []harness.Agent{coderAgent()},
			Config: &harness.Config{Providers: []harness.Provider{{
				Name:   harness.FakeProviderName,
				Preset: "ghost",
				Models: []harness.Model{{Alias: harness.DefaultModelAlias}},
			}}},
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `unknown preset "ghost"`)
	})

	t.Run("with an unknown protocol", func(t *testing.T) {
		app := harness.New(t, harness.Options{
			Agents: []harness.Agent{coderAgent()},
			Config: &harness.Config{Providers: []harness.Provider{{
				Name:     harness.FakeProviderName,
				Protocol: "gemini",
				Models:   []harness.Model{{Alias: harness.DefaultModelAlias}},
			}}},
		})

		result := app.Run(t, "run", "-a", "coder", "-p", "hi")

		require.Equal(t, 1, result.Code)
		require.Contains(t, result.Stderr, `unknown protocol "gemini"`)
	})
}
