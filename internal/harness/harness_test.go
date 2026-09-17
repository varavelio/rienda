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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/session"
)

// receivedRequest is the subset of a chat completions request the tests
// inspect. The provider fixtures fill it from the recorded wire payload.
type receivedRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

// scriptedProvider is a minimal OpenAI Chat Completions provider that answers
// every request with the next scripted response and records what it received.
type scriptedProvider struct {
	mu       sync.Mutex
	requests []receivedRequest
	scripts  [][]string
	server   *httptest.Server
}

// newScriptedProvider starts a provider serving the scripts in order. Every
// script is the list of raw SSE JSON payloads of one response.
func newScriptedProvider(t *testing.T, scripts ...[]string) *scriptedProvider {
	t.Helper()

	provider := &scriptedProvider{scripts: scripts}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(provider.server.Close)
	return provider
}

// serve answers one chat completions request with the next script.
func (p *scriptedProvider) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request receivedRequest
	_ = json.Unmarshal(body, &request)

	p.mu.Lock()
	p.requests = append(p.requests, request)
	index := len(p.requests) - 1
	p.mu.Unlock()

	if index >= len(p.scripts) {
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	}

	var response strings.Builder
	for _, chunk := range p.scripts[index] {
		response.WriteString("data: ")
		response.WriteString(chunk)
		response.WriteString("\n\n")
	}
	response.WriteString("data: [DONE]\n\n")

	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, response.String())
}

// roleChunk opens a streamed completion, as real providers do.
const roleChunk = `{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`

// finishChunk closes a streamed completion with a wire finish reason.
func finishChunk(reason string) string {
	return `{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"` +
		reason + `"}]}`
}

// textScript builds the streaming chunks of a text answer.
func textScript(text string) []string {
	return []string{
		roleChunk,
		`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"content":` +
			strconv.Quote(text) + `}}]}`,
		finishChunk("stop"),
	}
}

// toolScript builds the streaming chunks of a shell tool call.
func toolScript(id, arguments string) []string {
	return []string{
		roleChunk,
		`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":` +
			`[{"index":0,"id":"` + id + `","type":"function",` +
			`"function":{"name":"shell","arguments":""}}]}}]}`,
		`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":` +
			`[{"index":0,"function":{"arguments":` + strconv.Quote(arguments) + `}}]}}]}`,
		finishChunk("tool_calls"),
	}
}

// testEnvironment holds the filesystem layout and the scripted provider of a
// harness test.
type testEnvironment struct {
	workdir     string
	agentsDir   string
	sessionsDir string
	configPath  string
	provider    *scriptedProvider
}

// newTestEnvironment writes a configuration and a coder agent definition
// pointing at a scripted provider.
func newTestEnvironment(t *testing.T, scripts ...[]string) *testEnvironment {
	t.Helper()

	root := t.TempDir()
	env := &testEnvironment{
		workdir:     filepath.Join(root, "work"),
		agentsDir:   filepath.Join(root, "agents"),
		sessionsDir: filepath.Join(root, "sessions"),
		configPath:  filepath.Join(root, "config.yaml"),
		provider:    newScriptedProvider(t, scripts...),
	}
	require.NoError(t, os.MkdirAll(env.workdir, 0o750))
	require.NoError(t, os.MkdirAll(env.agentsDir, 0o750))

	configuration := "providers:\n" +
		"  fake:\n" +
		"    protocol: openai_chat_completions\n" +
		"    base_url: " + env.provider.server.URL + "\n" +
		"    api_key: test-key\n" +
		"    models:\n" +
		"      test-model:\n" +
		"        id: gpt-test\n"
	require.NoError(t, os.WriteFile(env.configPath, []byte(configuration), 0o600))
	env.writeAgent(t, "coder", "fake/test-model")

	return env
}

// writeAgent writes an agent definition for model.
func (e *testEnvironment) writeAgent(t *testing.T, id, model string) {
	t.Helper()

	definition := "---\n" +
		"description: A test agent\n" +
		"model: " + model + "\n" +
		"tools: [shell]\n" +
		"---\n" +
		"You answer briefly.\n"
	require.NoError(t, os.WriteFile(e.agentPath(id), []byte(definition), 0o600))
}

// agentPath returns the path of an agent definition.
func (e *testEnvironment) agentPath(id string) string {
	return filepath.Join(e.agentsDir, id+".md")
}

// options returns the preparation options of the environment.
func (e *testEnvironment) options() Options {
	return Options{
		AgentID:     "coder",
		Workdir:     e.workdir,
		ConfigPath:  e.configPath,
		AgentsDir:   e.agentsDir,
		SessionsDir: e.sessionsDir,
	}
}

// sessionPath returns the path a session file is stored at.
func (e *testEnvironment) sessionPath(t *testing.T, sessionID string) string {
	t.Helper()

	projectID, err := session.ProjectID(e.workdir)
	require.NoError(t, err)
	return filepath.Join(e.sessionsDir, projectID, sessionID+session.Extension)
}

// prepare prepares a session for the environment and closes it on cleanup.
func (e *testEnvironment) prepare(t *testing.T) *Session {
	t.Helper()

	prepared, err := Prepare(t.Context(), e.options())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, prepared.Close()) })
	return prepared
}

// collectEvents drains a run channel.
func collectEvents(events <-chan engine.Event) []engine.Event {
	collected := make([]engine.Event, 0, 8)
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

// joinedText returns the assistant text of a run.
func joinedText(events []engine.Event) string {
	var text strings.Builder
	for _, event := range events {
		if event.Type == engine.EventTextDelta {
			text.WriteString(event.Text)
		}
	}
	return text.String()
}

// TestPrepare verifies session preparation.
func TestPrepare(t *testing.T) {
	t.Run("rejects invalid options", func(t *testing.T) {
		env := newTestEnvironment(t)
		tests := []struct {
			name    string
			mutate  func(*Options)
			wantErr string
		}{
			{
				name:    "missing agent id",
				mutate:  func(options *Options) { options.AgentID = "  " },
				wantErr: "agent id is required",
			},
			{
				name: "missing workdir",
				mutate: func(options *Options) {
					options.Workdir = filepath.Join(t.TempDir(), "missing")
				},
				wantErr: "workdir",
			},
			{
				name: "missing configuration",
				mutate: func(options *Options) {
					options.ConfigPath = filepath.Join(t.TempDir(), "missing.yaml")
				},
				wantErr: "does not exist",
			},
			{
				name:    "unknown agent",
				mutate:  func(options *Options) { options.AgentID = "ghost" },
				wantErr: "ghost.md",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				options := env.options()
				test.mutate(&options)

				_, err := Prepare(t.Context(), options)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})

	t.Run("reports unknown agent models", func(t *testing.T) {
		env := newTestEnvironment(t)
		env.writeAgent(t, "coder", "fake/missing")

		_, err := Prepare(t.Context(), env.options())

		require.ErrorContains(t, err, `unknown model "missing"`)
	})

	t.Run("prepares a session on disk", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("hello"))
		prepared := env.prepare(t)

		path := env.sessionPath(t, prepared.ID())
		_, err := os.Stat(path)
		require.NoError(t, err)

		require.Equal(t, "coder", prepared.Info().Agent)
		require.Equal(t, "fake/test-model", prepared.Info().Model)
		require.Equal(t, env.workdir, prepared.Info().Workdir)

		stored, err := session.Open(filepath.Dir(path), prepared.ID(), id.NewIDGenerator())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, stored.Close()) })

		require.Equal(t, "coder", stored.Info().Agent)
		require.Equal(t, "fake/test-model", stored.Info().Model)
		require.Equal(t, env.workdir, stored.Info().Workdir)
	})

	t.Run("defaults to the global directories", func(t *testing.T) {
		env := newTestEnvironment(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(configEnvVar, env.configPath)

		require.NoError(t, os.MkdirAll(filepath.Join(home, ".rienda", "agents"), 0o750))
		definition, err := os.ReadFile(env.agentPath("coder"))
		require.NoError(t, err)

		agentFile := filepath.Join(home, ".rienda", "agents", "coder.md")
		//nolint:gosec // the path lives in a test temporary directory.
		err = os.WriteFile(agentFile, definition, 0o600)
		require.NoError(t, err)

		prepared, err := Prepare(t.Context(), Options{AgentID: "coder", Workdir: env.workdir})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		path := filepath.Join(
			home,
			".rienda",
			"sessions",
			filepath.Base(filepath.Dir(env.sessionPath(t, prepared.ID()))),
			prepared.ID()+session.Extension,
		)
		_, err = os.Stat(path)
		require.NoError(t, err)
	})
}

// TestResolveWorkdir verifies working directory resolution.
func TestResolveWorkdir(t *testing.T) {
	t.Run("defaults to the process working directory", func(t *testing.T) {
		cwd, err := os.Getwd()
		require.NoError(t, err)

		dir, err := resolveWorkdir("")

		require.NoError(t, err)
		require.Equal(t, cwd, dir)
	})

	t.Run("resolves relative paths", func(t *testing.T) {
		cwd, err := os.Getwd()
		require.NoError(t, err)

		dir, err := resolveWorkdir(".")

		require.NoError(t, err)
		require.Equal(t, cwd, dir)
	})

	t.Run("rejects paths that are not directories", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("data"), 0o600))

		_, err := resolveWorkdir(file)

		require.ErrorContains(t, err, "not a directory")
	})
}

// TestResolveConfigPath verifies configuration path resolution.
func TestResolveConfigPath(t *testing.T) {
	t.Run("prefers the explicit path", func(t *testing.T) {
		t.Setenv(configEnvVar, filepath.Join(t.TempDir(), "env.yaml"))

		path, err := resolveConfigPath("  /explicit.yaml  ")

		require.NoError(t, err)
		require.Equal(t, "/explicit.yaml", path)
	})

	t.Run("falls back to the environment variable", func(t *testing.T) {
		envPath := filepath.Join(t.TempDir(), "env.yaml")
		t.Setenv(configEnvVar, envPath)

		path, err := resolveConfigPath("")

		require.NoError(t, err)
		require.Equal(t, envPath, path)
	})

	t.Run("falls back to the default path", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(configEnvVar, "")

		path, err := resolveConfigPath("")

		require.NoError(t, err)
		require.Equal(t, filepath.Join(home, ".rienda", "config.yaml"), path)
	})
}

// TestRun verifies runs through the full stack.
func TestRun(t *testing.T) {
	t.Run("answers a prompt", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("hello"))
		prepared := env.prepare(t)

		events := collectEvents(prepared.Run(t.Context(), "say hello"))

		require.Equal(t, engine.EndReasonTurn, events[len(events)-1].Reason, "events: %+v", events)
		require.Equal(t, "hello", joinedText(events))

		require.Len(t, env.provider.requests, 1)
		request := env.provider.requests[0]
		require.Equal(t, "gpt-test", request.Model)
		require.Len(t, request.Messages, 2)
		require.Equal(t, "system", request.Messages[0].Role)
		require.Equal(t, "You answer briefly.", request.Messages[0].Content)
		require.Equal(t, "user", request.Messages[1].Role)
		require.Equal(t, "say hello", request.Messages[1].Content)

		require.Len(t, request.Tools, 1)
		require.Equal(t, "shell", request.Tools[0].Function.Name)
	})

	t.Run("runs tools and continues the conversation", func(t *testing.T) {
		env := newTestEnvironment(t,
			toolScript("call_1", `{"command":"echo harness"}`),
			textScript("done"),
		)
		prepared := env.prepare(t)

		var output, text strings.Builder
		for event := range prepared.Run(t.Context(), "run it") {
			switch event.Type {
			case engine.EventToolOutput:
				output.WriteString(event.Output)
			case engine.EventTextDelta:
				text.WriteString(event.Text)
			}
		}

		require.Equal(t, "done", text.String())
		require.Contains(t, output.String(), "harness")

		require.Len(t, env.provider.requests, 2)
		second := env.provider.requests[1].Messages
		require.Len(t, second, 4)
		require.Equal(t, "assistant", second[2].Role)
		require.Len(t, second[2].ToolCalls, 1)
		require.Equal(t, "call_1", second[2].ToolCalls[0].ID)
		require.Equal(t, "tool", second[3].Role)
		require.Equal(t, "call_1", second[3].ToolCallID)
		require.Contains(t, second[3].Content, "harness")
	})
}
