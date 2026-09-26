package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/jsruntime"
)

// testWakeup records the messages waking the interface.
type testWakeup struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (w *testWakeup) send(msg tea.Msg) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msg)
}

func (w *testWakeup) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.msgs)
}

func TestInteractor(t *testing.T) {
	t.Run("denies without an attached interface", func(t *testing.T) {
		interactor := &Interactor{}
		approved, err := interactor.Confirm(
			t.Context(),
			jsruntime.ConfirmRequest{Title: "t", Body: "b"},
		)
		require.NoError(t, err)
		require.False(t, approved)
	})

	t.Run("denies a canceled context", func(t *testing.T) {
		interactor := &Interactor{}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := interactor.Confirm(ctx, jsruntime.ConfirmRequest{Title: "t", Body: "b"})
		require.Error(t, err)
	})

	t.Run("answers through the interface", func(t *testing.T) {
		for _, approved := range []bool{true, false} {
			interactor := &Interactor{}
			wakeup := &testWakeup{}
			interactor.Attach(wakeup.send)

			answered := make(chan bool, 1)
			go func() {
				got, err := interactor.Confirm(
					t.Context(),
					jsruntime.ConfirmRequest{Title: "asker", Body: "proceed?"},
				)
				require.NoError(t, err)
				answered <- got
			}()

			require.Eventually(t, func() bool {
				_, _, pending := interactor.Pending()
				return pending
			}, time.Second, time.Millisecond)

			title, body, pending := interactor.Pending()
			require.True(t, pending)
			require.Equal(t, "asker", title)
			require.Equal(t, "proceed?", body)
			require.True(t, interactor.Answer(approved))
			require.Equal(t, approved, <-answered)
			_, _, pending = interactor.Pending()
			require.False(t, pending)
			require.NotZero(t, wakeup.count())
		}
	})

	t.Run("denies a second question while one waits", func(t *testing.T) {
		interactor := &Interactor{}
		wakeup := &testWakeup{}
		interactor.Attach(wakeup.send)

		release := make(chan struct{})
		go func() {
			_, _ = interactor.Confirm(
				t.Context(),
				jsruntime.ConfirmRequest{Title: "first", Body: "b"},
			)
			close(release)
		}()
		require.Eventually(t, func() bool {
			_, _, pending := interactor.Pending()
			return pending
		}, time.Second, time.Millisecond)

		approved, err := interactor.Confirm(
			t.Context(),
			jsruntime.ConfirmRequest{Title: "second", Body: "b"},
		)
		require.NoError(t, err)
		require.False(t, approved)

		require.True(t, interactor.Answer(true))
		<-release
	})

	t.Run("cancellation drops the question", func(t *testing.T) {
		interactor := &Interactor{}
		wakeup := &testWakeup{}
		interactor.Attach(wakeup.send)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := interactor.Confirm(ctx, jsruntime.ConfirmRequest{Title: "t", Body: "b"})
			done <- err
		}()
		require.Eventually(t, func() bool {
			_, _, pending := interactor.Pending()
			return pending
		}, time.Second, time.Millisecond)

		cancel()
		require.Error(t, <-done)
		_, _, pending := interactor.Pending()
		require.False(t, pending)
	})

	t.Run("answer without a question reports nothing", func(t *testing.T) {
		interactor := &Interactor{}
		require.False(t, interactor.Answer(true))
	})

	t.Run("notify wakes the interface", func(t *testing.T) {
		interactor := &Interactor{}
		interactor.Notify(t.Context(), jsruntime.Notification{Title: "t", Body: "b"})

		wakeup := &testWakeup{}
		interactor.Attach(wakeup.send)
		interactor.Notify(t.Context(), jsruntime.Notification{Title: "t", Body: "b"})
		require.Equal(t, 1, wakeup.count())
	})
}

func TestConfirmScreen(t *testing.T) {
	pressY := tea.KeyPressMsg{Code: 'y', Text: "y"}
	pressUp := tea.KeyPressMsg{Code: tea.KeyUp}
	pressDown := tea.KeyPressMsg{Code: tea.KeyDown}
	pressEnter := tea.KeyPressMsg{Code: tea.KeyEnter}

	// asking returns a chat model with a question pending in its interactor.
	asking := func(t *testing.T) (*model, chan bool) {
		t.Helper()
		m, _ := chatModel(t)
		m.interactor.Attach(func(tea.Msg) {})
		answered := make(chan bool, 1)
		go func() {
			approved, _ := m.interactor.Confirm(
				t.Context(),
				jsruntime.ConfirmRequest{Title: "asker", Body: "proceed?"},
			)
			answered <- approved
		}()
		require.Eventually(t, func() bool {
			_, _, pending := m.interactor.Pending()
			return pending
		}, time.Second, time.Millisecond)
		update(t, m, extensionConfirmMsg{})
		require.Equal(t, phaseConfirm, m.phase, "the question takes its own screen")
		return m, answered
	}

	t.Run("opens the question screen", func(t *testing.T) {
		m, _ := asking(t)
		screen := m.viewConfirm()
		require.Contains(t, screen, "confirm")
		require.Contains(t, screen, "asker")
		require.Contains(t, screen, "proceed?")
		require.Contains(t, screen, "Approve")
		require.Contains(t, screen, "Deny")
		require.False(t, m.input.Focused())
	})

	t.Run("enter takes the highlighted choice", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, pressEnter)
		require.True(t, <-answered)
		require.Equal(t, phaseChat, m.phase, "the answer returns to the chat")
		require.True(t, m.input.Focused(), "the prompt takes the keys back")
	})

	t.Run("the arrows move the highlight", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, pressDown)
		update(t, m, pressEnter)
		require.False(t, <-answered)
	})

	t.Run("the highlight wraps around", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, pressUp)
		update(t, m, pressEnter)
		require.False(t, <-answered, "up from the first choice lands on the last")
	})

	t.Run("escape denies", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, pressEscape)
		require.False(t, <-answered)
		require.Equal(t, phaseChat, m.phase)
	})

	t.Run("a stray key answers nothing", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, pressY)
		require.Equal(t, phaseConfirm, m.phase)
		update(t, m, pressEnter)
		require.True(t, <-answered)
	})

	t.Run("the body folds to the width", func(t *testing.T) {
		m, _ := asking(t)
		m.width = 40
		m.confirmScreen.body = "one two three four five six seven eight nine ten"
		require.Greater(t, strings.Count(m.viewConfirm(), "\n"), 4)
	})

	t.Run("a notice lands in the transcript", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, extensionNoticeMsg{text: "hook said hi"})
		found := false
		for _, entry := range m.transcript.entries {
			for _, fragment := range entry.fragments {
				if fragment == "hook said hi" {
					found = true
				}
			}
		}
		require.True(t, found)
	})

	t.Run("the question takes over another screen", func(t *testing.T) {
		m, answered := asking(t)
		m.phase = phaseSettings
		update(t, m, extensionConfirmMsg{})
		require.Equal(t, phaseConfirm, m.phase, "a hidden question looks like a stuck run")
		update(t, m, pressEnter)
		require.True(t, <-answered)
	})

	t.Run("ctrl+c stays reachable while asking", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, pressCtrlC)
		require.Equal(t, phaseConfirm, m.phase)
		require.Equal(t, confirmQuit, m.confirm.action)
		update(t, m, pressEnter)
		require.True(t, <-answered)
	})

	t.Run("a notice during a question changes nothing", func(t *testing.T) {
		m, answered := asking(t)
		update(t, m, extensionNoticeMsg{text: "busy"})
		require.Equal(t, phaseConfirm, m.phase)
		update(t, m, pressEnter)
		require.True(t, <-answered)
	})
}
