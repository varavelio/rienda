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

// openAIResponsesTestServer records the request it receives and replays canned responses.
type openAIResponsesTestServer struct {
	server      *httptest.Server
	requestBody map[string]any
	response    string
	status      int
	streamBody  string
}

func newOpenAIResponsesTestServer(t *testing.T) *openAIResponsesTestServer {
	t.Helper()
	fixture := &openAIResponsesTestServer{status: http.StatusOK}
	fixture.server = httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/responses", r.URL.Path)
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

func (f *openAIResponsesTestServer) client() llm.Client {
	return NewOpenAIResponses(Config{APIKey: "test-key", BaseURL: f.server.URL})
}

func TestOpenAIResponsesGenerate(t *testing.T) {
	t.Run("maps a full turn request and response", func(t *testing.T) {
		fixture := newOpenAIResponsesTestServer(t)
		fixture.response = `{
			"id": "resp_1", "object": "response", "model": "gpt-test", "status": "completed",
			"output": [
				{"type": "reasoning", "id": "rs_1", "status": "completed",
					"summary": [{"type": "summary_text", "text": "Thinking"}],
					"encrypted_content": "enc-1"},
				{"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
					"content": [{"type": "output_text", "text": "Reading", "annotations": []}]},
				{"type": "function_call", "id": "fc_1", "call_id": "call_1",
					"name": "read", "arguments": "{\"path\":\"a.go\"}", "status": "completed"}
			],
			"usage": {"input_tokens": 30, "output_tokens": 12, "total_tokens": 42,
				"input_tokens_details": {"cached_tokens": 5, "cache_write_tokens": 1},
				"output_tokens_details": {"reasoning_tokens": 4}}
		}`
		parallel := false

		resp, err := fixture.client().Generate(t.Context(), &llm.Request{
			Model:             "gpt-test",
			System:            "Be helpful.",
			MaxTokens:         256,
			Reasoning:         &llm.ReasoningConfig{Effort: "medium"},
			ToolChoice:        &llm.ToolChoice{Mode: llm.ToolChoiceRequired},
			ParallelToolCalls: &parallel,
			Tools:             []llm.Tool{{Name: "read", Description: "Read a file", Strict: true}},
			StopSequences:     []string{"END"},
			Messages: []llm.Message{
				{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Read a.go"}}},
				{Role: llm.RoleAssistant, Blocks: []llm.Block{
					{Type: llm.BlockText, Text: "On it"},
					{Type: llm.BlockThinking, Thinking: "plan", ThinkingSignature: "enc-0"},
					{Type: llm.BlockThinking, Thinking: "unsigned"},
					{
						Type: llm.BlockToolCall, ToolCallID: "call_0", ToolCallName: "read",
						ToolCallArguments: json.RawMessage(`{"path":"a.go"}`),
					},
				}},
				{Role: llm.RoleUser, Blocks: []llm.Block{
					{
						Type: llm.BlockToolResult, ToolResultCallID: "call_0",
						ToolResult: []llm.Block{{Type: llm.BlockText, Text: "content"}},
					},
				}},
			},
		})

		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"model":               "gpt-test",
			"instructions":        "Be helpful.",
			"max_output_tokens":   float64(256),
			"reasoning":           map[string]any{"effort": "medium"},
			"tool_choice":         "required",
			"parallel_tool_calls": false,
			"store":               false,
			"tools": []any{
				map[string]any{
					"type": "function", "name": "read", "description": "Read a file",
					"parameters": map[string]any{}, "strict": true,
				},
			},
			"input": []any{
				map[string]any{
					"type": "message", "role": "user",
					"content": []any{map[string]any{"type": "input_text", "text": "Read a.go"}},
				},
				map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "On it"}},
				},
				map[string]any{"type": "reasoning", "encrypted_content": "enc-0"},
				map[string]any{
					"type": "function_call", "call_id": "call_0", "name": "read",
					"arguments": `{"path":"a.go"}`,
				},
				map[string]any{
					"type": "function_call_output", "call_id": "call_0", "output": "content",
				},
			},
		}, fixture.requestBody)
		require.NotContains(t, fixture.requestBody, "stop")
		require.Equal(t, &llm.Response{
			ID:    "resp_1",
			Model: "gpt-test",
			Blocks: []llm.Block{
				{Type: llm.BlockThinking, Thinking: "Thinking", ThinkingSignature: "enc-1"},
				{Type: llm.BlockText, Text: "Reading"},
				{
					Type: llm.BlockToolCall, ToolCallID: "call_1", ToolCallName: "read",
					ToolCallArguments: json.RawMessage(`{"path":"a.go"}`),
				},
			},
			StopReason: llm.StopReasonToolUse,
			Usage: llm.Usage{
				InputTokens: 30, OutputTokens: 12, ReasoningTokens: 4,
				CacheReadTokens: 5, CacheWriteTokens: 1,
			},
		}, resp)
	})

	t.Run("maps incomplete and failed statuses", func(t *testing.T) {
		t.Run("max output tokens", func(t *testing.T) {
			fixture := newOpenAIResponsesTestServer(t)
			fixture.response = `{"id": "r", "model": "m", "status": "incomplete",
				"incomplete_details": {"reason": "max_output_tokens"},
				"output": [{"type": "message", "id": "m1", "role": "assistant",
					"content": [{"type": "output_text", "text": "Partial"}]}],
				"usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2,
					"output_tokens_details": {"reasoning_tokens": 0}}}`

			resp, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "m"})

			require.NoError(t, err)
			require.Equal(t, llm.StopReasonMaxTokens, resp.StopReason)
			require.Equal(t, []llm.Block{{Type: llm.BlockText, Text: "Partial"}}, resp.Blocks)
		})

		t.Run("failed response", func(t *testing.T) {
			fixture := newOpenAIResponsesTestServer(t)
			fixture.response = `{"id": "r", "model": "m", "status": "failed",
				"error": {"code": "server_error", "message": "boom"},
				"output": [],
				"usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0,
					"output_tokens_details": {"reasoning_tokens": 0}}}`

			_, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "m"})

			var failedErr *llm.Error
			require.ErrorAs(t, err, &failedErr)
			require.Equal(t, "openai-responses", failedErr.Provider)
			require.Equal(t, "server_error", failedErr.Type)
			require.Equal(t, "boom", failedErr.Message)
			require.Equal(t, llm.ErrorKindServer, failedErr.Kind)
		})
	})
}

func TestOpenAIResponsesStream(t *testing.T) {
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
		fixture := newOpenAIResponsesTestServer(t)
		fixture.streamBody = "event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test"}}` + "\n\n" +
			"event: response.in_progress\n" +
			`data: {"type":"response.in_progress","response":{"id":"resp_1","model":"gpt-test"}}` + "\n\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"Hi"}` + "\n\n" +
			"event: response.reasoning_summary_text.delta\n" +
			`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1",` +
			`"output_index":1,"delta":"Hmm"}` + "\n\n" +
			"event: response.output_item.added\n" +
			`data: {"type":"response.output_item.added","output_index":2,` +
			`"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read"}}` + "\n\n" +
			"event: response.function_call_arguments.delta\n" +
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1",` +
			`"output_index":2,"delta":"{\"path\":"}` + "\n\n" +
			"event: response.function_call_arguments.done\n" +
			`data: {"type":"response.function_call_arguments.done","item_id":"fc_1",` +
			`"output_index":2,"arguments":"{\"path\":\"a\"}"}` + "\n\n" +
			"event: response.output_item.done\n" +
			`data: {"type":"response.output_item.done","output_index":1,` +
			`"item":{"type":"reasoning","id":"rs_1","encrypted_content":"enc-9"}}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-test",` +
			`"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1"}],` +
			`"usage":{"input_tokens":30,"output_tokens":12,"total_tokens":42,` +
			`"input_tokens_details":{"cached_tokens":5,"cache_write_tokens":1},` +
			`"output_tokens_details":{"reasoning_tokens":4}}}}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "gpt-test"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		events := collect(t, stream)

		require.Equal(t, []llm.StreamEvent{
			{Type: llm.StreamMessageStart, ID: "resp_1", Model: "gpt-test"},
			{Type: llm.StreamTextDelta, Text: "Hi"},
			{Type: llm.StreamThinkingDelta, Thinking: "Hmm"},
			{Type: llm.StreamToolCallStart, ToolCallID: "call_1", ToolCallName: "read"},
			{
				Type:              llm.StreamToolCallArgsDelta,
				ToolCallID:        "call_1",
				ToolCallArgsDelta: `{"path":`,
			},
			{Type: llm.StreamThinkingDelta, ThinkingSignature: "enc-9"},
			{
				Type:       llm.StreamMessageEnd,
				StopReason: llm.StopReasonToolUse,
				Usage: llm.Usage{
					InputTokens: 30, OutputTokens: 12, ReasoningTokens: 4,
					CacheReadTokens: 5, CacheWriteTokens: 1,
				},
			},
		}, events)
		require.Equal(t, true, fixture.requestBody["stream"])
	})

	t.Run("surfaces failed responses", func(t *testing.T) {
		fixture := newOpenAIResponsesTestServer(t)
		fixture.streamBody = "event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"r","model":"m"}}` + "\n\n" +
			"event: response.failed\n" +
			`data: {"type":"response.failed","response":{"id":"r","status":"failed",` +
			`"error":{"code":"server_error","message":"boom"}}}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "m"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		event, err := stream.Next()
		require.NoError(t, err)
		require.Equal(t, llm.StreamMessageStart, event.Type)

		_, err = stream.Next()
		var streamErr *llm.Error
		require.ErrorAs(t, err, &streamErr)
		require.Equal(t, "server_error", streamErr.Type)
		require.Equal(t, llm.ErrorKindServer, streamErr.Kind)
	})

	t.Run("reports a truncated stream", func(t *testing.T) {
		fixture := newOpenAIResponsesTestServer(t)
		fixture.streamBody = "event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"r","model":"m"}}` + "\n\n"

		stream, err := fixture.client().Stream(t.Context(), &llm.Request{Model: "m"})
		require.NoError(t, err)
		defer func() { require.NoError(t, stream.Close()) }()

		event, err := stream.Next()
		require.NoError(t, err)
		require.Equal(t, llm.StreamMessageStart, event.Type)

		_, err = stream.Next()
		require.ErrorContains(t, err, "ended before completion")

		require.NoError(t, stream.Close())
	})

	t.Run("wraps read failures", func(t *testing.T) {
		client := NewOpenAIResponses(Config{
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

func TestOpenAIResponsesErrors(t *testing.T) {
	t.Run("maps HTTP failures to llm errors", func(t *testing.T) {
		fixture := newOpenAIResponsesTestServer(t)
		fixture.status = http.StatusUnauthorized
		fixture.response = `{"error": {"message": "bad key", "type": "invalid_request", "code": "invalid_api_key"}}`

		_, err := fixture.client().Generate(t.Context(), &llm.Request{Model: "m"})

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "openai-responses", providerErr.Provider)
		require.Equal(t, http.StatusUnauthorized, providerErr.StatusCode)
	})

	t.Run("rejects a nil request", func(t *testing.T) {
		fixture := newOpenAIResponsesTestServer(t)

		_, err := fixture.client().Generate(t.Context(), nil)
		require.Error(t, err)

		_, err = fixture.client().Stream(t.Context(), nil)
		require.Error(t, err)
	})
}

func TestOpenAIResponsesStopReasons(t *testing.T) {
	t.Run("derives reasons from output and status", func(t *testing.T) {
		withCall := &openAIResponsesResponse{
			Status: "completed",
			Output: []openAIResponsesOutputItem{{Type: "function_call"}},
		}
		require.Equal(t, llm.StopReasonToolUse, openAIResponsesStopReason(withCall))

		capped := &openAIResponsesResponse{
			Status:            "incomplete",
			IncompleteDetails: &openAIResponsesIncompleteDetails{Reason: "max_output_tokens"},
		}
		require.Equal(t, llm.StopReasonMaxTokens, openAIResponsesStopReason(capped))

		filtered := &openAIResponsesResponse{
			Status:            "incomplete",
			IncompleteDetails: &openAIResponsesIncompleteDetails{Reason: "content_filter"},
		}
		require.Equal(t, llm.StopReasonContentFilter, openAIResponsesStopReason(filtered))

		plain := &openAIResponsesResponse{Status: "completed"}
		require.Equal(t, llm.StopReasonEndTurn, openAIResponsesStopReason(plain))
	})
}
