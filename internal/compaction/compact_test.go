package compaction

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// stubClient is a scripted llm.Client: Generate consumes the next response and
// records the requests it receives, while Stream is never used by the
// procedure.
type stubClient struct {
	responses []*llm.Response
	errs      []error
	requests  []*llm.Request
}

// Generate returns the next scripted response.
func (c *stubClient) Generate(_ context.Context, request *llm.Request) (*llm.Response, error) {
	c.requests = append(c.requests, request)
	index := len(c.requests) - 1

	if index < len(c.errs) && c.errs[index] != nil {
		return nil, c.errs[index]
	}
	if index < len(c.responses) {
		return c.responses[index], nil
	}
	return nil, errors.New("stubClient: unexpected generate call")
}

// Stream is never expected: the summarization is not streamed.
func (c *stubClient) Stream(context.Context, *llm.Request) (llm.Stream, error) {
	return nil, errors.New("stubClient: Stream is not scripted")
}

// summarizationResponse builds a scripted summary response.
func summarizationResponse(summary string) *llm.Response {
	return &llm.Response{
		Model:      "kimi-k2",
		Blocks:     []llm.Block{{Type: llm.BlockText, Text: summary}},
		StopReason: llm.StopReasonEndTurn,
		Usage:      llm.Usage{InputTokens: 120, OutputTokens: 40},
	}
}

// prepared builds a preparation of one user message.
func prepared() Preparation {
	return Preparation{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}},
		},
		KeptID:       "u1",
		TokensBefore: 42,
	}
}

// TestCompact verifies the summarization call.
func TestCompact(t *testing.T) {
	t.Run("issues one generate call carrying the prompt and the conversation", func(t *testing.T) {
		client := &stubClient{responses: []*llm.Response{summarizationResponse("the summary")}}

		result, err := Compact(t.Context(), prepared(), Deps{
			Client:    client,
			Model:     "kimi-k2",
			Prompt:    "summarize it",
			MaxTokens: 512,
		})

		require.NoError(t, err)
		require.Equal(t, "the summary", result.Summary)
		require.Equal(t, "u1", result.KeptID)
		require.Equal(t, 42, result.TokensBefore)
		require.Equal(t, "kimi-k2", result.Model)
		require.Equal(t, llm.Usage{InputTokens: 120, OutputTokens: 40}, result.Usage)

		require.Len(t, client.requests, 1)
		request := client.requests[0]
		require.Equal(t, "kimi-k2", request.Model)
		require.Equal(t, "summarize it", request.System)
		require.Equal(t, 512, request.MaxTokens)
		require.Nil(t, request.Thinking, "the summarization sends no thinking of its own")
		require.Len(t, request.Messages, 1)
		require.Equal(t, llm.RoleUser, request.Messages[0].Role)
		text := request.Messages[0].Blocks[0].Text
		require.Contains(t, text, "<conversation>")
		require.Contains(t, text, "[User]: hello")
		require.NotContains(t, text, "<previous-summary>")
	})

	t.Run("carries a previous summary as text", func(t *testing.T) {
		client := &stubClient{
			responses: []*llm.Response{summarizationResponse("the second summary")},
		}
		prep := prepared()
		prep.PreviousSummary = "the first summary"

		result, err := Compact(t.Context(), prep, Deps{Client: client, Prompt: "p"})

		require.NoError(t, err)
		require.Equal(t, "the second summary", result.Summary)

		text := client.requests[0].Messages[0].Blocks[0].Text
		require.Contains(t, text, "<previous-summary>\nthe first summary\n</previous-summary>")
		require.Less(
			t,
			len("<previous-summary>"),
			len(text),
			"the previous summary leads the conversation",
		)
	})

	t.Run("falls back to the requested model when the response names none", func(t *testing.T) {
		client := &stubClient{responses: []*llm.Response{{
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "summary"}},
		}}}

		result, err := Compact(
			t.Context(),
			prepared(),
			Deps{Client: client, Model: "session-model"},
		)

		require.NoError(t, err)
		require.Equal(t, "session-model", result.Model)
	})

	t.Run("retries a transient failure", func(t *testing.T) {
		client := &stubClient{
			errs: []error{
				&llm.Error{Provider: "test", Kind: llm.ErrorKindOverloaded, Message: "overloaded"},
				nil,
			},
			responses: []*llm.Response{nil, summarizationResponse("recovered")},
		}

		result, err := Compact(t.Context(), prepared(), Deps{Client: client})

		require.NoError(t, err)
		require.Equal(t, "recovered", result.Summary)
		require.Len(t, client.requests, 2)
	})

	t.Run("returns a permanent failure", func(t *testing.T) {
		client := &stubClient{errs: []error{&llm.Error{
			Provider: "test",
			Kind:     llm.ErrorKindAuthentication,
			Message:  "bad key",
		}}}

		_, err := Compact(t.Context(), prepared(), Deps{Client: client})

		require.ErrorContains(t, err, "bad key")
		require.Len(t, client.requests, 1)
	})

	t.Run("refuses an empty summary", func(t *testing.T) {
		client := &stubClient{responses: []*llm.Response{summarizationResponse("   ")}}

		_, err := Compact(t.Context(), prepared(), Deps{Client: client})

		require.ErrorContains(t, err, "empty summary")
	})

	t.Run("refuses a missing client and an empty preparation", func(t *testing.T) {
		_, err := Compact(t.Context(), prepared(), Deps{})
		require.ErrorContains(t, err, "client is required")

		_, err = Compact(t.Context(), Preparation{}, Deps{Client: &stubClient{}})
		require.ErrorContains(t, err, "nothing to summarize")
	})
}
