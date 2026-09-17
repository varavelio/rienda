package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/session"
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
		update(t, m, windowMsg(80, 24))

		view := plain(m.render())

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

		require.Contains(t, plain(m.render()), "preparing the session of coder")
	})

	t.Run("renders the conversation", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("hello")
		update(t, m, pressEnter)

		require.Contains(t, plain(m.render()), "working…")

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

		view := plain(m.render())

		require.Contains(t, view, "rienda · coder · fake/test-model · session-1")
		require.Contains(t, view, "You")
		require.Contains(t, view, "hello")
		require.Contains(t, view, "Thinking")
		require.Contains(t, view, "let me think")
		require.Contains(t, view, `shell {"command":"ls"}`)
		require.Contains(t, view, "a.txt")
		require.Contains(t, view, "done")
		require.Contains(t, view, "enter send")
		require.Greater(
			t,
			strings.Index(view, "enter send"),
			strings.Index(view, "Ask the agent something"),
			"the footer belongs under the input",
		)
		require.GreaterOrEqual(t, strings.Count(view, "─"), 4, "separators and dividers")
	})

	t.Run("marks failed invocations", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
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

		view := plain(m.render())

		require.Contains(t, view, "shell")
		require.Contains(t, view, "boom")
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

		require.Contains(t, rendered, "shell")
		require.Contains(t, rendered, "output")
		require.Contains(t, rendered, "[output truncated]")
	})

	t.Run("starts empty", func(t *testing.T) {
		m, _ := chatModel(t)

		view := ansi.Strip(m.render())

		require.Len(t, strings.Split(view, "\n"), 24)
		require.NotContains(t, view, "A test agent")
	})

	t.Run("notices a scrolled transcript", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 30 {
			update(t, m, engineEventMsg{event: engine.Event{
				Type: engine.EventTextDelta,
				Text: "line\n",
			}})
		}
		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		}})

		require.NotContains(t, plain(m.render()), "↑ scrolled")

		update(t, m, pressPgUp)

		require.Contains(t, plain(m.render()), "↑ scrolled")
	})

	t.Run("renders failures", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)
		m.fatal = errors.New("boom")

		require.Contains(t, plain(m.render()), "error: boom")
	})

	t.Run("renders the start menu", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})

		view := plain(m.render())

		require.Contains(t, view, "What do you want to do?")
		require.Contains(t, view, "› New session")
		require.Contains(t, view, "  Continue a previous session")
		require.Contains(t, view, "enter select")
	})

	t.Run("renders the session list", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{
				{ID: "session-2", Agent: "coder", Title: "second"},
				{ID: "session-1", Agent: "coder", Title: "first"},
				{ID: "session-0", Agent: "coder"},
			},
		})
		m.phase = phaseSessions
		update(t, m, windowMsg(80, 24))

		view := plain(m.render())

		require.Contains(t, view, "Continue a previous session")
		require.Contains(t, view, "› second")
		require.Contains(t, view, "  first")
		require.Contains(t, view, "untitled session")
		require.Contains(t, view, "coder · ")
		require.Contains(t, view, "esc back")
	})

	t.Run("renders the input box", func(t *testing.T) {
		m, _ := chatModel(t)

		view := plain(m.render())

		require.Contains(t, view, "╭")
		require.Contains(t, view, "╰")
		require.Contains(t, view, "Ask the agent something")
	})

	t.Run("renders the alternate screen", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)

		view := m.View()

		require.True(t, view.AltScreen)
		require.Contains(t, plain(view.Content), "Select an agent")
	})

	t.Run("windows long session lists", func(t *testing.T) {
		sessions := make([]session.Info, 0, 20)
		for index := range 20 {
			sessions = append(sessions, session.Info{
				ID:    fmt.Sprintf("session-%d", index),
				Agent: "coder",
				Title: fmt.Sprintf("session %d", index),
			})
		}
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: sessions,
		})
		m.phase = phaseSessions
		update(t, m, windowMsg(80, 20))

		view := plain(m.render())
		require.Contains(t, view, "session 0")
		require.NotContains(t, view, "session 19")

		m.chosen = 19
		view = plain(m.render())
		require.Contains(t, view, "session 19")
		require.NotContains(t, view, "session 0")
	})
}

// TestSectionBlock verifies the layout of the conversation blocks.
func TestSectionBlock(t *testing.T) {
	styles := newStyles(true)

	t.Run("wraps the body under the label", func(t *testing.T) {
		block := styles.user.block(40, "You", "one two three four five six seven eight nine ten")
		lines := strings.Split(ansi.Strip(block), "\n")

		require.Equal(t, "You", lines[0])
		require.Equal(t, "", lines[1])
		require.True(t, strings.HasPrefix(lines[2], "  "))
		require.Contains(t, lines[2], "one two")
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), 40)
		}
	})

	t.Run("keeps an empty body to its label", func(t *testing.T) {
		require.Equal(t, "Thinking", ansi.Strip(styles.thinking.block(20, "Thinking", "")))
	})

	t.Run("keeps a label that already carries its styling", func(t *testing.T) {
		label := styles.tool.title.Render("shell") + " " + styles.dim.Render(`{"command":"ls"}`)

		require.Equal(t, label, styles.tool.titled(40, label, ""))
	})
}

// TestChatLayout verifies that the chat fills the terminal exactly.
func TestChatLayout(t *testing.T) {
	m, _ := chatModel(t)
	update(t, m, windowMsg(80, 30))
	m.input.SetValue("hello")
	update(t, m, pressEnter)
	update(t, m, engineEventMsg{event: engine.Event{Type: engine.EventTextDelta, Text: "hi"}})
	update(t, m, engineEventMsg{event: engine.Event{
		Type:   engine.EventRunEnd,
		Reason: engine.EndReasonTurn,
	}})

	view := ansi.Strip(m.render())
	lines := strings.Split(view, "\n")

	require.Len(t, lines, 30)
	for i, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), 80, "row %d", i)
	}
	require.Contains(t, view, "╰")
	require.Contains(t, lines[len(lines)-1], "enter send")
}

// TestVisibleWindow verifies the windowing of long lists.
func TestVisibleWindow(t *testing.T) {
	t.Run("returns the whole list when it fits", func(t *testing.T) {
		first, last := visibleWindow(0, 3, 5)

		require.Equal(t, 0, first)
		require.Equal(t, 3, last)
	})

	t.Run("keeps the cursor at the bottom edge", func(t *testing.T) {
		first, last := visibleWindow(9, 20, 5)

		require.Equal(t, 5, first)
		require.Equal(t, 10, last)
	})

	t.Run("never scrolls past the end", func(t *testing.T) {
		first, last := visibleWindow(19, 20, 5)

		require.Equal(t, 15, first)
		require.Equal(t, 20, last)
	})
}

// TestFormatAge verifies the age shown for stored sessions.
func TestFormatAge(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		moment time.Time
		want   string
	}{
		{name: "unknown moment", moment: time.Time{}, want: "unknown"},
		{name: "seconds", moment: now.Add(-10 * time.Second), want: "just now"},
		{name: "minutes", moment: now.Add(-5 * time.Minute), want: "5m ago"},
		{name: "hours", moment: now.Add(-3 * time.Hour), want: "3h ago"},
		{name: "days", moment: now.Add(-50 * time.Hour), want: "2d ago"},
		{
			name:   "months",
			moment: now.AddDate(0, -2, 0),
			want:   now.AddDate(0, -2, 0).Local().Format("Jan 2"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, formatAge(test.moment))
		})
	}
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
