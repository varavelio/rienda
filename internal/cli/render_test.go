package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/tool"
)

// eventsOf builds a closed channel carrying events.
func eventsOf(events ...engine.Event) <-chan engine.Event {
	channel := make(chan engine.Event, len(events))
	for _, event := range events {
		channel <- event
	}
	close(channel)
	return channel
}

// TestRender verifies run rendering.
func TestRender(t *testing.T) {
	t.Run("writes the assistant text to stdout", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(eventsOf(
			engine.Event{Type: engine.EventRunStart},
			engine.Event{Type: engine.EventTextDelta, Text: "hel"},
			engine.Event{Type: engine.EventTextDelta, Text: "lo"},
			engine.Event{Type: engine.EventMessageEnd},
			engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn},
		), stdout, stderr)

		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout.String())
		require.Empty(t, stderr.String())
	})

	t.Run("keeps complete lines untouched", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(eventsOf(
			engine.Event{Type: engine.EventTextDelta, Text: "line\n"},
			engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn},
		), stdout, stderr)

		require.NoError(t, err)
		require.Equal(t, "line\n", stdout.String())
	})

	t.Run("writes tool activity to stderr", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(eventsOf(
			engine.Event{
				Type:       engine.EventToolCall,
				ToolCallID: "call_1",
				ToolName:   "shell",
				Arguments:  json.RawMessage(`{"command":"ls"}`),
			},
			engine.Event{
				Type:       engine.EventToolOutput,
				ToolCallID: "call_1",
				Stream:     tool.StreamStdout,
				Output:     "file.txt\n",
			},
			engine.Event{
				Type:       engine.EventToolResult,
				ToolCallID: "call_1",
				ToolName:   "shell",
				Text:       "file.txt",
			},
			engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn},
		), stdout, stderr)

		require.NoError(t, err)
		require.Empty(t, stdout.String())
		require.Equal(t, "tool: shell {\"command\":\"ls\"}\nfile.txt\n", stderr.String())
	})

	t.Run("does not repeat tool failures that streamed output", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(eventsOf(
			engine.Event{Type: engine.EventToolCall, ToolCallID: "call_1", ToolName: "shell"},
			engine.Event{Type: engine.EventToolOutput, ToolCallID: "call_1", Output: "boom\n"},
			engine.Event{
				Type:       engine.EventToolResult,
				ToolCallID: "call_1",
				ToolName:   "shell",
				Text:       "boom",
				IsError:    true,
			},
			engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn},
		), stdout, stderr)

		require.NoError(t, err)
		require.Equal(t, "tool: shell\nboom\ntool failed\n", stderr.String())
	})

	t.Run("writes tool results that did not stream", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(eventsOf(
			engine.Event{Type: engine.EventToolCall, ToolCallID: "call_1", ToolName: "ghost"},
			engine.Event{
				Type:       engine.EventToolResult,
				ToolCallID: "call_1",
				ToolName:   "ghost",
				Text:       `unknown tool "ghost"`,
				IsError:    true,
			},
			engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn},
		), stdout, stderr)

		require.NoError(t, err)
		require.Equal(
			t,
			"tool: ghost\ntool failed: unknown tool \"ghost\"\n",
			stderr.String(),
		)
	})

	t.Run("reports run failures", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(eventsOf(
			engine.Event{Type: engine.EventError, Error: "boom"},
			engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonError},
		), stdout, stderr)

		require.EqualError(t, err, "boom")
	})

	t.Run("reports turn limits", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(
			eventsOf(engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonMaxTurns}),
			stdout,
			stderr,
		)

		require.EqualError(t, err, "the run reached the turn limit")
	})

	t.Run("reports interruptions", func(t *testing.T) {
		stdout, stderr := &strings.Builder{}, &strings.Builder{}

		err := render(
			eventsOf(engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonInterrupted}),
			stdout,
			stderr,
		)

		require.EqualError(t, err, "the run was interrupted")
	})
}
