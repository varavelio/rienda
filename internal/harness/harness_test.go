package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// receivedRequest is the subset of a chat completions request the tests
// inspect. The provider fixtures fill it from the recorded wire payload.
type receivedRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
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

// serve answers one chat completions request with the next script. A
// non-streamed request, which the summarization issues, is answered with the
// complete JSON response instead of the server-sent events.
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

	if !request.Stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completeResponse(p.scripts[index]))
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

// completeResponse folds a script of streamed chunks into the complete JSON
// response a non-streamed call receives: the text fragments are joined and the
// terminal chunk contributes the finish reason and the usage.
func completeResponse(chunks []string) string {
	var text strings.Builder
	finishReason := "stop"
	var usage map[string]any

	for _, chunk := range chunks {
		var decoded struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
		}
		if err := json.Unmarshal([]byte(chunk), &decoded); err != nil {
			continue
		}
		for _, choice := range decoded.Choices {
			text.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
		if decoded.Usage != nil {
			usage = decoded.Usage
		}
	}

	response := map[string]any{
		"id":    "chatcmpl_complete",
		"model": "gpt-test",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text.String()},
			"finish_reason": finishReason,
		}},
	}
	if usage != nil {
		response["usage"] = usage
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return `{"error":{"message":"encode complete response"}}`
	}
	return string(encoded)
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

	// extraModels lists the models the configuration declares besides the
	// default one, so a test can switch the model of a session.
	extraModels map[string]string
}

// newTestEnvironment writes a configuration and a coder agent definition
// pointing at a scripted provider.
func newTestEnvironment(t *testing.T, scripts ...[]string) *testEnvironment {
	t.Helper()

	root := t.TempDir()
	// The catalog and the summarization prompt are resolved from the home
	// directory, so the environment points HOME at the temporary root and no
	// test ever reads the home of the developer running it.
	t.Setenv("HOME", root)

	env := &testEnvironment{
		workdir:     filepath.Join(root, "work"),
		agentsDir:   filepath.Join(root, "agents"),
		sessionsDir: filepath.Join(root, "sessions"),
		configPath:  filepath.Join(root, "config.yaml"),
		provider:    newScriptedProvider(t, scripts...),
	}
	require.NoError(t, os.MkdirAll(env.workdir, 0o750))
	require.NoError(t, os.MkdirAll(env.agentsDir, 0o750))

	env.writeConfig(t)
	env.writeAgent(t, "coder", "fake/test-model")

	return env
}

// writeConfig writes the configuration of the environment, with the models it
// declares. Every model is an alias and the wire identifier it resolves to.
func (e *testEnvironment) writeConfig(t *testing.T) {
	t.Helper()

	configuration := "providers:\n" +
		"  fake:\n" +
		"    protocol: openai_chat_completions\n" +
		"    base_url: " + e.provider.server.URL + "\n" +
		"    api_key: test-key\n" +
		"    models:\n" +
		"      test-model:\n" +
		"        id: gpt-test\n"
	entries := make([]string, 0, len(e.extraModels))
	for alias, id := range e.extraModels {
		entries = append(entries, fmt.Sprintf("      %s:\n        id: %s\n", alias, id))
	}
	slices.Sort(entries)
	configuration += strings.Join(entries, "")
	require.NoError(t, os.WriteFile(e.configPath, []byte(configuration), 0o600))
}

// writeSecondModel adds a model to the configuration of the environment, so a
// test can switch the model of a session. It rewrites the whole configuration,
// so the models it declares stay in one place.
func (e *testEnvironment) writeSecondModel(t *testing.T, alias, id string) {
	t.Helper()

	if e.extraModels == nil {
		e.extraModels = make(map[string]string)
	}
	e.extraModels[alias] = id
	e.writeConfig(t)
}

// writeAgent writes an agent definition for model.
func (e *testEnvironment) writeAgent(t *testing.T, id, model string) {
	t.Helper()
	e.writeAgentPrompt(t, id, model, "You answer briefly.")
}

// writeAgentPrompt writes an agent definition for model whose body is prompt,
// so a test can tell the agents of a roster apart.
func (e *testEnvironment) writeAgentPrompt(t *testing.T, id, model, prompt string) {
	t.Helper()

	definition := "---\n" +
		"description: A test agent\n" +
		"model: " + model + "\n" +
		"tools: [shell]\n" +
		"---\n" +
		prompt + "\n"
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
				wantErr: `agent "ghost" is not defined`,
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

	t.Run("resumes a stored session", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"), textScript("two"))
		started := env.prepare(t)

		events := collectEvents(started.Run(t.Context(), "first"))
		require.Equal(t, "one", joinedText(events))
		require.NoError(t, started.Close())

		options := env.options()
		options.AgentID = ""
		options.SessionID = started.ID()
		resumed, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, resumed.Close()) })

		require.Equal(t, started.ID(), resumed.ID())
		require.Equal(t, "coder", resumed.Info().Agent)

		entries := resumed.Branch()
		require.Len(t, entries, 2)
		require.Equal(t, llm.RoleUser, entries[0].Message.Role)
		require.Equal(t, "first", entries[0].Message.Blocks[0].Text)
		require.Equal(t, llm.RoleAssistant, entries[1].Message.Role)
		require.Equal(t, "one", entries[1].Message.Blocks[0].Text)

		events = collectEvents(resumed.Run(t.Context(), "second"))
		require.Equal(t, "two", joinedText(events))
		require.Len(t, env.provider.requests[1].Messages, 4)
		require.Equal(t, "second", env.provider.requests[1].Messages[3].Content)
	})

	t.Run("reports unknown sessions", func(t *testing.T) {
		env := newTestEnvironment(t)

		options := env.options()
		options.AgentID = ""
		options.SessionID = id.NewIDGenerator().NewID(t.Context())

		_, err := Prepare(t.Context(), options)

		require.ErrorContains(t, err, "open session")
	})

	t.Run("reports sessions whose agent is gone", func(t *testing.T) {
		env := newTestEnvironment(t)
		stored := env.prepare(t)

		// Another agent stays in the roster, so the failure is the missing
		// agent of the session rather than an empty agent directory.
		env.writeAgent(t, "reviewer", "fake/test-model")
		require.NoError(t, os.Remove(env.agentPath("coder")))

		options := env.options()
		options.AgentID = ""
		options.SessionID = stored.ID()

		_, err := Prepare(t.Context(), options)

		require.ErrorContains(t, err, `unknown agent "coder"`)
	})

	t.Run("switches the agent of a resumed session", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"), textScript("two"))
		env.writeAgentPrompt(t, "reviewer", "fake/test-model", "You review code.")
		stored := env.prepare(t)
		collectEvents(stored.Run(t.Context(), "first"))
		require.Equal(t, "coder", stored.ActiveAgent())
		require.NoError(t, stored.Close())

		options := env.options()
		options.AgentID = "reviewer"
		options.SessionID = stored.ID()

		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		require.Equal(t, "reviewer", prepared.ActiveAgent())
		collectEvents(prepared.Run(t.Context(), "second"))
		require.Len(t, env.provider.requests, 2)
		require.Equal(t, "You review code.", env.provider.requests[1].Messages[0].Content)
	})

	t.Run("switches the model of a resumed session", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"), textScript("two"))
		env.writeSecondModel(t, "second-model", "gpt-second")
		stored := env.prepare(t)
		collectEvents(stored.Run(t.Context(), "first"))
		require.Equal(t, "fake/test-model", stored.ActiveModel())
		require.NoError(t, stored.Close())

		options := env.options()
		options.SessionID = stored.ID()
		options.ModelRef = "fake/second-model"

		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		require.Equal(t, "fake/second-model", prepared.ActiveModel())
		collectEvents(prepared.Run(t.Context(), "second"))
		require.Equal(t, "gpt-second", env.provider.requests[1].Model)
	})

	t.Run("creates a session on the requested model", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"))
		env.writeSecondModel(t, "second-model", "gpt-second")

		options := env.options()
		options.ModelRef = "fake/second-model"

		prepared, err := Prepare(t.Context(), options)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prepared.Close()) })

		require.Equal(t, "fake/second-model", prepared.Info().Model,
			"a model named for a new session is what its header records")
	})

	t.Run("refuses a model the configuration does not hold", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"))

		options := env.options()
		options.SessionID = ""
		options.ModelRef = "fake/ghost"

		_, err := Prepare(t.Context(), options)

		require.ErrorContains(t, err, `model "fake/ghost" is not declared`)
	})

	t.Run("refuses an agent a resumed session does not know", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"))
		stored := env.prepare(t)
		collectEvents(stored.Run(t.Context(), "first"))
		require.NoError(t, stored.Close())

		options := env.options()
		options.AgentID = "ghost"
		options.SessionID = stored.ID()

		_, err := Prepare(t.Context(), options)

		require.ErrorContains(t, err, `agent "ghost" is not defined`)
		require.ErrorContains(t, err, filepath.Join(env.agentsDir, "ghost.md"))
	})

	t.Run("refuses a roster that does not parse", func(t *testing.T) {
		env := newTestEnvironment(t)
		require.NoError(t, os.WriteFile(
			env.agentPath("broken"),
			[]byte("---\ndescription: A test agent\n---\nBody\n"),
			0o600,
		))

		_, err := Prepare(t.Context(), env.options())

		require.ErrorContains(t, err, "load agents")
		require.ErrorContains(t, err, "model is required")
	})
}

// TestSession verifies the branch, the tree and the labels a front end
// navigates.
func TestSession(t *testing.T) {
	t.Run("labels the turns of the tree", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("hello"))
		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "say hello"))

		turn := prepared.Tree()[0]
		require.NoError(t, prepared.SetTag(turn.ID, "bug"))
		require.Equal(t, "bug", prepared.Tree()[0].Tag)

		require.NoError(t, prepared.SetTag(turn.ID, ""))
		require.Empty(t, prepared.Tree()[0].Tag)
	})

	t.Run("names the session", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("hello"))
		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "say hello"))

		require.NoError(t, prepared.SetTitle("Fix the parser"))
		require.Equal(t, "Fix the parser", prepared.Info().Title)

		require.NoError(t, prepared.SetTitle(""))
		require.Equal(t, "say hello", prepared.Info().Title, "the derived title comes back")
	})

	t.Run("reports the turns it does not hold", func(t *testing.T) {
		env := newTestEnvironment(t)
		prepared := env.prepare(t)

		require.ErrorContains(t, prepared.SetLeaf("ghost"), `unknown entry "ghost"`)
		require.ErrorContains(t, prepared.SetTag("ghost", "bug"), `unknown entry "ghost"`)
	})

	t.Run("switches the agent of the branch", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"), textScript("two"))
		env.writeAgentPrompt(t, "reviewer", "fake/test-model", "You review code.")
		prepared := env.prepare(t)

		require.Equal(t, "coder", prepared.ActiveAgent())

		collectEvents(prepared.Run(t.Context(), "first"))
		require.NoError(t, prepared.SetAgent(t.Context(), "reviewer"))

		require.Equal(t, "reviewer", prepared.ActiveAgent())

		collectEvents(prepared.Run(t.Context(), "second"))
		require.Len(t, env.provider.requests, 2)
		require.Equal(t, "You review code.", env.provider.requests[1].Messages[0].Content,
			"the second request carries the definition of the new agent")

		// The selection is bound to the branch: reopening the session resolves
		// it from the file.
		reopened, err := session.Open(
			filepath.Dir(env.sessionPath(t, prepared.ID())),
			prepared.ID(),
			id.NewIDGenerator(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reopened.Close()) })
		require.Equal(t, "reviewer", reopened.ActiveAgent())
	})

	t.Run("switches the model of the branch", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"), textScript("two"))
		env.writeSecondModel(t, "second-model", "gpt-second")
		prepared := env.prepare(t)

		require.Equal(t, "fake/test-model", prepared.ActiveModel())
		require.Equal(t, []string{"fake/second-model", "fake/test-model"}, prepared.Models())

		collectEvents(prepared.Run(t.Context(), "first"))
		require.NoError(t, prepared.SetModel(t.Context(), "fake/second-model"))

		require.Equal(t, "fake/second-model", prepared.ActiveModel())

		collectEvents(prepared.Run(t.Context(), "second"))
		require.Len(t, env.provider.requests, 2)
		require.Equal(t, "gpt-test", env.provider.requests[0].Model)
		require.Equal(t, "gpt-second", env.provider.requests[1].Model,
			"the second request carries the wire id of the new model")
	})

	t.Run("summarizes with the model the branch runs", func(t *testing.T) {
		env := newTestEnvironment(t,
			textScript("one"),
			textScript("two"),
			textScript("the summary"),
		)
		env.writeSecondModel(t, "summarizer", "gpt-summary")
		writeCompactionConfig(t, env, "compaction:\n  keep_recent_tokens: 1\n")
		prepared := env.prepare(t)

		collectEvents(prepared.Run(t.Context(), "first"))
		collectEvents(prepared.Run(t.Context(), "second"))
		require.NoError(t, prepared.SetModel(t.Context(), "fake/summarizer"))

		collectEvents(prepared.Compact(t.Context()))

		require.Len(t, env.provider.requests, 3)
		require.Equal(t, "gpt-summary", env.provider.requests[2].Model,
			"an undeclared compaction model follows the model of the branch")
	})

	t.Run("refuses a model the configuration does not hold", func(t *testing.T) {
		env := newTestEnvironment(t)
		prepared := env.prepare(t)
		before := len(prepared.Tree())

		require.ErrorContains(
			t,
			prepared.SetModel(t.Context(), "fake/ghost"),
			`unknown model "fake/ghost"`,
		)
		require.Len(t, prepared.Tree(), before, "nothing was written")
	})

	t.Run("refuses an agent the roster does not hold", func(t *testing.T) {
		env := newTestEnvironment(t)
		prepared := env.prepare(t)
		before := len(prepared.Tree())

		require.ErrorContains(t, prepared.SetAgent(t.Context(), "ghost"), `unknown agent "ghost"`)
		require.Len(t, prepared.Tree(), before, "nothing was written")
	})
}

// TestSessions verifies session listing.
func TestSessions(t *testing.T) {
	t.Run("lists the sessions of the workspace", func(t *testing.T) {
		env := newTestEnvironment(t)
		first := env.prepare(t)
		second := env.prepare(t)

		infos, err := Sessions(env.options())

		require.NoError(t, err)
		require.Len(t, infos, 2)
		require.Equal(t, "coder", infos[0].Agent)
		require.ElementsMatch(
			t,
			[]string{first.ID(), second.ID()},
			[]string{infos[0].ID, infos[1].ID},
		)
	})

	t.Run("returns no sessions for an unused workspace", func(t *testing.T) {
		env := newTestEnvironment(t)

		infos, err := Sessions(env.options())

		require.NoError(t, err)
		require.Empty(t, infos)
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

	t.Run("continues from the turn the session returned to", func(t *testing.T) {
		env := newTestEnvironment(t,
			textScript("one"),
			textScript("two"),
			textScript("three"),
		)
		prepared := env.prepare(t)

		collectEvents(prepared.Run(t.Context(), "first"))
		collectEvents(prepared.Run(t.Context(), "second"))
		require.Len(t, prepared.Branch(), 4)

		// Returning to the first answer and writing again continues from it
		// instead of the second turn, which stays stored as a branch of its
		// own.
		first := prepared.Branch()[1]
		require.NoError(t, prepared.SetLeaf(first.ID))

		events := collectEvents(prepared.Run(t.Context(), "third"))

		require.Equal(t, "three", joinedText(events))
		require.Len(t, prepared.Branch(), 4)
		require.Equal(t, "third", prepared.Branch()[2].Message.Blocks[0].Text)
		require.Len(t, prepared.Tree(), 6)

		messages := env.provider.requests[2].Messages
		require.Len(t, messages, 4)
		require.Equal(t, "first", messages[1].Content)
		require.Equal(t, "one", messages[2].Content)
		require.Equal(t, "third", messages[3].Content)
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

// writeCompactionConfig rewrites the configuration of an environment, keeping
// its provider and adding a compaction block.
func writeCompactionConfig(t *testing.T, env *testEnvironment, compactionBlock string) {
	t.Helper()

	configuration := "providers:\n" +
		"  fake:\n" +
		"    protocol: openai_chat_completions\n" +
		"    base_url: " + env.provider.server.URL + "\n" +
		"    api_key: test-key\n" +
		"    models:\n" +
		"      test-model:\n" +
		"        id: gpt-test\n" +
		"      summarizer:\n" +
		"        id: gpt-summary\n" +
		compactionBlock
	require.NoError(t, os.WriteFile(env.configPath, []byte(configuration), 0o600))
}

// TestCompaction verifies the compaction wiring of the harness.
func TestCompaction(t *testing.T) {
	t.Run("reaches the engine and can compact", func(t *testing.T) {
		env := newTestEnvironment(t,
			textScript("one"),
			textScript("two"),
			textScript("the summary"),
		)
		writeCompactionConfig(t, env, "compaction:\n  keep_recent_tokens: 1\n")

		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "first"))
		collectEvents(prepared.Run(t.Context(), "second"))

		require.True(t, prepared.CanCompact())

		events := collectEvents(prepared.Compact(t.Context()))

		require.Equal(
			t,
			engine.EndReasonTurn,
			events[len(events)-1].Reason,
			"events: %+v",
			events,
		)
		require.Contains(t, eventTypeList(events), engine.EventCompactionEnd)

		branch := prepared.Branch()
		require.Equal(t, session.KindCompaction, branch[len(branch)-1].Kind)
		require.Equal(t, "the summary", branch[len(branch)-1].CompactionSummary)
	})

	t.Run("reads the prompt override from the home directory", func(t *testing.T) {
		env := newTestEnvironment(t,
			textScript("one"),
			textScript("two"),
			textScript("the summary"),
		)
		writeCompactionConfig(t, env, "compaction:\n  keep_recent_tokens: 1\n")

		override := filepath.Join(os.Getenv("HOME"), ".rienda", "COMPACTION.md")
		//nolint:gosec // the path lives in a test temporary directory.
		require.NoError(t, os.MkdirAll(filepath.Dir(override), 0o750))
		//nolint:gosec // the path lives in a test temporary directory.
		require.NoError(t, os.WriteFile(override, []byte("My own format."), 0o600))

		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "first"))
		collectEvents(prepared.Run(t.Context(), "second"))
		collectEvents(prepared.Compact(t.Context()))

		require.Len(t, env.provider.requests, 3)
		require.Equal(t, "My own format.", env.provider.requests[2].Messages[0].Content)
	})

	t.Run("carries keep_recent_tokens into the procedure", func(t *testing.T) {
		env := newTestEnvironment(t, textScript("one"))
		// A budget larger than the whole conversation leaves nothing to
		// summarize, so the procedure refuses and nothing can be compacted.
		writeCompactionConfig(t, env, "compaction:\n  keep_recent_tokens: 100000\n")

		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "first"))

		require.False(t, prepared.CanCompact())

		// The reason reaches the caller, so the interface can explain why the
		// manual command is unavailable instead of only fading it.
		refusal, refused := prepared.CompactRefusal()
		require.True(t, refused)
		require.Equal(t, compaction.RefusalShort, refusal.Kind)
		require.Positive(t, refusal.Needed)

		events := collectEvents(prepared.Compact(t.Context()))

		require.Equal(t, engine.EndReasonTurn, events[len(events)-1].Reason)
		require.NotContains(t, eventTypeList(events), engine.EventCompactionStart)
	})

	t.Run("explains a conversation that already ends in a compaction", func(t *testing.T) {
		env := newTestEnvironment(
			t,
			textScript("one"),
			textScript("two"),
			textScript("the summary"),
		)
		writeCompactionConfig(t, env, "compaction:\n  keep_recent_tokens: 1\n")

		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "first"))
		collectEvents(prepared.Run(t.Context(), "second"))
		collectEvents(prepared.Compact(t.Context()))

		refusal, refused := prepared.CompactRefusal()

		require.True(t, refused)
		require.Equal(t, compaction.RefusalCompacted, refusal.Kind)
	})

	t.Run("uses the declared summarization model", func(t *testing.T) {
		env := newTestEnvironment(t,
			textScript("one"),
			textScript("two"),
			textScript("the summary"),
		)
		writeCompactionConfig(t, env, "compaction:\n"+
			"  keep_recent_tokens: 1\n"+
			"  model: fake/summarizer\n")

		prepared := env.prepare(t)
		collectEvents(prepared.Run(t.Context(), "first"))
		collectEvents(prepared.Run(t.Context(), "second"))
		collectEvents(prepared.Compact(t.Context()))

		require.Len(t, env.provider.requests, 3)
		require.Equal(t, "gpt-summary", env.provider.requests[2].Model)
	})
}

// eventTypeList returns the types of a list of events.
func eventTypeList(events []engine.Event) []engine.EventType {
	types := make([]engine.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
