package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
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

// showAllPreferences returns the preferences that expand every collapsible
// block and keep the markdown formatting, so the tests assert the full
// rendering.
func showAllPreferences() preferences {
	return preferences{ExpandToolOutput: true, ExpandThinking: true, RenderMarkdown: true}
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
		m.preferences = showAllPreferences()
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("hello")
		update(t, m, pressEnter)

		require.Contains(t, plain(m.render()), "working")

		sendEvent(t, m, engine.Event{
			Type: engine.EventThinkingDelta,
			Text: "let me think",
		})
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
			Arguments:  json.RawMessage(`{"command":"ls"}`),
		})
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolOutput,
			ToolCallID: "call_1",
			Output:     "a.txt\n",
		})
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "a.txt",
		})
		sendEvent(t, m, engine.Event{
			Type: engine.EventTextDelta,
			Text: "done",
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

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

	t.Run("renders markdown in the answers of the model", func(t *testing.T) {
		m, _ := chatModel(t)
		m.preferences = showAllPreferences()
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{
			Type: engine.EventTextDelta,
			Text: "# Title\n\n- first\n- second\n\nUse `go test` and **bold**.",
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		view := strings.ReplaceAll(plain(m.render()), "\u00a0", " ")

		require.Contains(t, view, "Title")
		require.NotContains(t, view, "# Title", "the heading marker is consumed")
		require.Contains(t, view, "• first")
		require.Contains(t, view, "go test", "inline code keeps its text")
		require.Contains(t, view, "bold", "emphasis keeps its text")
		require.NotContains(t, view, "**")
		require.NotContains(t, view, "`")
	})

	t.Run("aligns the answer with its label", func(t *testing.T) {
		m, _ := chatModel(t)
		m.preferences = showAllPreferences()
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "plain answer"})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		lines := strings.Split(plain(m.render()), "\n")

		require.Contains(t, lines, markerTurn+" coder", "the answer carries its label")
		require.Contains(t, lines, markerTurn+" You", "the prompt carries its label")
		require.Contains(t, lines, "plain answer", "the body aligns with the label")
	})

	t.Run("shows raw markdown when the rendering is disabled", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "# Title"})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})
		require.NotContains(t, plain(m.render()), "# Title", "the heading renders as markdown")

		update(t, m, pressCtrlP)
		update(t, m, pressDown)
		update(t, m, pressDown)
		update(t, m, pressSpace)
		update(t, m, pressEscape)

		require.Contains(t, plain(m.render()), "# Title", "the raw markdown shows again")
	})

	t.Run("keeps the color of the answer label", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "answer"})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		require.Contains(t, m.render(), "\x1b[1;92mcoder", "the label keeps its green style")
	})

	t.Run("marks failed invocations", func(t *testing.T) {
		m, _ := chatModel(t)
		m.preferences = showAllPreferences()
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
		})
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "boom",
			IsError:    true,
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		view := plain(m.render())

		require.Contains(t, view, "shell")
		require.Contains(t, view, "boom")
	})

	t.Run("previews the last lines of the tool output by default", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolCall,
			ToolCallID: "call_1",
			ToolName:   "shell",
			Arguments:  json.RawMessage(`{"command":"ls"}`),
		})
		for _, line := range []string{"one", "two", "three", "four", "five"} {
			sendEvent(t, m, engine.Event{
				Type:       engine.EventToolOutput,
				ToolCallID: "call_1",
				Output:     line + "\n",
			})
		}
		sendEvent(t, m, engine.Event{
			Type:       engine.EventToolResult,
			ToolCallID: "call_1",
			Text:       "five",
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		view := plain(m.render())

		require.Contains(t, view, `shell {"command":"ls"}`)
		require.Contains(t, view, "… three", "the preview is marked as a fragment")
		require.Contains(t, view, "four")
		require.Contains(t, view, "five")
		require.NotContains(t, view, "one", "the earlier lines are dropped")
		require.NotContains(t, view, "two")
	})

	t.Run("previews the last lines of the reasoning by default", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{
			Type: engine.EventThinkingDelta,
			Text: "step one\nstep two\nstep three\nstep four",
		})

		view := plain(m.render())
		require.Contains(t, view, "Thinking")
		require.Contains(t, view, "… step two")
		require.Contains(t, view, "step three")
		require.Contains(t, view, "step four")
		require.NotContains(t, view, "step one", "the earlier reasoning is dropped")
	})

	t.Run("marks truncated invocations", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)
		m.width = 40
		m.preferences.ExpandToolOutput = true

		rendered := plain(m.renderToolEntry(&entry{
			kind:      entryTool,
			fragments: []string{"output"},
			toolName:  "shell",
			toolDone:  true,
			truncated: true,
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
			sendEvent(t, m, engine.Event{
				Type: engine.EventTextDelta,
				Text: "line\n\n",
			})
		}
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

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

	t.Run("renders the command center", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		update(t, m, pressCtrlP)

		view := plain(m.render())

		require.Contains(t, view, "Command center")
		require.Contains(t, view, "› Expand tool output")
		require.Contains(t, view, "[off]")
		require.Contains(t, view, "Expand thinking")
		require.Contains(t, view, "Render markdown")
		require.Contains(t, view, "enter toggle")
		require.Contains(t, view, "esc close")
	})

	t.Run("opens every identity line with the brand logo", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))

		require.Contains(t, plain(m.render()), varavelLogo+" \u00b7 varavel rienda")

		menu := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		require.Contains(t, plain(menu.render()), varavelLogo+" \u00b7 varavel rienda")
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

// TestTailPreview verifies the preview of the trailing lines a collapsed block
// shows.
func TestTailPreview(t *testing.T) {
	t.Run("returns an empty preview for empty text", func(t *testing.T) {
		require.Empty(t, tailPreview("", 3))
		require.Empty(t, tailPreview("   \n", 3))
	})

	t.Run("keeps a text that fits", func(t *testing.T) {
		require.Equal(t, "one\ntwo", tailPreview("one\ntwo", 3))
	})

	t.Run("keeps the last lines and marks the cut", func(t *testing.T) {
		text := "one\ntwo\nthree\nfour"

		require.Equal(t, "… two\nthree\nfour", tailPreview(text, 3))
	})

	t.Run("ignores trailing blank lines", func(t *testing.T) {
		require.Equal(t, "only", tailPreview("only\n\n\n", 3))
	})
}

// TestSectionBlock verifies the layout of the conversation blocks.
func TestSectionBlock(t *testing.T) {
	styles := newStyles(true)

	t.Run("opens the label with a marker and aligns the body to it", func(t *testing.T) {
		block := styles.user.block(40, "You", "one two three four five six seven eight nine ten")
		lines := strings.Split(ansi.Strip(block), "\n")

		require.Equal(t, markerTurn+" You", lines[0])
		require.Equal(t, "", lines[1])
		require.False(t, strings.HasPrefix(lines[2], " "), "the body aligns with the label")
		require.Contains(t, lines[2], "one two")
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), 40)
		}
	})

	t.Run("keeps an empty body to its label", func(t *testing.T) {
		require.Equal(
			t,
			markerActivity+" Thinking",
			ansi.Strip(styles.thinking.block(20, "Thinking", "")),
		)
	})

	t.Run("keeps a label that already carries its styling", func(t *testing.T) {
		label := styles.tool.title.Render("shell") + " " + styles.dim.Render(`{"command":"ls"}`)

		require.Equal(
			t,
			styles.tool.markerStyle.Render(markerActivity)+" "+label,
			styles.tool.titled(40, label, ""),
		)
	})

	t.Run("colors the marker with the label", func(t *testing.T) {
		block := styles.user.block(40, "You", "")

		require.Equal(
			t,
			styles.user.markerStyle.Render(markerTurn)+" "+styles.user.title.Render("You"),
			block,
		)
	})

	t.Run("wraps a label that does not fit", func(t *testing.T) {
		label := styles.tool.title.Render("shell") + " " + styles.dim.Render(
			`{"command":"a very long command with several arguments"}`,
		)

		lines := strings.Split(ansi.Strip(styles.tool.titled(20, label, "")), "\n")

		require.Greater(t, len(lines), 1)
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), 20)
		}
	})
}

// TestToolLabel verifies the label shown for a tool invocation.
func TestToolLabel(t *testing.T) {
	t.Run("compacts the arguments into one line", func(t *testing.T) {
		require.Equal(t, `{ "a": 1, "b": 2 }`, toolLabel("{\n  \"a\": 1,\n  \"b\": 2\n}"))
	})

	t.Run("caps what it shows", func(t *testing.T) {
		label := toolLabel(strings.Repeat("x", maxToolLabel+50))

		require.LessOrEqual(t, ansi.StringWidth(label), maxToolLabel)
		require.True(t, strings.HasSuffix(label, "…"))
	})
}

// TestChatLayout verifies that the chat fills the terminal exactly.
func TestChatLayout(t *testing.T) {
	t.Run("fills the terminal", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 30))
		m.input.SetValue("hello")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "hi"})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		view := ansi.Strip(m.render())
		lines := strings.Split(view, "\n")

		require.Len(t, lines, 30)
		for i, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), 80, "row %d", i)
		}
		require.Contains(t, view, "╰")
		require.Contains(t, lines[len(lines)-1], "enter send")
	})

	t.Run("fits every size, wrapping the long titles", func(t *testing.T) {
		sizes := []struct{ width, height int }{
			{100, 30},
			{80, 24},
			{60, 20},
			{40, 16},
			{24, 12},
		}

		for _, size := range sizes {
			m, _ := chatModel(t)
			m.preferences = showAllPreferences()
			update(t, m, windowMsg(size.width, size.height))
			m.input.SetValue("go")
			update(t, m, pressEnter)
			sendEvent(t, m, engine.Event{
				Type:       engine.EventToolCall,
				ToolCallID: "call_1",
				ToolName:   "shell",
				Arguments: json.RawMessage(
					`{"command":"` + strings.Repeat("verylongword ", 20) + `"}`,
				),
			})
			sendEvent(t, m, engine.Event{
				Type:       engine.EventToolResult,
				ToolCallID: "call_1",
				Text:       strings.Repeat("some output text ", 40),
			})

			view := ansi.Strip(m.render())
			lines := strings.Split(view, "\n")

			require.Len(t, lines, size.height, "size %dx%d", size.width, size.height)
			for index, line := range lines {
				require.LessOrEqual(
					t,
					ansi.StringWidth(line),
					size.width,
					"size %dx%d row %d",
					size.width,
					size.height,
					index,
				)
			}
			require.Contains(t, view, "working")
		}
	})

	t.Run("breathes around the status line", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(60, 20))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		update(t, m, spinnerTickMsg{})

		lines := strings.Split(plain(m.render()), "\n")
		status := -1
		for index, line := range lines {
			if strings.Contains(line, "working") {
				status = index
			}
		}
		require.Positive(t, status, "the status line is shown")
		require.Empty(t, strings.TrimSpace(lines[status-1]), "a blank row above the status")
		require.Empty(t, strings.TrimSpace(lines[status-2]), "a second blank row above the status")
		require.Empty(t, strings.TrimSpace(lines[status+1]), "a blank row below the status")

		// The status block keeps its height when the run ends, so the layout
		// does not jump under the reader.
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		require.Len(t, strings.Split(plain(m.render()), "\n"), len(lines))
	})

	t.Run("pads every line to the terminal width", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(40, 16))

		for line := range strings.SplitSeq(m.render(), "\n") {
			require.Equal(
				t,
				ansi.StringWidth(ansi.Strip(line)),
				40,
				"every line fills the width so a shrinking line cannot leave a tail",
			)
		}
	})

	t.Run("rewraps the conversation after a resize", func(t *testing.T) {
		m, _ := chatModel(t)
		m.preferences = showAllPreferences()
		update(t, m, windowMsg(80, 30))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{
			Type: engine.EventTextDelta,
			Text: strings.Repeat("word ", 200),
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})
		require.True(t, m.conversation.atBottom())

		update(t, m, windowMsg(40, 20))

		view := ansi.Strip(m.render())
		lines := strings.Split(view, "\n")

		require.Len(t, lines, 20)
		for index, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), 40, "row %d", index)
		}
		require.True(t, m.conversation.atBottom())
		require.Contains(t, lines[len(lines)-1], "enter send")
	})
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

// Shape of the stream the rendering benchmark replays: bursts of chunks over a
// transcript seeded with a previous conversation.
const (
	benchChunk         = "the agent streams a short sentence "
	benchBurst         = 10
	benchResetEvery    = 200
	benchHistoryBlocks = 50
)

// BenchmarkStreamBurst measures the cost of folding one burst of streamed
// chunks, which is the unit of work of a fast stream. The transcript holds a
// previous conversation, as a long session does.
func BenchmarkStreamBurst(b *testing.B) {
	history := make([]llm.Message, 0, benchHistoryBlocks)
	for index := range benchHistoryBlocks {
		role := llm.RoleAssistant
		if index%2 == 0 {
			role = llm.RoleUser
		}
		history = append(history, llm.Message{
			Role: role,
			Blocks: []llm.Block{
				{Type: llm.BlockText, Text: strings.Repeat("a previous answer.\n", 200)},
			},
		})
	}

	scripted := newFakeSession()
	m := newModel(modelConfig{
		agents:   []agent.Agent{{ID: "coder", Description: "A test agent"}},
		selected: 0,
		newSession: func(string) (Session, error) {
			return scripted, nil
		},
		newRunContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	})
	m.Update(m.Init()())
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.input.SetValue("go")
	m.Update(pressEnter)

	burst := make(engineEventsMsg, benchBurst)
	for index := range burst {
		burst[index] = engine.Event{Type: engine.EventTextDelta, Text: benchChunk}
	}

	for i := 0; b.Loop(); i++ {
		if i%benchResetEvery == 0 {
			m.transcript = transcript{}
			m.transcript.load(history)
		}
		m.Update(burst)
	}
}
