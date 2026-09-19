package tui

import (
	"context"
	"encoding/json"
	"errors"
	"image/color"
	"strings"
	"sync"
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

// fakeSession is a scripted Session implementation.
type fakeSession struct {
	info         session.Info
	history      []llm.Message
	events       chan engine.Event
	prompts      []string
	canceled     chan struct{}
	canceledOnce sync.Once
	closed       bool
}

// newFakeSession builds a session with a buffered event channel.
func newFakeSession() *fakeSession {
	return &fakeSession{
		info: session.Info{
			ID:    "session-1",
			Agent: "coder",
			Model: "fake/test-model",
		},
		events:   make(chan engine.Event, 16),
		canceled: make(chan struct{}),
	}
}

// Info returns the session metadata.
func (s *fakeSession) Info() session.Info { return s.info }

// History returns the stored messages of the session.
func (s *fakeSession) History() []llm.Message { return s.history }

// Run records the prompt and returns the scripted event channel.
func (s *fakeSession) Run(ctx context.Context, prompt string) <-chan engine.Event {
	s.prompts = append(s.prompts, prompt)
	go func() {
		<-ctx.Done()
		s.canceledOnce.Do(func() { close(s.canceled) })
	}()
	return s.events
}

// Close marks the session as closed.
func (s *fakeSession) Close() error {
	s.closed = true
	return nil
}

// Keys used by the tests.
var (
	pressUp     = tea.KeyPressMsg{Code: tea.KeyUp}
	pressDown   = tea.KeyPressMsg{Code: tea.KeyDown}
	pressEnter  = tea.KeyPressMsg{Code: tea.KeyEnter}
	pressEscape = tea.KeyPressMsg{Code: tea.KeyEscape}
	pressSpace  = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	pressCtrlC  = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	pressCtrlD  = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	pressCtrlJ  = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	pressCtrlP  = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	pressPgUp   = tea.KeyPressMsg{Code: tea.KeyPgUp}
	pressPgDown = tea.KeyPressMsg{Code: tea.KeyPgDown}
	pressHome   = tea.KeyPressMsg{Code: tea.KeyHome}
	pressEnd    = tea.KeyPressMsg{Code: tea.KeyEnd}
)

// windowMsg builds a terminal resize message.
func windowMsg(width, height int) tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: width, Height: height}
}

// update applies one message to the model and returns the command it issues.
func update(t *testing.T, m *model, msg tea.Msg) tea.Cmd {
	t.Helper()

	_, cmd := m.Update(msg)
	return cmd
}

// run executes a command and feeds every message it produces into the model the
// way the Bubble Tea runtime does, following the commands a batch carries. It
// returns the last command the model issued, so tests can assert side effects
// such as quitting.
func run(t *testing.T, m *model, cmd tea.Cmd) tea.Cmd {
	t.Helper()

	return deliver(t, m, cmd)
}

// deliver executes one command, if any, and feeds its message into the model.
func deliver(t *testing.T, m *model, cmd tea.Cmd) tea.Cmd {
	t.Helper()

	if cmd == nil {
		return nil
	}
	return feed(t, m, cmd())
}

// feed gives a message to the model and follows the commands of a batch.
func feed(t *testing.T, m *model, msg tea.Msg) tea.Cmd {
	t.Helper()

	if batch, ok := msg.(tea.BatchMsg); ok {
		var last tea.Cmd
		for _, sub := range batch {
			if out := deliver(t, m, sub); out != nil {
				last = out
			}
		}
		return last
	}

	_, out := m.Update(msg)
	return out
}

// sendEvent delivers one engine event to the model, as the run stream does.
func sendEvent(t testing.TB, m *model, event engine.Event) tea.Cmd {
	t.Helper()

	_, cmd := m.Update(engineEventsMsg{event})
	return cmd
}

// newTestModel builds a model with scripted agents and sessions.
func newTestModel(
	t *testing.T,
	definitions []agent.Agent,
	selected int,
	prepare sessionFactory,
) *model {
	t.Helper()

	return newTestModelWith(t, modelConfig{
		agents:     definitions,
		selected:   selected,
		newSession: prepare,
	})
}

// newTestModelWith builds a model from a configuration, filling the pieces the
// tests do not care about.
func newTestModelWith(t *testing.T, cfg modelConfig) *model {
	t.Helper()

	if cfg.newSession == nil {
		cfg.newSession = func(string) (Session, error) { return newFakeSession(), nil }
	}
	if cfg.resumeSession == nil {
		cfg.resumeSession = func(string) (Session, error) { return newFakeSession(), nil }
	}
	if cfg.newRunContext == nil {
		cfg.newRunContext = func() (context.Context, context.CancelFunc) {
			return context.WithCancel(t.Context())
		}
	}
	return newModel(cfg)
}

// chatModel returns a model already chatting with a scripted session.
func chatModel(t *testing.T) (*model, *fakeSession) {
	t.Helper()

	scripted := newFakeSession()
	m := newTestModel(
		t,
		[]agent.Agent{{ID: "coder", Description: "A test agent"}},
		0,
		func(string) (Session, error) { return scripted, nil },
	)

	cmd := m.Init()
	require.NotNil(t, cmd)
	run(t, m, cmd)
	require.Equal(t, phaseChat, m.phase)
	update(t, m, windowMsg(80, 24))
	return m, scripted
}

// TestModel verifies the state machine of the interface.
func TestModel(t *testing.T) {
	t.Run("starts the only agent automatically", func(t *testing.T) {
		scripted := newFakeSession()
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}},
			0,
			func(agentID string) (Session, error) {
				require.Equal(t, "coder", agentID)
				return scripted, nil
			},
		)
		update(t, m, windowMsg(80, 24))

		cmd := m.Init()
		require.NotNil(t, cmd)
		run(t, m, cmd)

		require.Equal(t, phaseChat, m.phase)
		require.Equal(t, "session-1", m.session.Info().ID)
	})

	t.Run("picks the highlighted agent", func(t *testing.T) {
		created := ""
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}, {ID: "writer"}},
			-1,
			func(agentID string) (Session, error) {
				created = agentID
				return newFakeSession(), nil
			},
		)
		update(t, m, windowMsg(80, 24))
		require.Equal(t, phasePicker, m.phase)
		require.Nil(t, m.Init())

		update(t, m, pressDown)
		cmd := update(t, m, pressEnter)
		require.Equal(t, phasePreparing, m.phase)
		require.NotNil(t, cmd)

		run(t, m, cmd)

		require.Equal(t, "writer", created)
		require.Equal(t, phaseChat, m.phase)
	})

	t.Run("keeps the selection in range", func(t *testing.T) {
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}, {ID: "writer"}},
			-1,
			func(string) (Session, error) { return newFakeSession(), nil },
		)

		update(t, m, pressUp)
		require.Equal(t, 0, m.cursor)
		update(t, m, pressDown)
		update(t, m, pressDown)
		require.Equal(t, 1, m.cursor)
	})

	t.Run("reports preparation failures", func(t *testing.T) {
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}},
			0,
			func(string) (Session, error) { return nil, errors.New("boom") },
		)
		update(t, m, windowMsg(80, 24))

		cmd := m.Init()
		require.NotNil(t, cmd)
		quit := run(t, m, cmd)

		require.ErrorContains(t, m.fatal, "boom")
		require.NotNil(t, quit)
		require.IsType(t, tea.QuitMsg{}, quit())
		require.Contains(t, plain(m.render()), "error: boom")
	})

	t.Run("submits a prompt and folds the answer", func(t *testing.T) {
		m, scripted := chatModel(t)

		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))
		require.Equal(t, []string{"hello"}, scripted.prompts)
		require.True(t, m.running)
		require.Empty(t, m.input.Value())

		sendEvent(t, m, engine.Event{Type: engine.EventRunStart})
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "hi "})
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "there"})
		sendEvent(t, m, engine.Event{
			Type:  engine.EventMessageEnd,
			Usage: &engine.Usage{InputTokens: 3, OutputTokens: 2},
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		require.False(t, m.running)
		require.Equal(t, 3, m.usageIn)
		require.Equal(t, 2, m.usageOut)

		view := plain(m.render())
		require.Contains(t, view, "hello")
		require.Contains(t, view, "hi there")
		require.Contains(t, view, "tokens 3 in")
	})

	t.Run("ignores empty prompts", func(t *testing.T) {
		m, scripted := chatModel(t)

		m.input.SetValue("   ")
		require.Nil(t, update(t, m, pressEnter))

		require.Empty(t, scripted.prompts)
		require.False(t, m.running)
	})

	t.Run("ignores prompts while running", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("first")
		update(t, m, pressEnter)

		m.input.SetValue("second")
		update(t, m, pressEnter)

		require.Equal(t, []string{"first"}, scripted.prompts)
		require.Equal(t, "second", m.input.Value())
	})

	t.Run("asks for confirmation before interrupting the run", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("long task")
		update(t, m, pressEnter)
		require.True(t, m.running)

		cmd := update(t, m, pressEscape)

		require.True(t, m.confirmInterrupt)
		require.NotNil(t, cmd, "arming the confirmation starts its timeout")
		require.Contains(t, plain(m.render()), "esc again to interrupt")
		select {
		case <-scripted.canceled:
			require.Fail(t, "the run was canceled before the confirmation")
		default:
		}
	})

	t.Run("cancels the run on the confirming escape", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("long task")
		update(t, m, pressEnter)

		update(t, m, pressEscape)
		require.True(t, m.confirmInterrupt)

		require.Nil(t, update(t, m, pressEscape), "confirming needs no timeout")

		select {
		case <-scripted.canceled:
		case <-time.After(2 * time.Second):
			require.Fail(t, "the run was not canceled")
		}
		require.False(t, m.confirmInterrupt)

		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonInterrupted,
		})

		require.False(t, m.running)
		require.Contains(t, plain(m.render()), "interrupted")
	})

	t.Run("drops the interruption request when it times out", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("long task")
		update(t, m, pressEnter)
		update(t, m, pressEscape)
		require.True(t, m.confirmInterrupt)

		update(t, m, interruptTimeoutMsg{seq: m.interruptSeq})

		require.False(t, m.confirmInterrupt)
		require.Contains(t, plain(m.render()), "esc to interrupt")
		require.NotContains(t, plain(m.render()), "esc again")
		select {
		case <-scripted.canceled:
			require.Fail(t, "the timed out request canceled the run")
		default:
		}
	})

	t.Run("ignores the timeout of a replaced request", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("long task")
		update(t, m, pressEnter)
		update(t, m, pressEscape)

		// The timer of a request that a newer press already replaced carries
		// an older sequence and must not drop the pending confirmation.
		update(t, m, interruptTimeoutMsg{seq: m.interruptSeq - 1})

		require.True(t, m.confirmInterrupt, "a stale timer must not drop the request")
	})

	t.Run("ignores escape when no run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)

		require.Nil(t, update(t, m, pressEscape))
		require.False(t, m.confirmInterrupt)
	})

	t.Run("finishes the run when the event channel closes", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		require.True(t, m.running)

		update(t, m, eventsClosedMsg{})

		require.False(t, m.running)
		require.Nil(t, m.events)
	})

	t.Run("scrolls the transcript with the arrow keys", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 40 {
			sendEvent(t, m, engine.Event{
				Type: engine.EventTextDelta,
				Text: "line\n\n",
			})
		}
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})
		require.Positive(t, m.conversation.offsetRows())

		before := m.conversation.offsetRows()
		update(t, m, pressUp)

		require.Less(t, m.conversation.offsetRows(), before)
	})

	t.Run("keeps the scroll position while the run streams", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 20))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 20 {
			sendEvent(t, m, engine.Event{
				Type: engine.EventTextDelta,
				Text: "para\n\n",
			})
		}

		update(t, m, pressPgUp)
		scrolled := m.conversation.offsetRows()
		require.Less(t, scrolled, m.conversation.maxOffset(), "the window scrolled up")

		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "more\n\n"})

		require.Equal(t, scrolled, m.conversation.offsetRows(), "the stream must not drag it back")
	})

	t.Run("jumps to the top and the bottom with home and end", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 20))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 20 {
			sendEvent(t, m, engine.Event{
				Type: engine.EventTextDelta,
				Text: "para\n\n",
			})
		}

		update(t, m, pressHome)
		require.Zero(t, m.conversation.offsetRows(), "home goes to the top")

		update(t, m, pressEnd)
		require.True(t, m.conversation.atBottom(), "end goes to the bottom")
	})

	t.Run("moves block by block with page up and page down", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 16))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 12 {
			sendEvent(t, m, engine.Event{
				Type: engine.EventTextDelta,
				Text: "para\n\n",
			})
		}

		update(t, m, pressHome)
		update(t, m, pressPgDown)
		require.Positive(t, m.conversation.offsetRows(), "page down advances a block")

		update(t, m, pressPgUp)
		require.Zero(t, m.conversation.offsetRows(), "page up goes back a block")
	})

	t.Run("grows the prompt without a line limit", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 30))

		for range 40 {
			update(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
			update(t, m, pressCtrlJ)
		}

		require.Equal(t, 41, strings.Count(m.input.Value(), "\n")+1, "every line is kept")
		require.LessOrEqual(
			t,
			m.input.Height(),
			maxInputRows,
			"the input stays a reasonable height",
		)
		require.Equal(t, maxInputRows, m.input.Height(), "the input grows up to the cap")
	})

	t.Run("opens and closes the command center", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("draft")

		update(t, m, pressCtrlP)
		require.Equal(t, phaseSettings, m.phase)

		update(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
		require.Equal(t, "draft", m.input.Value())

		update(t, m, pressEscape)

		require.Equal(t, phaseChat, m.phase)
		require.Equal(t, "draft", m.input.Value())
	})

	t.Run("returns from the command center to the previous phase", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		require.Equal(t, phaseMenu, m.phase)

		update(t, m, pressCtrlP)
		require.Equal(t, phaseSettings, m.phase)

		update(t, m, pressCtrlP)

		require.Equal(t, phaseMenu, m.phase)
	})

	t.Run("toggles the harness options", func(t *testing.T) {
		m, _ := chatModel(t)
		require.True(t, m.preferences.HideToolOutput)
		require.True(t, m.preferences.HideThinking)
		require.True(t, m.preferences.RenderMarkdown)

		update(t, m, pressCtrlP)
		update(t, m, pressEnter)
		require.False(t, m.preferences.HideToolOutput)

		update(t, m, pressSpace)
		require.True(t, m.preferences.HideToolOutput)

		update(t, m, pressDown)
		update(t, m, pressSpace)
		require.False(t, m.preferences.HideThinking)

		update(t, m, pressDown)
		update(t, m, pressSpace)
		require.False(t, m.preferences.RenderMarkdown)

		update(t, m, pressDown)
		update(t, m, pressDown)
		require.Equal(t, 2, m.settingCursor, "the cursor stays on the last option")
	})

	t.Run("shows the tool output toggled from the command center", func(t *testing.T) {
		m, _ := chatModel(t)
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
			Text:       "a.txt",
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})
		require.NotContains(t, plain(m.render()), "a.txt")

		update(t, m, pressCtrlP)
		update(t, m, pressEnter)
		update(t, m, pressEscape)

		require.Contains(t, plain(m.render()), "a.txt")
	})

	t.Run("keeps the rendered conversation in sync", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventThinkingDelta, Text: "thinking about it"})
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
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "done"})

		before := m.conversation.view()
		m.invalidateTranscript()
		m.refreshTranscript()

		require.Equal(t, before, m.conversation.view())
	})

	t.Run("passes typed text to the input", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, tea.KeyPressMsg{Code: 'a', Text: "ab"})

		require.Equal(t, "ab", m.input.Value())
	})

	t.Run("inserts newlines in the prompt", func(t *testing.T) {
		m, scripted := chatModel(t)
		update(t, m, windowMsg(80, 24))

		update(t, m, tea.KeyPressMsg{Code: 'a', Text: "first"})
		update(t, m, pressCtrlJ)
		update(t, m, tea.KeyPressMsg{Code: 'b', Text: "second"})

		require.Equal(t, "first\nsecond", m.input.Value())
		require.Equal(t, 2, m.input.LineCount())
		require.Empty(t, scripted.prompts)

		update(t, m, pressEnter)

		require.Equal(t, []string{"first\nsecond"}, scripted.prompts)
		require.Equal(t, 1, m.input.LineCount())
	})

	t.Run("moves the prompt cursor with the arrows when it is multi-line", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, tea.KeyPressMsg{Code: 'a', Text: "one"})
		update(t, m, pressCtrlJ)
		update(t, m, tea.KeyPressMsg{Code: 'b', Text: "two"})
		require.Equal(t, 1, m.input.Line())

		update(t, m, pressUp)

		require.Equal(t, 0, m.input.Line())
	})

	t.Run("applies the reported terminal background", func(t *testing.T) {
		m, _ := chatModel(t)
		require.True(t, m.hasDarkBG)

		update(t, m, tea.BackgroundColorMsg{Color: color.White})
		require.False(t, m.hasDarkBG)

		update(t, m, tea.BackgroundColorMsg{Color: color.Black})
		require.True(t, m.hasDarkBG)
	})

	t.Run("ticks the spinner only while busy", func(t *testing.T) {
		m, _ := chatModel(t)
		require.Nil(t, update(t, m, spinnerTickMsg{}))

		m.input.SetValue("go")
		update(t, m, pressEnter)

		require.True(t, m.busy())
		require.NotNil(t, update(t, m, spinnerTickMsg{}))
	})

	t.Run("animates the status spinner while a run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		before := plain(m.render())
		require.Contains(t, before, "working")

		update(t, m, spinnerTickMsg{})

		require.NotEqual(t, before, plain(m.render()), "the status spinner advances")
	})

	t.Run("quits with ctrl+c", func(t *testing.T) {
		m, _ := chatModel(t)

		cmd := update(t, m, pressCtrlC)

		require.NotNil(t, cmd)
		require.IsType(t, tea.QuitMsg{}, cmd())
	})

	t.Run("quits with ctrl+d only when idle", func(t *testing.T) {
		m, _ := chatModel(t)

		require.IsType(t, tea.QuitMsg{}, update(t, m, pressCtrlD)())

		m.running = true
		require.Nil(t, update(t, m, pressCtrlD))
	})

	t.Run("closes the session once", func(t *testing.T) {
		m, scripted := chatModel(t)

		m.Close()
		m.Close()

		require.True(t, scripted.closed)
		require.Nil(t, m.session)
	})

	t.Run("continues a previous session", func(t *testing.T) {
		scripted := newFakeSession()
		scripted.history = []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}},
			{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
		}
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
			resumeSession: func(sessionID string) (Session, error) {
				require.Equal(t, "session-7", sessionID)
				return scripted, nil
			},
		})
		update(t, m, windowMsg(80, 24))
		require.Equal(t, phaseMenu, m.phase)
		require.Nil(t, m.Init())

		update(t, m, pressDown)
		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phaseSessions, m.phase)

		cmd := update(t, m, pressEnter)
		require.Equal(t, phasePreparing, m.phase)
		require.NotNil(t, cmd)
		run(t, m, cmd)

		require.Equal(t, phaseChat, m.phase)
		view := plain(m.render())
		require.Contains(t, view, "hello")
		require.Contains(t, view, "hi")
	})

	t.Run("returns from the session list to the menu", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})

		update(t, m, pressDown)
		update(t, m, pressEnter)
		require.Equal(t, phaseSessions, m.phase)

		update(t, m, pressEscape)

		require.Equal(t, phaseMenu, m.phase)
	})

	t.Run("keeps the session selection in range", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{
				{ID: "session-1", Agent: "coder", Title: "one"},
				{ID: "session-2", Agent: "coder", Title: "two"},
			},
		})
		m.phase = phaseSessions

		update(t, m, pressUp)
		require.Equal(t, 0, m.chosen)
		update(t, m, pressDown)
		update(t, m, pressDown)
		require.Equal(t, 1, m.chosen)
	})

	t.Run("starts a new session from the menu", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		require.Equal(t, phaseMenu, m.phase)

		require.Nil(t, update(t, m, pressEnter))

		require.Equal(t, phasePicker, m.phase)
	})

	t.Run("prepares the only agent from the menu", func(t *testing.T) {
		created := ""
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
			newSession: func(agentID string) (Session, error) {
				created = agentID
				return newFakeSession(), nil
			},
		})

		cmd := update(t, m, pressEnter)
		require.Equal(t, phasePreparing, m.phase)
		run(t, m, cmd)

		require.Equal(t, "coder", created)
		require.Equal(t, phaseChat, m.phase)
	})
}

// TestActivity verifies the single status line that reports what a run is
// doing at the end of the conversation.
func TestActivity(t *testing.T) {
	t.Run("is blank while no run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)

		require.Empty(t, plain(m.activityLine()))
	})

	t.Run("animates the fluid mark", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)

		first := plain(m.activityLine())
		require.Contains(t, first, fluidFrames[0].mark, "the run opens on the first frame")

		update(t, m, spinnerTickMsg{})

		require.NotEqual(t, first, plain(m.activityLine()), "the mark advances")
		require.Contains(t, plain(m.activityLine()), fluidFrames[1].mark)
	})

	t.Run("reports what the run is doing", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		require.Contains(t, plain(m.activityLine()), "working")

		sendEvent(t, m, engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		require.Contains(t, plain(m.activityLine()), "thinking")

		sendEvent(
			t,
			m,
			engine.Event{Type: engine.EventToolCall, ToolCallID: "c1", ToolName: "shell"},
		)
		require.Contains(t, plain(m.activityLine()), "running shell")

		sendEvent(t, m, engine.Event{Type: engine.EventToolResult, ToolCallID: "c1", Text: "ok"})
		require.Contains(
			t,
			plain(m.activityLine()),
			"working",
			"a finished tool hands back to the model",
		)

		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "answer"})
		require.Contains(t, plain(m.activityLine()), "working")
	})

	t.Run("carries the interrupt hint and arms it", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		require.Contains(t, plain(m.activityLine()), "esc to interrupt")

		update(t, m, pressEscape)
		require.Contains(t, plain(m.activityLine()), "esc again to interrupt")
	})

	t.Run("clears when the run ends", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)

		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.Empty(t, plain(m.activityLine()))
	})

	t.Run("shrinks to a separator when the run ends", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		require.Equal(t, activityRows, m.activityHeight(), "the block occupies rows while running")
		running := m.conversation.height

		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.Equal(t, 1, m.activityHeight(), "the block keeps a single separator row")
		require.Greater(
			t,
			m.conversation.height,
			running,
			"the conversation grows into the freed rows",
		)
	})

	t.Run("does not put a spinner in the blocks", func(t *testing.T) {
		m, _ := chatModel(t)
		m.preferences = preferences{HideThinking: true}
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		sendEvent(
			t,
			m,
			engine.Event{Type: engine.EventToolCall, ToolCallID: "c1", ToolName: "shell"},
		)

		require.Equal(t, markerActivity+" Thinking", ansi.Strip(m.renderThinkingEntry(
			&m.transcript.entries[1], 40,
		)))
		require.Equal(t, markerActivity+" shell", ansi.Strip(m.renderToolEntry(
			&m.transcript.entries[2], 40,
		)))
	})
}

// TestStartPhase verifies the phase the interface opens with.
func TestStartPhase(t *testing.T) {
	definitions := []agent.Agent{{ID: "coder"}}
	sessions := []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}}

	t.Run("starts a requested agent directly", func(t *testing.T) {
		require.Equal(t, phasePreparing, startPhase(modelConfig{
			agents:    definitions,
			selected:  0,
			requested: true,
			sessions:  sessions,
		}))
	})

	t.Run("asks with previous sessions", func(t *testing.T) {
		require.Equal(t, phaseMenu, startPhase(modelConfig{
			agents:   definitions,
			selected: -1,
			sessions: sessions,
		}))
	})

	t.Run("starts the only agent without sessions", func(t *testing.T) {
		require.Equal(t, phasePreparing, startPhase(modelConfig{
			agents:   definitions,
			selected: 0,
		}))
	})

	t.Run("picks among several agents", func(t *testing.T) {
		require.Equal(t, phasePicker, startPhase(modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
		}))
	})
}

// TestStreamEvents verifies event delivery.
func TestStreamEvents(t *testing.T) {
	t.Run("delivers the events already available", func(t *testing.T) {
		events := make(chan engine.Event, 3)
		events <- engine.Event{Type: engine.EventTextDelta, Text: "one"}
		events <- engine.Event{Type: engine.EventTextDelta, Text: "two"}

		burst, ok := streamEvents(events)().(engineEventsMsg)

		require.True(t, ok)
		require.Len(t, burst, 2)
		require.Equal(t, "one", burst[0].Text)
		require.Equal(t, "two", burst[1].Text)
	})

	t.Run("caps the burst", func(t *testing.T) {
		events := make(chan engine.Event, maxEventBurst+1)
		for range maxEventBurst + 1 {
			events <- engine.Event{Type: engine.EventTextDelta, Text: "chunk"}
		}

		burst, ok := streamEvents(events)().(engineEventsMsg)

		require.True(t, ok)
		require.Len(t, burst, maxEventBurst)
		require.Len(t, events, 1)
	})

	t.Run("reports a closed channel", func(t *testing.T) {
		events := make(chan engine.Event)
		close(events)

		require.IsType(t, eventsClosedMsg{}, streamEvents(events)())
	})

	t.Run("closes after delivering the last events", func(t *testing.T) {
		events := make(chan engine.Event, 1)
		events <- engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn}
		close(events)

		burst, ok := streamEvents(events)().(engineEventsMsg)
		require.True(t, ok)
		require.Len(t, burst, 1)

		require.IsType(t, eventsClosedMsg{}, streamEvents(events)())
	})
}
