package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// recordingClient records the harness session ID each call carries.
type recordingClient struct {
	generated []string
	streamed  []string
}

// Generate records the session ID of the context it receives.
func (c *recordingClient) Generate(ctx context.Context, _ *llm.Request) (*llm.Response, error) {
	id, _ := llm.SessionIDFromContext(ctx)
	c.generated = append(c.generated, id)
	return &llm.Response{}, nil
}

// Stream records the session ID of the context it receives.
func (c *recordingClient) Stream(ctx context.Context, _ *llm.Request) (llm.Stream, error) {
	id, _ := llm.SessionIDFromContext(ctx)
	c.streamed = append(c.streamed, id)
	return nil, errors.New("the recording client does not stream")
}

// TestSessionClient verifies the identity every client of a session carries, so
// a provider that routes a call by session accepts a summarization the same way
// it accepts a conversation turn.
func TestSessionClient(t *testing.T) {
	t.Run("attaches the session identity to every call", func(t *testing.T) {
		recorder := &recordingClient{}
		client := sessionClient{Client: recorder, sessionID: "session-1"}

		_, err := client.Generate(context.Background(), &llm.Request{})
		require.NoError(t, err)
		_, _ = client.Stream(context.Background(), &llm.Request{})

		require.Equal(t, []string{"session-1"}, recorder.generated)
		require.Equal(t, []string{"session-1"}, recorder.streamed)
	})

	t.Run("the session identity overrides an inherited one", func(t *testing.T) {
		recorder := &recordingClient{}
		client := sessionClient{Client: recorder, sessionID: "session-1"}

		_, err := client.Generate(llm.WithSessionID(context.Background(), "other"), &llm.Request{})
		require.NoError(t, err)

		require.Equal(t, []string{"session-1"}, recorder.generated)
	})
}
