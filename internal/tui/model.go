package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/session"
)

// chromeHeight is the number of terminal lines the interface reserves for the
// header, the status line and the input, leaving the rest to the transcript.
const chromeHeight = 3

// Session is the agent session the interface drives. The harness package
// provides the production implementation.
type Session interface {
	// Info returns the session metadata.
	Info() session.Info

	// Run starts a run and returns the channel carrying its events.
	Run(ctx context.Context, prompt string) <-chan engine.Event

	// Close releases the session.
	Close() error
}

// sessionFactory prepares a session for the agent identified by its id.
type sessionFactory func(agentID string) (Session, error)

// contextFactory creates the context of a run together with its cancel
// function. The model stores no context, only the cancel function of the run
// in flight.
type contextFactory func() (context.Context, context.CancelFunc)

// phase is the screen the interface shows.
type phase int

const (
	// phasePicker asks the user to choose the agent to run.
	phasePicker phase = iota
	// phasePreparing creates the session of the chosen agent.
	phasePreparing
	// phaseChat shows the conversation.
	phaseChat
)

// sessionReadyMsg carries a prepared session into the interface.
type sessionReadyMsg struct {
	session Session
}

// sessionFailedMsg reports that a session could not be prepared.
type sessionFailedMsg struct {
	err error
}

// engineEventMsg carries one event of a running session.
type engineEventMsg struct {
	event engine.Event
}

// eventsClosedMsg reports that the event channel of a run was closed.
type eventsClosedMsg struct{}

// modelConfig configures a model.
type modelConfig struct {
	// agents lists the agent definitions the user may run.
	agents []agent.Agent

	// selected preselects the agent to run by index, or -1 to let the user
	// pick one.
	selected int

	// newSession prepares the session of an agent.
	newSession sessionFactory

	// newRunContext creates the context of a run.
	newRunContext contextFactory
}

// model is the Bubble Tea model of the interactive interface.
type model struct {
	agents   []agent.Agent
	cursor   int
	selected int
	phase    phase

	session  Session
	events   <-chan engine.Event
	cancel   context.CancelFunc
	running  bool
	fatal    error
	usageIn  int
	usageOut int

	transcript transcript
	input      textinput.Model
	spinner    spinner.Model
	viewport   viewport.Model

	width  int
	height int

	newSession    sessionFactory
	newRunContext contextFactory
	styles        styles
}

// newModel builds the interface model.
func newModel(cfg modelConfig) *model {
	styles := newStyles()

	input := textinput.New()
	input.Prompt = "› "
	input.Placeholder = "Ask the agent something"
	input.PromptStyle = styles.inputPrompt
	input.PlaceholderStyle = styles.dim

	phase := phasePicker
	if cfg.selected >= 0 {
		phase = phasePreparing
	}

	return &model{
		agents:        cfg.agents,
		selected:      cfg.selected,
		phase:         phase,
		input:         input,
		spinner:       spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(styles.dim)),
		viewport:      viewport.New(0, 0),
		newSession:    cfg.newSession,
		newRunContext: cfg.newRunContext,
		styles:        styles,
	}
}

// Init starts the interface.
func (m *model) Init() tea.Cmd {
	if m.phase == phasePreparing {
		return m.prepareSession()
	}
	return nil
}

// Update handles one message and returns the command it triggers.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	case sessionReadyMsg:
		m.enterChat(msg.session)
		return m, textinput.Blink
	case sessionFailedMsg:
		m.fatal = msg.err
		return m, tea.Quit
	case engineEventMsg:
		return m, m.handleEvent(msg.event)
	case eventsClosedMsg:
		if m.running {
			m.finishRun(engine.EndReasonError)
			m.refreshTranscript()
		}
		return m, nil
	case spinner.TickMsg:
		if !m.busy() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

// handleKey dispatches a key press to the current phase.
func (m *model) handleKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "ctrl+d":
		if !m.running {
			return m.quit()
		}
		return nil
	}

	switch m.phase {
	case phasePicker:
		return m.handlePickerKey(key)
	case phasePreparing:
		return nil
	default:
		return m.handleChatKey(key)
	}
}

// handlePickerKey moves the agent selection or starts the chosen agent.
func (m *model) handlePickerKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.agents)-1 {
			m.cursor++
		}
	case "enter":
		m.selected = m.cursor
		m.phase = phasePreparing
		return m.prepareSession()
	}
	return nil
}

// handleChatKey submits prompts, interrupts runs and scrolls the transcript.
func (m *model) handleChatKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		m.cancelRun()
		return nil
	case "enter":
		return m.submit()
	case "up", "down", "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(key)
		return cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return cmd
}

// prepareSession returns the command that creates the session of the selected
// agent.
func (m *model) prepareSession() tea.Cmd {
	prepare := m.newSession
	agentID := m.agents[m.selected].ID
	return func() tea.Msg {
		prepared, err := prepare(agentID)
		if err != nil {
			return sessionFailedMsg{err: err}
		}
		return sessionReadyMsg{session: prepared}
	}
}

// enterChat switches the interface to the conversation of a ready session.
func (m *model) enterChat(prepared Session) {
	m.session = prepared
	m.phase = phaseChat
	m.cancel = nil
	m.events = nil
	m.running = false
	m.transcript = transcript{}
	m.input.Reset()
	m.input.Focus()
	m.refreshTranscript()
}

// submit sends the prompt held by the input to the agent.
func (m *model) submit() tea.Cmd {
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" || m.running || m.session == nil {
		return nil
	}

	m.input.Reset()
	m.transcript.addUser(prompt)
	m.running = true
	m.refreshTranscript()

	ctx, cancel := m.newRunContext()
	m.cancel = cancel
	m.events = m.session.Run(ctx, prompt)

	return tea.Batch(m.spinner.Tick, waitForEvent(m.events))
}

// handleEvent folds one engine event into the interface state.
func (m *model) handleEvent(event engine.Event) tea.Cmd {
	m.transcript.apply(event)

	switch event.Type {
	case engine.EventMessageEnd:
		if event.Usage != nil {
			m.usageIn += event.Usage.InputTokens
			m.usageOut += event.Usage.OutputTokens
		}
	case engine.EventRunEnd:
		m.finishRun(event.Reason)
		m.refreshTranscript()
		return nil
	}

	m.refreshTranscript()
	return waitForEvent(m.events)
}

// finishRun marks the run as finished and reports unusual endings.
func (m *model) finishRun(reason engine.EndReason) {
	m.running = false
	m.cancel = nil
	m.events = nil

	switch reason {
	case engine.EndReasonMaxTurns:
		m.transcript.addNotice("the run reached the turn limit")
	case engine.EndReasonInterrupted:
		m.transcript.addNotice("the run was interrupted")
	}
}

// cancelRun interrupts the run in flight, if there is one.
func (m *model) cancelRun() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// quit interrupts the run in flight and exits the program.
func (m *model) quit() tea.Cmd {
	m.cancelRun()
	return tea.Quit
}

// Close releases the session, if there is one. It is safe to call repeatedly.
func (m *model) Close() {
	if m.session != nil {
		_ = m.session.Close()
		m.session = nil
	}
}

// busy reports whether a session is being prepared or a run is in flight.
func (m *model) busy() bool {
	return m.running || m.phase == phasePreparing
}

// resize applies the terminal size to the interface widgets.
func (m *model) resize(width, height int) {
	m.width = width
	m.height = height
	m.input.Width = max(1, width-2)
	m.viewport.Width = max(1, width)
	m.viewport.Height = max(1, height-chromeHeight)
	m.refreshTranscript()
}

// refreshTranscript renders the conversation into the viewport, keeping the
// view pinned to its bottom when it already was there.
func (m *model) refreshTranscript() {
	if m.width <= 0 {
		return
	}

	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.renderTranscript())
	if atBottom {
		m.viewport.GotoBottom()
	}
}

// waitForEvent returns the command that delivers the next event of a run.
func waitForEvent(events <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return eventsClosedMsg{}
		}
		return engineEventMsg{event: event}
	}
}
