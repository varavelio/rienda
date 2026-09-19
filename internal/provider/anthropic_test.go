package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/varavelio/rienda/internal/llm"

	"github.com/stretchr/testify/require"
)

// anthropicTestServer records the request it receives and replays canned responses.
type anthropicTestServer struct {
	server      *httptest.Server
	requestBody map[string]any
	headers     http.Header
	response    string
	status      int
	streamBody  string
}

func newAnthropicTestServer(t *testing.T) *anthropicTestServer {
	t.Helper()
	fixture := &anthropicTestServer{status: http.StatusOK}
	fixture.server = httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fixture.headers = r.Header.Clone()
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			if len(body) > 0 {
				require.NoError(t, json.Unmarshal(body, &fixture.requestBody))
			}
			if fixture.streamBody != "" {
				w.Header().Set("Content-Type", "text/event-stream")
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(fixture.status)
			if fixture.streamBody != "" {
				_, _ = w.Write([]byte(fixture.streamBody))
			} else {
				_, _ = w.Write([]byte(fixture.response))
			}
		}),
	)
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *anthropicTestServer) client() llm.Client {
	return NewAnthropic(Config{APIKey: "test-key", BaseURL: f.server.URL + "/"})
}

func TestAnthropicGenerate(t *testing.T) {
	t.Run("maps a text request and response", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-test",
			"content": [{"type": "text", "text": "Hello!"}],
			"stop_reason": "end_turn", "stop_sequence": null,
			"usage": {"input_tokens": 10, "output_tokens": 5,
				"cache_creation_input_tokens": 2, "cache_read_input_tokens": 3}
		}`

		resp, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:     "claude-test",
			System:    "Be helpful.",
			MaxTokens: 512,
			Messages: []llm.Message{
				{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Hi"}}},
			},
		})

		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"model":      "claude-test",
			"max_tokens": float64(512),
			"system":     "Be helpful.",
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "Hi"},
					},
				},
			},
		}, fixture.requestBody)
		require.Equal(t, "test-key", fixture.headers.Get("X-Api-Key"))
		require.Equal(t, "2023-06-01", fixture.headers.Get("Anthropic-Version"))
		require.Equal(t, &llm.Response{
			ID:         "msg_1",
			Model:      "claude-test",
			Blocks:     []llm.Block{{Type: llm.BlockText, Text: "Hello!"}},
			StopReason: llm.StopReasonEndTurn,
			Usage: llm.Usage{
				InputTokens: 10, OutputTokens: 5, CacheReadTokens: 3, CacheWriteTokens: 2,
			},
		}, resp)
	})

	t.Run("applies the default max tokens when unset", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c", "content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`

		_, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "c"})

		require.NoError(t, err)
		require.Equal(t, float64(8192), fixture.requestBody["max_tokens"])
		require.NotContains(t, fixture.requestBody, "stream")
	})

	t.Run("maps tools, tool choice and thinking, dropping temperature", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c",
			"content": [
				{"type": "thinking", "thinking": "Let me think", "signature": "sig-1"},
				{"type": "tool_use", "id": "toolu_1", "name": "read",
					"input": {"path": "main.go"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`
		temperature := 0.7

		resp, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:       "c",
			Temperature: &temperature,
			Thinking:    &llm.ThinkingConfig{MaxTokens: 1000},
			Tools: []llm.Tool{
				{
					Name:        "read",
					Description: "Read a file",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
				{Name: "empty"},
			},
			ToolChoice: &llm.ToolChoice{Mode: llm.ToolChoiceTool, ToolName: "read"},
			Messages: []llm.Message{
				{Role: llm.RoleAssistant, Blocks: []llm.Block{
					{Type: llm.BlockThinking, Thinking: "Hmm", ThinkingSignature: "sig-0"},
					{Type: llm.BlockToolCall, ToolCallID: "toolu_0", ToolCallName: "read"},
				}},
				{Role: llm.RoleUser, Blocks: []llm.Block{
					{
						Type:              llm.BlockToolResult,
						ToolResultCallID:  "toolu_0",
						ToolResultIsError: true,
						ToolResult:        []llm.Block{{Type: llm.BlockText, Text: "not found"}},
					},
				}},
			},
		})

		require.NoError(t, err)
		require.Equal(t, map[string]any{"type": "enabled", "budget_tokens": float64(1000)},
			fixture.requestBody["thinking"])
		require.NotContains(t, fixture.requestBody, "temperature")
		require.NotContains(t, fixture.requestBody, "system")
		require.Equal(
			t,
			map[string]any{"type": "tool", "name": "read"},
			fixture.requestBody["tool_choice"],
		)
		require.Equal(t, []any{
			map[string]any{
				"name": "read", "description": "Read a file",
				"input_schema": map[string]any{"type": "object"},
			},
			map[string]any{"name": "empty", "input_schema": map[string]any{}},
		}, fixture.requestBody["tools"])
		require.Equal(t, []any{
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "Hmm", "signature": "sig-0"},
					map[string]any{
						"type": "tool_use", "id": "toolu_0", "name": "read",
						"input": map[string]any{},
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "tool_result", "tool_use_id": "toolu_0", "is_error": true,
						"content": []any{map[string]any{"type": "text", "text": "not found"}},
					},
				},
			},
		}, fixture.requestBody["messages"])
		require.Equal(t, []llm.Block{
			{Type: llm.BlockThinking, Thinking: "Let me think", ThinkingSignature: "sig-1"},
			{
				Type: llm.BlockToolCall, ToolCallID: "toolu_1", ToolCallName: "read",
				ToolCallArguments: json.RawMessage(`{"path": "main.go"}`),
			},
		}, resp.Blocks)
		require.Equal(t, llm.StopReasonToolUse, resp.StopReason)
	})

	t.Run("maps strict tools and disabling parallel tool use", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c", "content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`
		parallel := false

		_, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:             "c",
			Tools:             []llm.Tool{{Name: "read", Strict: true}},
			ParallelToolCalls: &parallel,
		})

		require.NoError(t, err)
		require.Equal(t, []any{
			map[string]any{"name": "read", "input_schema": map[string]any{}, "strict": true},
		}, fixture.requestBody["tools"])
		require.Equal(t, map[string]any{
			"type": "auto", "disable_parallel_tool_use": true,
		}, fixture.requestBody["tool_choice"])
	})

	t.Run("allows parallel tool use explicitly", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c", "content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`
		parallel := true

		_, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:             "c",
			Tools:             []llm.Tool{{Name: "read"}},
			ParallelToolCalls: &parallel,
		})

		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"type": "auto", "disable_parallel_tool_use": false,
		}, fixture.requestBody["tool_choice"])
	})

	t.Run("drops the parallel flag for a forced single tool", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c", "content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`
		parallel := false

		_, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:             "c",
			Tools:             []llm.Tool{{Name: "read"}},
			ToolChoice:        &llm.ToolChoice{Mode: llm.ToolChoiceTool, ToolName: "read"},
			ParallelToolCalls: &parallel,
		})

		require.NoError(t, err)
		require.Equal(
			t,
			map[string]any{"type": "tool", "name": "read"},
			fixture.requestBody["tool_choice"],
		)
	})

	t.Run("drops the parallel flag without tools", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c", "content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`
		parallel := false

		_, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:             "c",
			ParallelToolCalls: &parallel,
		})

		require.NoError(t, err)
		require.NotContains(t, fixture.requestBody, "tool_choice")
	})

	t.Run("replays redacted thinking blocks", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c", "content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`

		_, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model: "c",
			Messages: []llm.Message{
				{Role: llm.RoleAssistant, Blocks: []llm.Block{
					{Type: llm.BlockRedactedThinking, ThinkingRedactedData: "opaque"},
				}},
			},
		})

		require.NoError(t, err)
		require.Equal(t, []any{
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "redacted_thinking", "data": "opaque"},
				},
			},
		}, fixture.requestBody["messages"])
	})

	t.Run("maps redacted thinking responses", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.response = `{"id": "m", "model": "c",
			"content": [{"type": "redacted_thinking", "data": "opaque"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}}`

		resp, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "c"})

		require.NoError(t, err)
		require.Equal(t, []llm.Block{
			{Type: llm.BlockRedactedThinking, ThinkingRedactedData: "opaque"},
		}, resp.Blocks)
	})
}

func TestAnthropicStream(t *testing.T) {
	collect := func(t *testing.T, stream llm.Stream) []llm.StreamEvent {
		t.Helper()
		var events []llm.StreamEvent
		for {
			event, err := stream.Next()
			if errors.Is(err, io.EOF) {
				return events
			}
			require.NoError(t, err)
			events = append(events, event)
		}
	}

	t.Run("translates the full event sequence", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.streamBody = "event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_1","model":"c",` +
			`"usage":{"input_tokens":7,"cache_read_input_tokens":1,"cache_creation_input_tokens":2}}}` + "\n\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Hmm"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-9"}}` + "\n\n" +
			"event: content_block_stop\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":2,` +
			`"content_block":{"type":"tool_use","id":"toolu_1","name":"read"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":2,` +
			`"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":2,` +
			`"delta":{"type":"input_json_delta","partial_json":"\"a\"}"}}` + "\n\n" +
			"event: content_block_stop\n" +
			`data: {"type":"content_block_stop","index":2}` + "\n\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n" +
			"event: ping\n" +
			`data: {"type":"ping"}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "c"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		events := collect(t, stream)

		require.Equal(t, []llm.StreamEvent{
			{Type: llm.StreamMessageStart, ID: "msg_1", Model: "c"},
			{Type: llm.StreamThinkingDelta, Thinking: "Hmm"},
			{Type: llm.StreamThinkingDelta, ThinkingSignature: "sig-9"},
			{Type: llm.StreamTextDelta, Text: "Hi"},
			{Type: llm.StreamToolCallStart, ToolCallID: "toolu_1", ToolCallName: "read"},
			{
				Type:              llm.StreamToolCallArgsDelta,
				ToolCallID:        "toolu_1",
				ToolCallArgsDelta: `{"path":`,
			},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "toolu_1", ToolCallArgsDelta: `"a"}`},
			{
				Type:       llm.StreamMessageEnd,
				StopReason: llm.StopReasonToolUse,
				Usage: llm.Usage{
					InputTokens:      7,
					OutputTokens:     12,
					CacheReadTokens:  1,
					CacheWriteTokens: 2,
				},
			},
		}, events)
		require.Equal(t, true, fixture.requestBody["stream"])
	})

	t.Run("surfaces stream error events", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.streamBody = "event: error\n" +
			`data: {"type":"error","error":{"type":"overloaded_error","message":"busy"}}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "c"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		_, err = stream.Next()

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "anthropic", providerErr.Provider)
		require.Equal(t, "overloaded_error", providerErr.Type)
		require.Equal(t, "busy", providerErr.Message)
		require.Equal(t, llm.ErrorKindOverloaded, providerErr.Kind)
	})

	t.Run("surfaces redacted thinking blocks", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.streamBody = "event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,` +
			`"content_block":{"type":"redacted_thinking","data":"opaque"}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "c"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		event, err := stream.Next()

		require.NoError(t, err)
		require.Equal(t, llm.StreamEvent{
			Type:                 llm.StreamThinkingRedacted,
			ThinkingRedactedData: "opaque",
		}, event)
	})

	t.Run("reports a truncated stream", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.streamBody = "event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"m","model":"c",` +
			`"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "c"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		event, err := stream.Next()
		require.NoError(t, err)
		require.Equal(t, llm.StreamMessageStart, event.Type)

		_, err = stream.Next()
		require.ErrorContains(t, err, "ended before message_stop")
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("wraps read failures", func(t *testing.T) {
		client := NewAnthropic(Config{
			APIKey:     "key",
			BaseURL:    "https://example.com",
			HTTPClient: &http.Client{Transport: failingTransport{readErr: errors.New("boom")}},
		})

		stream, err := client.Stream(t.Context(), &llm.Request{Model: "c"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		_, err = stream.Next()

		require.Error(t, err)
		require.NotErrorIs(t, err, io.EOF)
		require.ErrorContains(t, err, "read stream")
		require.ErrorContains(t, err, "boom")
	})
}

func TestAnthropicToolChoiceFrom(t *testing.T) {
	t.Run("maps every canonical mode", func(t *testing.T) {
		cases := []struct {
			name   string
			choice *llm.ToolChoice
			want   *anthropicToolChoice
		}{
			{name: "nil defaults to auto", want: &anthropicToolChoice{Type: "auto"}},
			{
				name:   "auto",
				choice: &llm.ToolChoice{Mode: llm.ToolChoiceAuto},
				want:   &anthropicToolChoice{Type: "auto"},
			},
			{
				name:   "none",
				choice: &llm.ToolChoice{Mode: llm.ToolChoiceNone},
				want:   &anthropicToolChoice{Type: "none"},
			},
			{
				name:   "required becomes any",
				choice: &llm.ToolChoice{Mode: llm.ToolChoiceRequired},
				want:   &anthropicToolChoice{Type: "any"},
			},
			{
				name:   "specific tool",
				choice: &llm.ToolChoice{Mode: llm.ToolChoiceTool, ToolName: "read"},
				want:   &anthropicToolChoice{Type: "tool", Name: "read"},
			},
		}

		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				require.Equal(t, testCase.want, anthropicToolChoiceFrom(testCase.choice))
			})
		}
	})
}

func TestAnthropicErrors(t *testing.T) {
	t.Run("maps HTTP failures to llm errors", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)
		fixture.status = http.StatusUnauthorized
		fixture.response = `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`

		_, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "c"})

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "anthropic", providerErr.Provider)
		require.Equal(t, http.StatusUnauthorized, providerErr.StatusCode)
		require.Equal(t, "authentication_error", providerErr.Type)
		require.Equal(t, "bad key", providerErr.Message)
	})

	t.Run("rejects a nil request", func(t *testing.T) {
		fixture := newAnthropicTestServer(t)

		_, err := fixture.client().Generate(t.Context(), nil)
		require.Error(t, err)

		_, err = fixture.client().Stream(t.Context(), nil)
		require.Error(t, err)
	})
}

func TestAnthropicStopReasons(t *testing.T) {
	t.Run("maps every known wire reason", func(t *testing.T) {
		cases := map[string]llm.StopReason{
			"end_turn":      llm.StopReasonEndTurn,
			"max_tokens":    llm.StopReasonMaxTokens,
			"tool_use":      llm.StopReasonToolUse,
			"stop_sequence": llm.StopReasonStopSequence,
			"refusal":       llm.StopReasonRefusal,
			"pause_turn":    llm.StopReasonPaused,
			"something_new": llm.StopReasonEndTurn,
			"":              llm.StopReasonEndTurn,
		}
		for wire, expected := range cases {
			require.Equal(t, expected, anthropicStopReasonTo(wire), "wire reason %q", wire)
		}
	})
}
