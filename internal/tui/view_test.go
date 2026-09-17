package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
)

// plain strips the styling and the padding of rendered output.
func plain(text string) string {
	lines := strings.Split(ansi.Strip(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// TestView verifies the rendering of every phase of the interface.
func TestView(t *testing.T) {
	t.Run("renders the agent picker", func(t *testing.T) {
		m := newTestModel(
			t,
			[]agent.Agent{
				{ID: "coder", Description: "Writes code"},
				{ID: "writer", Description: "Writes prose"},
				{ID: "minimal"},
			},
			-1,
			func(string) (Session, error) { return newFakeSession(), nil },
		)

		view := plain(m.View())

		require.Contains(t, view, "Select an agent")
		require.Contains(t, view, "› coder")
		require.Contains(t, view, "Writes code")
		require.Contains(t, view, "writer")
		require.Contains(t, view, "Writes prose")
		require.Contains(t, view, "  minimal")
		require.Contains(t, view, "enter select")
	})

	t.Run("renders the session preparation", func(t *testing.T) {
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}},
			0,
			func(string) (Session, error) { return newFakeSession(), nil },
		)

		require.Contains(t, plain(m.View()), "preparing the session of coder")
	})

	t.Run("renders the conversation", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("hello")
		update(t, m, keyEnter)

		require.Contains(t, plain(m.View()), "working…")

		update(t, m, engineEventMsg{event: engine.Event{
			Type: engine.EventThinkingDelta,
			Text: "let me think",
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
			Arguments:  json.RawMessage(`{"command":"ls"}`),
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:       engine.EventToolOutput,
			ToolCallID: "call_1",
			Output:     "a.txt\n",
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "a.txt",
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type: engine.EventTextDelta,
			Text: "done",
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		}})

		view := plain(m.View())

		require.Contains(t, view, "rienda · coder · fake/test-model · session-1")
		require.Contains(t, view, "› hello")
		require.Contains(t, view, "let me think")
		require.Contains(t, view, `● shell {"command":"ls"}`)
		require.Contains(t, view, "a.txt")
		require.Contains(t, view, "done")
		require.Contains(t, view, "enter send")
	})

	t.Run("marks failed invocations", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, keyEnter)
		update(t, m, engineEventMsg{event: engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "boom",
			IsError:    true,
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		}})

		view := plain(m.View())

		require.Contains(t, view, "✗ shell")
		require.Contains(t, view, "  boom")
	})

	t.Run("marks truncated invocations", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)
		m.width = 40

		rendered := plain(m.renderToolEntry(entry{
			kind:      entryTool,
			toolName:  "shell",
			toolDone:  true,
			truncated: true,
			text:      "output",
		}, m.width))

		require.Contains(t, rendered, "● shell")
		require.Contains(t, rendered, "  output")
		require.Contains(t, rendered, "[output truncated]")
	})

	t.Run("renders failures", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)
		m.fatal = errors.New("boom")

		require.Contains(t, plain(m.View()), "error: boom")
	})
}

// TestWrap verifies text wrapping.
func TestWrap(t *testing.T) {
	t.Run("wraps long text to the width", func(t *testing.T) {
		wrapped := plain(wrap("one two three four five", 9))

		lines := strings.Split(wrapped, "\n")
		require.Greater(t, len(lines), 1)
		for _, line := range lines {
			require.LessOrEqual(t, len(line), 9)
		}
	})

	t.Run("keeps text untouched without a width", func(t *testing.T) {
		require.Equal(t, "abc", wrap("abc", 0))
	})
}

// TestClip verifies line clipping.
func TestClip(t *testing.T) {
	t.Run("keeps text untouched without a width", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)

		require.Equal(t, "hello", m.clip("hello"))
	})

	t.Run("clips text that exceeds the width", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)
		m.width = 4

		require.Equal(t, "hell", plain(m.clip("hello world")))
	})
}

// TestIndent verifies line indentation.
func TestIndent(t *testing.T) {
	require.Equal(t, "  a\n  b", indent("a\nb"))
}
