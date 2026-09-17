package tui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/session"
)

// fakeSession is a scripted Session implementation.
type fakeSession struct {
	info         session.Info
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
	keyUp     = tea.KeyMsg{Type: tea.KeyUp}
	keyDown   = tea.KeyMsg{Type: tea.KeyDown}
	keyEnter  = tea.KeyMsg{Type: tea.KeyEnter}
	keyEscape = tea.KeyMsg{Type: tea.KeyEsc}
	keyCtrlC  = tea.KeyMsg{Type: tea.KeyCtrlC}
	keyCtrlD  = tea.KeyMsg{Type: tea.KeyCtrlD}
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

	return newModel(modelConfig{
		agents:     definitions,
		selected:   selected,
		newSession: prepare,
		newRunContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(t.Context())
		},
	})
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

		update(t, m, keyDown)
		cmd := update(t, m, keyEnter)
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

		update(t, m, keyUp)
		require.Equal(t, 0, m.cursor)
		update(t, m, keyDown)
		update(t, m, keyDown)
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
		require.Contains(t, plain(m.View()), "error: boom")
	})

	t.Run("submits a prompt and folds the answer", func(t *testing.T) {
		m, scripted := chatModel(t)

		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, keyEnter))
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

		view := plain(m.View())
		require.Contains(t, view, "hello")
		require.Contains(t, view, "hi there")
		require.Contains(t, view, "tokens 3 in")
	})

	t.Run("ignores empty prompts", func(t *testing.T) {
		m, scripted := chatModel(t)

		m.input.SetValue("   ")
		require.Nil(t, update(t, m, keyEnter))

		require.Empty(t, scripted.prompts)
		require.False(t, m.running)
	})

	t.Run("ignores prompts while running", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("first")
		update(t, m, keyEnter)

		m.input.SetValue("second")
		update(t, m, keyEnter)

		require.Equal(t, []string{"first"}, scripted.prompts)
		require.Equal(t, "second", m.input.Value())
	})

	t.Run("cancels the run in flight with escape", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("long task")
		update(t, m, keyEnter)
		require.True(t, m.running)

		update(t, m, keyEscape)

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
		require.Contains(t, plain(m.View()), "interrupted")
	})

	t.Run("reports the turn limit", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, keyEnter)

		update(t, m, engineEventMsg{event: engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonMaxTurns,
		}})

		require.Contains(t, plain(m.View()), "turn limit")
	})

	t.Run("finishes the run when the event channel closes", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, keyEnter)
		require.True(t, m.running)

		update(t, m, eventsClosedMsg{})

		require.False(t, m.running)
		require.Nil(t, m.events)
	})

	t.Run("scrolls the transcript with the arrow keys", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, keyEnter)
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
		require.Positive(t, m.viewport.YOffset)

		before := m.viewport.YOffset
		update(t, m, keyUp)

		require.Less(t, m.viewport.YOffset, before)
	})

	t.Run("passes typed text to the input", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ab")})

		require.Equal(t, "ab", m.input.Value())
	})

	t.Run("ticks the spinner only while busy", func(t *testing.T) {
		m, _ := chatModel(t)
		require.Nil(t, update(t, m, m.spinner.Tick()))

		m.input.SetValue("go")
		update(t, m, keyEnter)

		require.True(t, m.busy())
		require.NotNil(t, update(t, m, m.spinner.Tick()))
	})

	t.Run("quits with ctrl+c", func(t *testing.T) {
		m, _ := chatModel(t)

		cmd := update(t, m, keyCtrlC)

		require.NotNil(t, cmd)
		require.IsType(t, tea.QuitMsg{}, cmd())
	})

	t.Run("quits with ctrl+d only when idle", func(t *testing.T) {
		m, _ := chatModel(t)

		require.IsType(t, tea.QuitMsg{}, update(t, m, keyCtrlD)())

		m.running = true
		require.Nil(t, update(t, m, keyCtrlD))
	})

	t.Run("closes the session once", func(t *testing.T) {
		m, scripted := chatModel(t)

		m.Close()
		m.Close()

		require.True(t, scripted.closed)
		require.Nil(t, m.session)
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
