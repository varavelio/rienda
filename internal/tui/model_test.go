package tui

import (
	"context"
	"errors"
	"image/color"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
	pressCtrlC  = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	pressCtrlD  = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	pressCtrlJ  = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	pressPgUp   = tea.KeyPressMsg{Code: tea.KeyPgUp}
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
	update(t, m, cmd())
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
		update(t, m, cmd())

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

		update(t, m, cmd())

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
		quit := update(t, m, cmd())

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

		update(t, m, engineEventMsg{event: engine.Event{Type: engine.EventRunStart}})
		update(t, m, engineEventMsg{event: engine.Event{Type: engine.EventTextDelta, Text: "hi "}})
		update(
			t,
			m,
			engineEventMsg{event: engine.Event{Type: engine.EventTextDelta, Text: "there"}},
		)
		update(t, m, engineEventMsg{event: engine.Event{
			Type:  engine.EventMessageEnd,
			Usage: &engine.Usage{InputTokens: 3, OutputTokens: 2},
		}})
		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		}})

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

	t.Run("cancels the run in flight with escape", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("long task")
		update(t, m, pressEnter)
		require.True(t, m.running)

		update(t, m, pressEscape)

		select {
		case <-scripted.canceled:
		case <-time.After(2 * time.Second):
			require.Fail(t, "the run was not canceled")
		}

		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonInterrupted,
		}})

		require.False(t, m.running)
		require.Contains(t, plain(m.render()), "interrupted")
	})

	t.Run("reports the turn limit", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)

		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonMaxTurns,
		}})

		require.Contains(t, plain(m.render()), "turn limit")
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
			update(t, m, engineEventMsg{event: engine.Event{
				Type: engine.EventTextDelta,
				Text: "line\n",
			}})
		}
		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		}})
		require.Positive(t, m.viewport.YOffset())

		before := m.viewport.YOffset()
		update(t, m, pressUp)

		require.Less(t, m.viewport.YOffset(), before)
	})

	t.Run("passes typed text to the input", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, tea.KeyPressMsg{Code: 'a', Text: "ab"})

		require.Equal(t, "ab", m.input.Value())
	})

	t.Run("inserts newlines in the prompt", func(t *testing.T) {
		m, scripted := chatModel(t)
		require.Equal(t, 14, m.viewport.Height())

		update(t, m, tea.KeyPressMsg{Code: 'a', Text: "first"})
		update(t, m, pressCtrlJ)
		update(t, m, tea.KeyPressMsg{Code: 'b', Text: "second"})

		require.Equal(t, "first\nsecond", m.input.Value())
		require.Equal(t, 2, m.input.LineCount())
		require.Equal(t, 2, m.input.Height())
		require.Equal(t, 13, m.viewport.Height())
		require.Empty(t, scripted.prompts)

		update(t, m, pressEnter)

		require.Equal(t, []string{"first\nsecond"}, scripted.prompts)
		require.Equal(t, 1, m.input.Height())
		require.Equal(t, 14, m.viewport.Height())
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
		require.Nil(t, update(t, m, m.spinner.Tick()))

		m.input.SetValue("go")
		update(t, m, pressEnter)

		require.True(t, m.busy())
		require.NotNil(t, update(t, m, m.spinner.Tick()))
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
		update(t, m, cmd())

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
		update(t, m, cmd())

		require.Equal(t, "coder", created)
		require.Equal(t, phaseChat, m.phase)
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

// TestWaitForEvent verifies event delivery.
func TestWaitForEvent(t *testing.T) {
	events := make(chan engine.Event, 1)
	events <- engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn}
	close(events)

	require.IsType(t, engineEventMsg{}, waitForEvent(events)())
	require.IsType(t, eventsClosedMsg{}, waitForEvent(events)())
}
