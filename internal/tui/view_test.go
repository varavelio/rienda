package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
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
		require.Contains(t, view, "ctrl+c quit")
		require.NotContains(t, view, "esc back", "the picker opened alone has nowhere to return")
	})

	t.Run("advertises the way back from the picker only when there is one", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		update(t, m, windowMsg(80, 24))
		require.Equal(t, phaseStart, m.phase)

		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phasePicker, m.phase)

		require.Contains(t, plain(m.render()), "esc back")
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

	t.Run(
		"announces the quit key of the preparation screen once it is pending",
		func(t *testing.T) {
			m := newTestModel(
				t,
				[]agent.Agent{{ID: "coder"}},
				0,
				func(string) (Session, error) { return newFakeSession(), nil },
			)

			// The screen keeps to the session it is preparing until the user asks
			// to leave, which is when the key that leaves it has to show up.
			require.NotContains(t, plain(m.render()), "ctrl+c")

			update(t, m, pressCtrlC)

			require.Contains(t, plain(m.render()), "ctrl+c again to quit")
		},
	)

	t.Run("asks for a second press before quitting from the start list", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		update(t, m, windowMsg(80, 24))
		require.Equal(t, phaseStart, m.phase)
		require.Contains(t, plain(m.render()), "ctrl+c quit")

		update(t, m, pressCtrlC)

		view := plain(m.render())
		require.Equal(t, confirmQuit, m.confirm.action)
		require.Contains(t, view, "ctrl+c again to quit")
		require.NotContains(t, view, "type to filter", "the request takes the place of the hints")
	})

	t.Run("asks for a second press before quitting from the session tree", func(t *testing.T) {
		m, _ := treeModel(t, textMessage(llm.RoleUser, "fix the parser"))
		require.NotContains(
			t,
			plain(m.render()),
			"ctrl+c",
			"the tree footer does not advertise the key that leaves the interface",
		)

		update(t, m, pressCtrlC)

		view := plain(m.render())
		require.Contains(t, view, "ctrl+c again to quit")
		require.NotContains(t, view, "enter rewind", "the request takes the place of the hints")
	})

	t.Run("renders the session preparation without a selected agent", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: -1,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})

		update(t, m, windowMsg(80, 24))
		update(t, m, pressDown)
		cmd := update(t, m, pressEnter)

		// The runtime renders the preparation phase before the asynchronous
		// command that opens the session delivers its message, so the screen
		// must render without a selected agent.
		require.Equal(t, phasePreparing, m.phase)
		require.NotNil(t, cmd)
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
		require.Contains(t, view, "Agent: thinking")
		require.Contains(t, view, "let me think")
		require.Contains(t, view, `Tool: shell {"command":"ls"}`)
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

		require.Contains(t, lines, markerTurn+" Agent: coder", "the answer carries its label")
		require.Contains(t, lines, markerTurn+" You", "the prompt carries its label")
		require.Contains(t, lines, "plain answer", "the body aligns with the label")
	})

	t.Run("closes the turn with the time it took", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 40))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "all set"})
		m.runStart = time.Now().Add(-90 * time.Second)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		view := plain(m.render())
		footnote := "took " + formatElapsed(90*time.Second)

		require.Contains(t, view, footnote)
		require.Greater(
			t,
			strings.Index(view, footnote),
			strings.Index(view, "all set"),
			"the time closes the turn under the answer",
		)
		require.Contains(
			t,
			strings.Split(view, "\n"),
			footnote,
			"the footnote aligns with the body, without left padding",
		)
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
		typeFilter(t, m, "Render markdown")
		update(t, m, pressEnter)
		update(t, m, pressCtrlP)

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

		require.Contains(t, m.render(), "\x1b[1;92mAgent: coder", "the label keeps its green style")
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
		require.Contains(t, view, "Agent: thinking")
		require.Contains(t, view, "… step two")
		require.Contains(t, view, "step three")
		require.Contains(t, view, "step four")
		require.NotContains(t, view, "step one", "the earlier reasoning is dropped")
	})

	t.Run("keeps a collapsed block three rows tall", func(t *testing.T) {
		m, _ := chatModel(t)
		m.width = 40
		m.preferences = preferences{}

		// A single long paragraph must preview as three rows, not one row per
		// source line, so the block stays compact whatever the content shape.
		rendered := plain(m.renderThinkingEntry(&entry{
			kind:      entryThinking,
			fragments: []string{strings.Repeat("word ", 60)},
		}, m.width))

		rows := strings.Split(rendered, "\n")
		// The label, a blank row and the three rows of the preview.
		require.Len(t, rows, 5)
		require.Contains(t, rows[0], "Agent: thinking")
		require.Empty(t, rows[1])
		require.True(t, strings.HasPrefix(rows[2], "… "), "the preview marks the cut")
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

	t.Run("renders the start list", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		update(t, m, windowMsg(80, 24))

		view := plain(m.render())

		require.Contains(t, view, "Start a new session or continue a previous one")
		require.Contains(t, view, "Search sessions")
		require.Contains(t, view, "› New session")
		require.Contains(t, view, "  hello")
		require.Contains(t, view, "type to filter")
		require.Contains(t, view, "ctrl+c quit")
	})

	t.Run("renders the stored sessions of the start list", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{
				{ID: "session-2", Agent: "coder", Title: "second"},
				{ID: "session-1", Agent: "coder", Title: "first"},
				{ID: "session-0", Agent: "coder"},
			},
		})
		m.phase = phaseStart
		update(t, m, windowMsg(80, 24))

		view := plain(m.render())

		require.Contains(t, view, "› New session")
		require.Contains(t, view, "  second")
		require.Contains(t, view, "  first")
		require.Contains(t, view, "untitled session")
		require.Contains(t, view, "coder · ")
	})

	t.Run("renders the input box", func(t *testing.T) {
		m, _ := chatModel(t)

		view := plain(m.render())

		require.Contains(t, view, "╭")
		require.Contains(t, view, "╰")
		require.Contains(t, view, "Ask the agent something")
	})

	t.Run("renders the session tree", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "it is fixed"),
		)

		view := plain(m.render())

		require.Contains(t, view, "Session tree")
		require.Contains(t, view, "You: fix the parser")
		require.Contains(t, view, "Agent (coder): it is fixed")
		require.Contains(t, view, "└─", "a turn hangs from the turn it follows")
		require.Contains(t, view, "● You: fix the parser", "the branch the session runs is marked")
		require.Contains(
			t,
			view,
			"› ●",
			"the turn the session is at opens with the cursor and its mark",
		)
		require.Contains(t, view, "enter rewind")
		require.Contains(t, view, "ctrl+f/a/o fold")
		require.Contains(t, view, "esc back")
	})

	t.Run("marks the branch and the turn the session is at", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "done"),
		)

		view := m.render()

		require.Contains(
			t,
			view,
			"\x1b[2m●\x1b[m",
			"the turns of the branch the session runs carry a faint mark",
		)
		require.Contains(
			t,
			view,
			"\x1b[1;92m●\x1b[m",
			"the turn the session is at carries a bright mark",
		)
		require.Contains(
			t,
			view,
			"  \x1b[2m●\x1b[m \x1b[1;95mYou:\x1b[m",
			"the mark opens the row of the turn, before its author",
		)
	})

	t.Run("styles only the message of the highlighted row", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "done"),
		)

		view := m.render()

		require.Contains(
			t,
			view,
			"\x1b[1;92mAgent (coder):\x1b[m \x1b[1;94mdone\x1b[m",
			"the highlight styles the message, after the author keeps its own color",
		)
		require.Contains(
			t,
			view,
			"› \x1b[1;92m●\x1b[m ",
			"the highlighted row keeps the mark of the turn it holds",
		)
	})

	t.Run("renders the branch a turn of the tree opens", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "one"),
			textMessage(llm.RoleAssistant, "first"),
			textMessage(llm.RoleUser, "two"),
			textMessage(llm.RoleAssistant, "second"),
		)
		// The session returns to the first answer and writes again from it,
		// which opens a branch beside the turn that followed it.
		update(t, m, pressUp)
		update(t, m, pressUp)
		update(t, m, pressEnter)
		m.input.SetValue("again")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		update(t, m, pressCtrlT)

		view := plain(m.render())

		require.Contains(t, view, "You: one")
		require.Contains(t, view, "├─ You: two", "the turns the session left keep their branch")
		require.Contains(t, view, "└─ You: again", "the branch the session opened closes the group")
	})

	t.Run("shows the turns a folded turn hides", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "it is fixed"),
		)
		update(t, m, pressUp)

		update(t, m, pressCtrlF)

		view := plain(m.render())
		require.Contains(t, view, "⊟─ You: fix the parser", "the folded turn says so")
		require.NotContains(t, view, "it is fixed")
	})

	t.Run("shows the outline of a folded tree", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "it is fixed"),
		)

		update(t, m, pressCtrlA)

		view := plain(m.render())
		require.Contains(
			t,
			view,
			"⊟─ You: fix the parser",
			"the turn that opens the conversation says that it holds turns",
		)
		require.NotContains(t, view, "it is fixed")
	})

	t.Run("folds the turns beside the branch the session runs", func(t *testing.T) {
		m, _ := storeChat(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		// The session returns to the first answer and writes again from it,
		// which opens a branch beside the turn that followed it.
		update(t, m, pressCtrlT)
		update(t, m, pressUp)
		update(t, m, pressUp)
		update(t, m, pressEnter)
		m.input.SetValue("other")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		update(t, m, pressCtrlT)

		update(t, m, pressCtrlO)

		view := plain(m.render())
		require.Contains(t, view, "⊞─ You: second", "the turn the branch hangs from folds")
		require.Contains(t, view, "You: other", "the branch the session runs stays whole")
		require.NotContains(t, view, "two", "the turns under the folded branch hide")
	})

	t.Run("caps the message of a turn", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, strings.Repeat("word ", 200)),
			textMessage(llm.RoleAssistant, "done"),
		)

		for line := range strings.SplitSeq(m.render(), "\n") {
			require.LessOrEqual(
				t,
				ansi.StringWidth(line),
				80,
				"a long message never floods the tree",
			)
		}
		require.Contains(t, plain(m.render()), "done", "the marks of the turn stay visible")
	})

	t.Run("keeps the marks of a turn on a narrow row", func(t *testing.T) {
		m, _ := treeModel(t, textMessage(llm.RoleUser, strings.Repeat("word ", 40)))
		update(t, m, windowMsg(30, 24))

		view := plain(m.render())

		require.Contains(t, view, "You:", "a narrow row keeps the author of the turn")
		require.Contains(t, view, "› ● You:", "the cursor and the mark of the turn survive the cut")
		for line := range strings.SplitSeq(m.render(), "\n") {
			require.LessOrEqual(
				t,
				ansi.StringWidth(line),
				30,
				"the row never outgrows the terminal",
			)
		}
	})

	t.Run("caps the message however wide the terminal is", func(t *testing.T) {
		m, _ := treeModel(t, textMessage(llm.RoleUser, strings.Repeat("word ", 200)))
		update(t, m, windowMsg(200, 24))

		view := plain(m.render())
		row := ""
		for line := range strings.SplitSeq(view, "\n") {
			if strings.Contains(line, "You:") {
				row = line
			}
		}

		require.NotEmpty(t, row)
		require.LessOrEqual(
			t,
			ansi.StringWidth(row),
			treeMessageReserve+treeMessageMax,
			"the message stops growing at its cap",
		)
	})

	t.Run("shows the tag that labels a turn", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "done"),
		)
		turn := stored.store.Entries()[0]
		require.NoError(t, stored.store.SetTag(turn.ID, "bug"))
		m.buildTree()

		view := plain(m.render())
		require.Contains(t, view, "#bug")
		require.Regexp(
			t,
			`(?m)^  ● #bug `,
			view,
			"the tag follows the mark, before the author and the message",
		)
		require.Contains(t, m.render(), "\x1b[93m#bug", "the tag carries the color of the tags")
	})

	t.Run("renders the command center", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		update(t, m, pressCtrlP)

		view := plain(m.render())

		require.Contains(t, view, "Command center")
		require.Contains(t, view, "› New session")
		require.Contains(t, view, "Sessions")
		require.Contains(t, view, "Expand tool output")
		require.Contains(t, view, "[off]")
		require.Contains(t, view, "Expand thinking")
		require.Contains(t, view, "Render markdown")
		require.Contains(t, view, "enter run")
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

	t.Run("renders the alternate screen and reports the wheel", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, -1, nil)

		view := m.View()

		require.True(t, view.AltScreen)
		// Reporting the mouse is what turns the wheel into a message of its
		// own instead of the arrow keys a terminal translates it to when the
		// mouse is not reported.
		require.Equal(t, tea.MouseModeCellMotion, view.MouseMode)
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
		m.phase = phaseStart
		update(t, m, windowMsg(80, 20))

		view := plain(m.render())
		require.Contains(t, view, "session 0")
		require.NotContains(t, view, "session 19")

		// The offer of a new session leads the list, so the last stored
		// session sits at the position after the whole list of them.
		m.start.cursor = 20
		view = plain(m.render())
		require.Contains(t, view, "session 19")
		require.NotContains(t, view, "session 0")
	})
}

// TestTailPreview verifies the preview of the trailing rows a collapsed block
// shows.
func TestTailPreview(t *testing.T) {
	t.Run("returns an empty preview for empty text", func(t *testing.T) {
		require.Empty(t, tailPreview("", 3, 40))
		require.Empty(t, tailPreview("   \n", 3, 40))
	})

	t.Run("keeps a text that fits", func(t *testing.T) {
		require.Equal(t, "one\ntwo", tailPreview("one\ntwo", 3, 40))
	})

	t.Run("keeps the last rows and marks the cut", func(t *testing.T) {
		text := "one\ntwo\nthree\nfour"

		require.Equal(t, "… two\nthree\nfour", tailPreview(text, 3, 40))
	})

	t.Run("counts the rows the terminal shows, not the source lines", func(t *testing.T) {
		// A single long paragraph wraps into many rows: the preview keeps the
		// last three of them, so the block stays a steady three rows tall.
		text := strings.Repeat("word ", 60)
		preview := tailPreview(text, 3, 40)

		rows := strings.Split(preview, "\n")
		require.Len(t, rows, 3, "the preview is three visible rows")
		require.True(t, strings.HasPrefix(rows[0], "… "), "the cut is marked")
		for _, row := range rows {
			require.LessOrEqual(t, ansi.StringWidth(row), 40)
		}
	})

	t.Run("ignores trailing blank lines", func(t *testing.T) {
		require.Equal(t, "only", tailPreview("only\n\n\n", 3, 40))
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

	t.Run("breathes around the notice of a branch", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		update(t, m, pressUp)
		update(t, m, pressUp)
		update(t, m, pressEnter)

		lines := strings.Split(plain(m.render()), "\n")
		notice := -1
		for index, line := range lines {
			if strings.Contains(line, "rewound") {
				notice = index
			}
		}
		require.Positive(t, notice, "the notice is shown")
		require.Empty(t, strings.TrimSpace(lines[notice-1]), "a blank row above the notice")
		require.Empty(t, strings.TrimSpace(lines[notice+1]), "a blank row below the notice")

		// The block keeps a single row once the message is sent, so the
		// conversation grows into the rows the notice leaves.
		m.input.SetValue("again")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.NotContains(t, plain(m.render()), "rewound")
		require.Equal(t, 1, m.activityHeight(), "the notice gives its rows back")
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

// TestFormatElapsed verifies how long a turn took, rounded to the nearest
// second so the count stays steady while a run streams.
func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "under half a second rounds down", d: 400 * time.Millisecond, want: "0s"},
		{name: "over half a second rounds up", d: 1500 * time.Millisecond, want: "2s"},
		{name: "seconds", d: 8 * time.Second, want: "8s"},
		{name: "a whole minute", d: time.Minute, want: "1m0s"},
		{name: "minutes and seconds", d: 65 * time.Second, want: "1m5s"},
		{name: "a long turn", d: 90 * time.Second, want: "1m30s"},
		{name: "a whole hour", d: 3 * time.Hour, want: "3h0m0s"},
		{name: "beyond a day", d: 50 * time.Hour, want: "50h0m0s"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, formatElapsed(test.d))
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
	history := make([]session.Entry, 0, benchHistoryBlocks)
	for index := range benchHistoryBlocks {
		role := llm.RoleAssistant
		if index%2 == 0 {
			role = llm.RoleUser
		}
		history = append(history, session.Entry{
			Message: llm.Message{
				Role: role,
				Blocks: []llm.Block{
					{Type: llm.BlockText, Text: strings.Repeat("a previous answer.\n", 200)},
				},
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

// TestContextLabel verifies the context figure of the chat footer.
func TestContextLabel(t *testing.T) {
	m, _ := chatModel(t)
	m.context = tokens.Report{Used: 68000, Window: 200000}

	tests := []struct {
		name    string
		percent int
		style   lipgloss.Style
	}{
		{name: "fits the window", percent: 34, style: m.styles.footer},
		{
			name:    "reaches the warning threshold",
			percent: contextWarningPercent,
			style:   m.styles.footer,
		},
		{
			name:    "passes the warning threshold",
			percent: contextWarningPercent + 1,
			style:   m.styles.notice,
		},
		{
			name:    "reaches the critical threshold",
			percent: contextCriticalPercent,
			style:   m.styles.notice,
		},
		{
			name:    "passes the critical threshold",
			percent: contextCriticalPercent + 1,
			style:   m.styles.errorText,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m.context.Percent = test.percent

			require.Equal(
				t,
				test.style.Render(fmt.Sprintf("ctx %d%% · 68k/200k", test.percent)),
				m.contextLabel(),
			)
		})
	}

	t.Run("is empty without a window", func(t *testing.T) {
		m.context = tokens.Report{}

		require.Empty(t, m.contextLabel())
	})

	t.Run("reaches the footer", func(t *testing.T) {
		m.context = tokens.Report{Used: 68000, Window: 200000, Percent: 34}

		require.Contains(t, plain(m.render()), "ctx 34% · 68k/200k")
	})
}

// TestFormatTokens verifies the token counts of the chat footer.
func TestFormatTokens(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{count: 0, want: "0"},
		{count: 1, want: "1"},
		{count: 999, want: "999"},
		{count: 1000, want: "1k"},
		{count: 1050, want: "1.1k"},
		{count: 1496, want: "1.5k"},
		{count: 1500, want: "1.5k"},
		{count: 96432, want: "96.4k"},
		{count: 100000, want: "100k"},
		{count: 250000, want: "250k"},
		{count: 262144, want: "262.1k"},
		// The rounding reaches the next unit, which renders as a million rather
		// than as 1000k.
		{count: 999949, want: "999.9k"},
		{count: 999950, want: "1m"},
		{count: 1000000, want: "1m"},
		{count: 1000123, want: "1m"},
		{count: 1049000, want: "1m"},
		{count: 1050000, want: "1.1m"},
		{count: 1500000, want: "1.5m"},
		{count: 2000000, want: "2m"},
		{count: 10500000, want: "10.5m"},
		{count: 10500786, want: "10.5m"},
	}

	for _, test := range tests {
		t.Run(strconv.Itoa(test.count), func(t *testing.T) {
			require.Equal(t, test.want, formatTokens(test.count))
		})
	}
}

// TestCompactionRendering verifies the checkpoint block of the conversation.
func TestCompactionRendering(t *testing.T) {
	t.Run("renders the block with its label, body and color", func(t *testing.T) {
		m, _ := chatModel(t)
		m.preferences = showAllPreferences()
		m.transcript.addCompaction()
		m.refreshTranscript()

		rendered := m.render()
		view := plain(rendered)

		require.Contains(t, view, "Compaction")
		require.Contains(t, view, compactionBody)
		require.Contains(
			t,
			rendered,
			m.styles.compaction.title.Render("Compaction"),
			"the label carries the color of a checkpoint",
		)
		require.Contains(
			t,
			rendered,
			m.styles.compaction.title.Render("Compaction"),
			"white is the color of a checkpoint",
		)
	})

	t.Run("renders a checkpoint carrying a tag without losing either color", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		first := stored.store.Branch()[0]
		require.NoError(t, stored.store.SetTag(first.ID, "bug"))
		_, err := stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			first.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)

		// The tag and the checkpoint label live on different rows, so both
		// colors survive: the tag in the tree and the label in the
		// conversation.
		m.reloadTranscript()
		rendered := m.render()

		require.Contains(t, plain(rendered), compactionBody)
		require.Contains(
			t,
			rendered,
			m.styles.compaction.title.Render("Compaction"),
			"the checkpoint keeps its white",
		)
	})
}

// TestCommandLine verifies the rendering of the command center entries.
func TestCommandLine(t *testing.T) {
	t.Run("keeps the cursor marker on a disabled command and explains why", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.refused = true
		scripted.refusal = compaction.Refusal{Kind: compaction.RefusalShort, Needed: 20000}
		update(t, m, windowSize)

		position := commandPosition(t, m, "Compact context")
		m.commands.cursor = position

		line := m.commandLine(position)

		require.Contains(t, plain(line), "needs 20k more tokens of history")
		require.Contains(
			t,
			line,
			cursorMark(true),
			"the cursor stays visible on a command the user cannot run",
		)
		require.Contains(
			t,
			line,
			m.styles.dim.Render("Compact context  needs 20k more tokens of history"),
			"a disabled command stays faint",
		)
		require.NotContains(t, line, "› "+m.styles.selected.Render("Compact context"))
	})

	t.Run(
		"leaves the cursor blank on a disabled command the selection is away from",
		func(t *testing.T) {
			m, scripted := chatModel(t)
			scripted.refused = true
			scripted.refusal = compaction.Refusal{Kind: compaction.RefusalShort, Needed: 20000}
			update(t, m, windowSize)

			position := commandPosition(t, m, "Compact context")
			m.commands.cursor = 0

			line := m.commandLine(position)

			require.Contains(t, line, cursorMark(false))
			require.NotContains(t, line, cursorMark(true))
		},
	)

	t.Run("renders an enabled command with its own note", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowSize)

		position := commandPosition(t, m, "Compact context")

		require.Contains(
			t,
			plain(m.commandLine(position)),
			"summarize the oldest turns into a checkpoint",
		)
	})

	t.Run("highlights the command the selection rests on", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowSize)

		position := commandPosition(t, m, "New session")
		m.commands.cursor = position

		line := m.commandLine(position)

		require.Contains(t, line, cursorMark(true))
		require.Contains(t, line, m.styles.selected.Render("New session"))
	})
}

// windowSize is a terminal large enough for the whole command center.
var windowSize = windowMsg(80, 24)

// commandPosition returns the position of the command with the given label in
// the list the command center shows.
func commandPosition(t *testing.T, m *model, label string) int {
	t.Helper()

	for position, index := range m.commands.shown {
		if commandList[index].Label == label {
			return position
		}
	}
	t.Fatalf("the command center holds no command %q", label)
	return -1
}

// TestSettingsInput verifies the input row of the command center, which shows
// the query that narrows the commands or the input that names the session.
func TestSettingsInput(t *testing.T) {
	t.Run("shows the query of the commands by default", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, pressCtrlP)
		update(t, m, windowSize)

		require.Contains(t, plain(m.render()), "Search commands")
		require.Contains(t, plain(m.render()), "type to filter · ↑/↓ move · enter run · esc close")
	})

	t.Run("shows the name of the session while it is edited", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.info.Title = "named"
		scripted.info.Named = true
		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		update(t, m, windowSize)

		view := plain(m.render())

		require.Contains(t, view, "name: named")
		require.NotContains(t, view, "Search commands", "the query input gives way to the name")
		require.Contains(t, view, "type a name · enter save · esc cancel")
	})

	t.Run("reports the failure of the session in place of the hints", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.titleErr = errors.New("boom")
		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		typeName(t, m, "named")
		update(t, m, pressEnter)
		update(t, m, windowSize)

		view := plain(m.render())

		require.Contains(t, view, "error: boom")
		require.Contains(t, view, "esc cancel")
		require.NotContains(t, view, "enter save", "the failure takes the place of the hints")
	})
}
