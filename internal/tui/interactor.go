package tui

import (
	"context"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/jsruntime"
)

// extensionConfirmMsg asks the interface to show the pending extension
// question, which the interactor already holds.
type extensionConfirmMsg struct{}

// extensionNoticeMsg carries a notice an extension reported.
type extensionNoticeMsg struct {
	text string
}

// extensionConfirm is one yes/no question an extension asked the user. The
// answer channel is buffered, so answering never blocks the interface.
type extensionConfirm struct {
	title  string
	body   string
	answer chan bool
}

// Interactor answers the questions extensions ask the user of the interface
// and shows their notices. It implements jsruntime.Interactor for the
// sessions the interface prepares.
//
// Confirm blocks the calling run, never the interface: it stores the pending
// question, wakes the interface through Attach, and waits until a key press
// answers or the run is canceled. Only one question is ever pending, because
// the engine runs one tool and one hook at a time; a second question asked
// while one waits is denied.
type Interactor struct {
	mu      sync.Mutex
	wakeup  func(tea.Msg)
	pending *extensionConfirm
}

// interactorOrDetached returns the configured interactor, or a detached one
// that denies every question and drops every notice.
func (c modelConfig) interactorOrDetached() *Interactor {
	if c.interactor != nil {
		return c.interactor
	}
	return &Interactor{}
}

// Attach connects the interactor to the running interface: wakeup delivers
// the message that re-renders it. It is called once the program exists.
func (i *Interactor) Attach(wakeup func(tea.Msg)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.wakeup = wakeup
}

// Confirm asks the user a yes/no question and blocks until they answer or
// ctx is canceled. Without an attached interface it denies, which is the
// headless default.
func (i *Interactor) Confirm(ctx context.Context, req jsruntime.ConfirmRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("tui: confirm canceled: %w", err)
	}
	i.mu.Lock()
	if i.wakeup == nil || i.pending != nil {
		i.mu.Unlock()
		return false, nil
	}
	pending := &extensionConfirm{title: req.Title, body: req.Body, answer: make(chan bool, 1)}
	i.pending = pending
	wakeup := i.wakeup
	i.mu.Unlock()

	wakeup(extensionConfirmMsg{})
	select {
	case approved := <-pending.answer:
		return approved, nil
	case <-ctx.Done():
		i.forget(pending)
		return false, fmt.Errorf("tui: confirm canceled: %w", ctx.Err())
	}
}

// Notify shows a non-blocking notice to the user. Without an attached
// interface it is dropped.
func (i *Interactor) Notify(_ context.Context, note jsruntime.Notification) {
	i.mu.Lock()
	wakeup := i.wakeup
	i.mu.Unlock()
	if wakeup == nil || note.Title == "" && note.Body == "" {
		return
	}
	wakeup(extensionNoticeMsg{text: noteTitle(note) + note.Body})
}

// noteTitle renders the identity of the sender of a notice.
func noteTitle(note jsruntime.Notification) string {
	if note.Title == "" {
		return ""
	}
	return note.Title + ": "
}

// Pending returns the question waiting for an answer, if any.
func (i *Interactor) Pending() (title, body string, pending bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.pending == nil {
		return "", "", false
	}
	return i.pending.title, i.pending.body, true
}

// Answer resolves the pending question, reporting whether one waited. The
// interface calls it from the loop that handles its messages, so it never
// wakes the loop: it only unblocks the run and clears the question, and the
// caller leaves the screen the question opened.
func (i *Interactor) Answer(approved bool) bool {
	i.mu.Lock()
	pending := i.pending
	i.pending = nil
	i.mu.Unlock()
	if pending == nil {
		return false
	}
	pending.answer <- approved
	return true
}

// forget drops a pending question that no longer applies, such as one whose
// run was canceled while it waited.
func (i *Interactor) forget(pending *extensionConfirm) {
	i.mu.Lock()
	if i.pending == pending {
		i.pending = nil
	}
	wakeup := i.wakeup
	i.mu.Unlock()
	if wakeup != nil {
		wakeup(extensionConfirmMsg{})
	}
}

// confirmChoices are the options of the question screen, in the order the user
// reads them. The zero index approves and the second denies.
var confirmChoices = [2]string{"Approve", "Deny"}

// confirmScreen shows the question an extension asks the user, which the
// engine blocks on until it is answered. It is a screen of its own rather than
// a dialog over the chat, so the reader answers it deliberately instead of by
// reflex, and the run visibly waits instead of looking stuck.
type confirmScreen struct {
	// cursor is the position of the highlighted choice.
	cursor int

	// title and body are the question the extension asked, rendered above the
	// choices.
	title string
	body  string
}

// openConfirm shows the question an extension asked, moving to the question
// screen. The answer reaches the waiting run through Answer.
func (m *model) openConfirm(title, body string) {
	m.confirmScreen = confirmScreen{title: title, body: body}
	m.phase = phaseConfirm
	m.input.Blur()
}

// handleConfirmKey moves the highlight of the question and answers it. Enter
// answers with the highlighted choice, escape denies, and the remaining keys
// reach nothing, so the question cannot be dismissed by accident.
func (m *model) handleConfirmKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyUp:
		m.confirmScreen.move(-1)
	case keyDown:
		m.confirmScreen.move(1)
	case keyEnter:
		return m.answerConfirm(m.confirmScreen.approved())
	case keyEscape:
		return m.answerConfirm(false)
	}
	return nil
}

// answerConfirm delivers the answer to the waiting run and leaves the question
// screen, returning to the chat with the prompt focused. The run resumes
// there, which is where a question is asked from.
func (m *model) answerConfirm(approved bool) tea.Cmd {
	if !m.interactor.Answer(approved) {
		return nil
	}
	m.phase = phaseChat
	return m.input.Focus()
}

// move shifts the highlight delta positions through the choices, wrapping
// around at both ends.
func (c *confirmScreen) move(delta int) {
	c.cursor = moveCursor(c.cursor, delta, len(confirmChoices))
}

// approved reports whether the highlighted choice approves.
func (c *confirmScreen) approved() bool {
	return c.cursor == 0
}

// applyExtensionQuestion shows the question an extension asked, whatever
// screen was up when it arrived, or returns to the chat once it is answered.
// The question takes over the interface because the run is blocked on it, so
// leaving it hidden would look like a stuck run. A notice waking the interface
// while the question screen is up changes nothing.
func (m *model) applyExtensionQuestion() tea.Cmd {
	title, body, pending := m.interactor.Pending()
	switch {
	case pending && m.phase != phaseConfirm:
		m.openConfirm(title, body)
	case !pending && m.phase == phaseConfirm:
		return m.showChat()
	}
	return nil
}
