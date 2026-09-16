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

// openAIChatTestServer records the request it receives and replays canned responses.
type openAIChatTestServer struct {
	server      *httptest.Server
	requestBody map[string]any
	response    string
	status      int
	streamBody  string
}

func newOpenAIChatTestServer(t *testing.T) *openAIChatTestServer {
	t.Helper()
	fixture := &openAIChatTestServer{status: http.StatusOK}
	fixture.server = httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/chat/completions", r.URL.Path)
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			if len(body) > 0 {
				require.NoError(t, json.Unmarshal(body, &fixture.requestBody))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(fixture.status)
			payload := fixture.response
			if fixture.streamBody != "" {
				payload = fixture.streamBody
			}
			_, _ = w.Write([]byte(payload))
		}),
	)
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *openAIChatTestServer) client() llm.Client {
	return NewOpenAIChat(Config{APIKey: "test-key", BaseURL: f.server.URL})
}

func TestOpenAIChatGenerate(t *testing.T) {
	t.Run("maps a tool call request and response", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.response = `{
			"id": "chatcmpl_1", "object": "chat.completion", "model": "gpt-test",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant", "content": null,
					"tool_calls": [{
						"id": "call_1", "type": "function",
						"function": {"name": "read", "arguments": "{\"path\":\"a.go\"}"}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 20, "completion_tokens": 8, "total_tokens": 28,
				"prompt_tokens_details": {"cached_tokens": 7},
				"completion_tokens_details": {"reasoning_tokens": 3}}
		}`
		effort := "low"
		parallel := false

		resp, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:             "gpt-test",
			System:            "Be helpful.",
			MaxTokens:         256,
			Reasoning:         &llm.ReasoningConfig{Effort: effort},
			ToolChoice:        &llm.ToolChoice{Mode: llm.ToolChoiceAuto},
			ParallelToolCalls: &parallel,
			StopSequences:     []string{"END"},
			Tools:             []llm.Tool{{Name: "read", Description: "Read a file", Strict: true}},
			Messages: []llm.Message{
				{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Read a.go"}}},
				{Role: llm.RoleAssistant, Blocks: []llm.Block{
					{Type: llm.BlockThinking, Thinking: "dropped"},
					{
						Type: llm.BlockToolCall, ToolCallID: "call_0", ToolCallName: "read",
						ToolCallArguments: json.RawMessage(`{"path":"a.go"}`),
					},
				}},
				{Role: llm.RoleUser, Blocks: []llm.Block{
					{Type: llm.BlockText, Text: "before"},
					{
						Type: llm.BlockToolResult, ToolResultCallID: "call_0",
						ToolResult: []llm.Block{{Type: llm.BlockText, Text: "content"}},
					},
					{Type: llm.BlockText, Text: "after"},
				}},
			},
		})

		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"model":                 "gpt-test",
			"max_completion_tokens": float64(256),
			"reasoning_effort":      "low",
			"tool_choice":           "auto",
			"parallel_tool_calls":   false,
			"stop":                  []any{"END"},
			"tools": []any{
				map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": "read", "description": "Read a file",
						"parameters": map[string]any{}, "strict": true,
					},
				},
			},
			"messages": []any{
				map[string]any{"role": "system", "content": "Be helpful."},
				map[string]any{"role": "user", "content": "Read a.go"},
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id":   "call_0",
							"type": "function",
							"function": map[string]any{
								"name": "read", "arguments": `{"path":"a.go"}`,
							},
						},
					},
				},
				map[string]any{"role": "user", "content": "before"},
				map[string]any{"role": "tool", "content": "content", "tool_call_id": "call_0"},
				map[string]any{"role": "user", "content": "after"},
			},
		}, fixture.requestBody)
		require.Equal(t, &llm.Response{
			ID:    "chatcmpl_1",
			Model: "gpt-test",
			Blocks: []llm.Block{{
				Type:              llm.BlockToolCall,
				ToolCallID:        "call_1",
				ToolCallName:      "read",
				ToolCallArguments: json.RawMessage(`{"path":"a.go"}`),
			}},
			StopReason: llm.StopReasonToolUse,
			Usage: llm.Usage{
				InputTokens:     20,
				OutputTokens:    8,
				ReasoningTokens: 3,
				CacheReadTokens: 7,
			},
		}, resp)
	})

	t.Run("maps text content and refusal-free stop reasons", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.response = `{
			"id": "c2", "model": "gpt-test",
			"choices": [{"index": 0,
				"message": {"role": "assistant", "content": "Done", "reasoning_content": "Because"},
				"finish_reason": "length"}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2,
				"completion_tokens_details": {"reasoning_tokens": 0}}
		}`

		resp, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "gpt-test"})

		require.NoError(t, err)
		require.Equal(t, []llm.Block{
			{Type: llm.BlockText, Text: "Done"},
			{Type: llm.BlockThinking, Thinking: "Because"},
		}, resp.Blocks)
		require.Equal(t, llm.StopReasonMaxTokens, resp.StopReason)
		require.NotContains(t, fixture.requestBody, "stream")
	})
}

func TestOpenAIChatStream(t *testing.T) {
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

	t.Run("translates chunks, tool calls and usage", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.streamBody = "data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
			"data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"Hi"}}]}` + "\n\n" +
			"data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,` +
			`"delta":{"reasoning_content":"Hmm"}}]}` + "\n\n" +
			"data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,` +
			`"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read"}}]}}]}` + "\n\n" +
			"data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,` +
			`"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}` + "\n\n" +
			"data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,` +
			`"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a\"}"}}]}}],` +
			`"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28,` +
			`"prompt_tokens_details":{"cached_tokens":7},` +
			`"completion_tokens_details":{"reasoning_tokens":3}}}` + "\n\n" +
			"data: " +
			`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "gpt-test"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		events := collect(t, stream)

		require.Equal(t, []llm.StreamEvent{
			{Type: llm.StreamMessageStart, ID: "chatcmpl_1", Model: "gpt-test"},
			{Type: llm.StreamTextDelta, Text: "Hi"},
			{Type: llm.StreamThinkingDelta, Thinking: "Hmm"},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "read"},
			{
				Type:              llm.StreamToolCallArgsDelta,
				ToolCallID:        "call_1",
				ToolCallArgsDelta: `{"path":`,
			},
			{Type: llm.StreamToolCallArgsDelta, ToolCallID: "call_1", ToolCallArgsDelta: `"a"}`},
			{
				Type:       llm.StreamMessageEnd,
				StopReason: llm.StopReasonToolUse,
				Usage: llm.Usage{
					InputTokens:     20,
					OutputTokens:    8,
					ReasoningTokens: 3,
					CacheReadTokens: 7,
				},
			},
		}, events)
		require.Equal(t, true, fixture.requestBody["stream"])
		require.Equal(
			t,
			map[string]any{"include_usage": true},
			fixture.requestBody["stream_options"],
		)
	})

	t.Run("rejects streams ending without a finish reason", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.streamBody = "data: " +
			`{"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
			"data: [DONE]\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "m"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		event, err := stream.Next()
		require.NoError(t, err)
		require.Equal(t, llm.StreamMessageStart, event.Type)

		_, err = stream.Next()
		require.Error(t, err)
		require.NotErrorIs(t, err, io.EOF)
	})

	t.Run("surfaces error payloads instead of truncation", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.streamBody = "data: " +
			`{"error":{"message":"bad input","type":"invalid_request_error",` +
			`"code":"invalid_value"}}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "m"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		_, err = stream.Next()

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "bad input", providerErr.Message)
		require.Equal(t, "invalid_request_error", providerErr.Type)
		require.Equal(t, llm.ErrorKindInvalidRequest, providerErr.Kind)
	})

	t.Run("reports a truncated stream", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.streamBody = "data: " +
			`{"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "m"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		event, err := stream.Next()
		require.NoError(t, err)
		require.Equal(t, llm.StreamMessageStart, event.Type)

		_, err = stream.Next()
		require.ErrorContains(t, err, "ended before [DONE]")

		require.NoError(t, stream.Close())
	})

	t.Run("wraps read failures", func(t *testing.T) {
		client := NewOpenAIChat(Config{
			APIKey:     "key",
			BaseURL:    "https://example.com",
			HTTPClient: &http.Client{Transport: failingTransport{readErr: errors.New("boom")}},
		})

		stream, err := client.Stream(t.Context(), &llm.Request{Model: "m"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		_, err = stream.Next()

		require.Error(t, err)
		require.NotErrorIs(t, err, io.EOF)
		require.ErrorContains(t, err, "read stream")
		require.ErrorContains(t, err, "boom")
	})
}

func TestOpenAIToolChoiceFrom(t *testing.T) {
	t.Run("maps every canonical mode", func(t *testing.T) {
		cases := []struct {
			name   string
			choice *llm.ToolChoice
			want   any
		}{
			{name: "auto", choice: &llm.ToolChoice{Mode: llm.ToolChoiceAuto}, want: "auto"},
			{name: "none", choice: &llm.ToolChoice{Mode: llm.ToolChoiceNone}, want: "none"},
			{
				name:   "required",
				choice: &llm.ToolChoice{Mode: llm.ToolChoiceRequired},
				want:   "required",
			},
			{
				name:   "specific tool",
				choice: &llm.ToolChoice{Mode: llm.ToolChoiceTool, ToolName: "read"},
				want: map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "read"},
				},
			},
		}

		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				require.Equal(t, testCase.want, openAIToolChoiceFrom(testCase.choice))
			})
		}
	})
}

func TestOpenAIChatErrors(t *testing.T) {
	t.Run("maps HTTP failures to llm errors", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.status = http.StatusTooManyRequests
		fixture.response = `{"error": {"message": "slow down", "type": "rate_limit", "code": "rate_limit_exceeded"}}`

		_, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "m"})

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "openai-chat", providerErr.Provider)
		require.Equal(t, http.StatusTooManyRequests, providerErr.StatusCode)
		require.Equal(t, "rate_limit", providerErr.Type)
		require.Equal(t, "slow down", providerErr.Message)
	})

	t.Run("rejects a nil request", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)

		_, err := fixture.client().Generate(t.Context(), nil)
		require.Error(t, err)

		_, err = fixture.client().Stream(t.Context(), nil)
		require.Error(t, err)
	})

	t.Run("wraps network failures", func(t *testing.T) {
		client := NewOpenAIChat(Config{
			APIKey:     "key",
			BaseURL:    "https://example.com",
			HTTPClient: &http.Client{Transport: errorTransport{err: errors.New("boom")}},
		})

		_, err := client.Generate(t.Context(), &llm.Request{Model: "m"})
		require.ErrorContains(t, err, "send request")
		require.ErrorContains(t, err, "boom")

		_, err = client.Stream(t.Context(), &llm.Request{Model: "m"})
		require.ErrorContains(t, err, "send request")
		require.ErrorContains(t, err, "boom")
	})

	t.Run("reports undecodable responses", func(t *testing.T) {
		fixture := newOpenAIChatTestServer(t)
		fixture.response = "<html>nope</html>"

		_, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "m"})

		require.ErrorContains(t, err, "decode response")
	})

	t.Run("uses the status text when the failure body cannot be read", func(t *testing.T) {
		client := NewOpenAIChat(Config{
			APIKey:  "key",
			BaseURL: "https://example.com",
			HTTPClient: &http.Client{Transport: failingTransport{
				readErr:    errors.New("boom"),
				statusCode: http.StatusBadGateway,
			}},
		})

		_, err := client.Generate(t.Context(), &llm.Request{Model: "m"})

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, http.StatusBadGateway, providerErr.StatusCode)
		require.Equal(t, http.StatusText(http.StatusBadGateway), providerErr.Message)
		require.Equal(t, llm.ErrorKindServer, providerErr.Kind)
	})
}

func TestOpenAIChatStopReasons(t *testing.T) {
	t.Run("maps every known wire reason", func(t *testing.T) {
		cases := map[string]llm.StopReason{
			"stop":           llm.StopReasonEndTurn,
			"length":         llm.StopReasonMaxTokens,
			"tool_calls":     llm.StopReasonToolUse,
			"function_call":  llm.StopReasonToolUse,
			"content_filter": llm.StopReasonContentFilter,
			"something_new":  llm.StopReasonEndTurn,
			"":               llm.StopReasonEndTurn,
		}
		for wire, expected := range cases {
			require.Equal(t, expected, openAIChatStopReasonTo(wire), "wire reason %q", wire)
		}
	})
}
