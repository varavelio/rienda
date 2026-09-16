package transport

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// errReader fails on every read.
type errReader struct{ err error }

func (r errReader) Read(_ []byte) (int, error) { return 0, r.err }

func TestSSEScanner(t *testing.T) {
	collect := func(t *testing.T, body string) []SSEEvent {
		t.Helper()
		scanner := NewSSEScanner(strings.NewReader(body))
		var events []SSEEvent
		for {
			event, err := scanner.Next()
			if errors.Is(err, io.EOF) {
				return events
			}
			require.NoError(t, err)
			events = append(events, event)
		}
	}

	t.Run("parses typed events with data", func(t *testing.T) {
		body := "event: message_start\ndata: {\"id\":\"1\"}\n\n" +
			"event: message_stop\ndata: {}\n\n"

		events := collect(t, body)

		require.Equal(t, []SSEEvent{
			{Name: "message_start", Data: `{"id":"1"}`},
			{Name: "message_stop", Data: "{}"},
		}, events)
	})

	t.Run("parses data-only lines with empty name", func(t *testing.T) {
		body := "data: {\"a\":1}\n\ndata: [DONE]\n\n"

		events := collect(t, body)

		require.Equal(t, []SSEEvent{
			{Data: `{"a":1}`},
			{Data: "[DONE]"},
		}, events)
	})

	t.Run("joins multiline data and skips comments and stray blanks", func(t *testing.T) {
		body := "\n: keep-alive\n\nevent: delta\ndata: line1\ndata: line2\n\n"

		events := collect(t, body)

		require.Equal(t, []SSEEvent{
			{Name: "delta", Data: "line1\nline2"},
		}, events)
	})

	t.Run("handles CRLF line endings and ignores unknown fields", func(t *testing.T) {
		body := "event: ping\r\nid: 42\r\nretry: 1000\r\ndata: ok\r\n\r\n"

		events := collect(t, body)

		require.Equal(t, []SSEEvent{{Name: "ping", Data: "ok"}}, events)
	})

	t.Run("flushes the final event without a trailing blank line", func(t *testing.T) {
		body := "event: last\ndata: tail"

		events := collect(t, body)

		require.Equal(t, []SSEEvent{{Name: "last", Data: "tail"}}, events)
	})

	t.Run("dispatches empty and colon-less data fields", func(t *testing.T) {
		events := collect(t, "data:\n\ndata\n\n")

		require.Equal(t, []SSEEvent{{Data: ""}, {Data: ""}}, events)
	})

	t.Run("dispatches an event name without data", func(t *testing.T) {
		events := collect(t, "event: ready\n\n")

		require.Equal(t, []SSEEvent{{Name: "ready"}}, events)
	})

	t.Run("keeps inner colons in data values", func(t *testing.T) {
		events := collect(t, "data: {\"a\":\"b:c\"}\n\n")

		require.Equal(t, []SSEEvent{{Data: `{"a":"b:c"}`}}, events)
	})

	t.Run("returns EOF when the stream only carries comments", func(t *testing.T) {
		scanner := NewSSEScanner(strings.NewReader(": ping\n\n"))

		_, err := scanner.Next()

		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("returns EOF on an empty stream", func(t *testing.T) {
		scanner := NewSSEScanner(strings.NewReader(""))

		_, err := scanner.Next()

		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("wraps reader failures", func(t *testing.T) {
		scanner := NewSSEScanner(errReader{err: errors.New("boom")})

		_, err := scanner.Next()

		require.Error(t, err)
		require.NotErrorIs(t, err, io.EOF)
		require.ErrorContains(t, err, "scan SSE stream")
	})
}
