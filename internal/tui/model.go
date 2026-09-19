package tui

import (
	"context"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/filecomplete"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// brandRows is the number of rows the identity region of every phase
// occupies: the identity line, the separator under it and the padding below
// the separator.
const brandRows = 3

// inputBoxRows is the number of rows the input box occupies outside the input:
// its two borders and its vertical padding.
const inputBoxRows = 4

// activityRows is the number of rows the status block occupies between the
// transcript and the input box: two blank rows above the status line and one
// below, so the run status breathes instead of touching the content or the
// prompt.
const activityRows = 4

// chatFooterRows is the number of rows the chat footer occupies under the
// input box.
const chatFooterRows = 1

// listPromptRows is the number of rows a list phase spends on its question and
// the blank row after it.
const listPromptRows = 2

// listFooterRows is the number of rows that close a list phase: the blank row,
// the separator, the blank row after it and the hints.
const listFooterRows = 4

// chatChrome is the number of rows the chat phase reserves outside the
// transcript and the input.
const chatChrome = brandRows + activityRows + inputBoxRows + chatFooterRows

// listChrome is the number of rows the list phases (the start menu, the
// session list and the agent picker) reserve outside their rows.
const listChrome = brandRows + listPromptRows + listFooterRows

// inputGutterWidth is the number of columns the input box spends on its border
// and padding.
const inputGutterWidth = 4

// inputPromptWidth is the width of the prompt shown at the first input line.
const inputPromptWidth = 2

// minInputRows is the smallest the prompt input can grow to, a single line.
const minInputRows = 1

// maxInputRows is the tallest the prompt input grows to, so a long prompt
// never takes over the interface. The input scrolls its content beyond that.
const maxInputRows = 10

// minTranscriptRows is the number of conversation rows kept visible however
// tall the prompt input grows, so a long prompt never hides the answer.
const minTranscriptRows = 3

// maxEventBurst caps how many events one update folds, so a flood of events
// cannot stall the interface.
const maxEventBurst = 64

// interruptConfirmWindow is how long the interface waits for a second escape
// before dropping a pending interruption request.
const interruptConfirmWindow = 3 * time.Second

// defaultDarkBackground is the terminal background assumed until the terminal
// reports the real one.
const defaultDarkBackground = true

// Key names the interface handles, shared by the phase handlers.
const (
	keyUp      = "up"
	keyDown    = "down"
	keyEnter   = "enter"
	keySpace   = "space"
	keyEscape  = "esc"
	keyTab     = "tab"
	keyHome    = "home"
	keyEnd     = "end"
	keyPgUp    = "pgup"
	keyPgDown  = "pgdown"
	keyVimUp   = "k"
	keyVimDown = "j"
)

// activity is what a run is doing at the moment, reported by the single status
// spinner that closes the conversation.
type activity int

const (
	// activityIdle marks a run that is not in flight.
	activityIdle activity = iota
	// activityWorking marks the model producing an answer.
	activityWorking
	// activityThinking marks the model reasoning.
	activityThinking
	// activityTool marks a tool invocation running.
	activityTool
)

// preferences groups the options of the harness the command center toggles.
type preferences struct {
	// ExpandToolOutput shows the whole output of a tool. When it is off only
	// the trailing lines of the output are shown.
	ExpandToolOutput bool

	// ExpandThinking shows the whole reasoning of the model. When it is off
	// only the trailing lines of the reasoning are shown.
	ExpandThinking bool

	// RenderMarkdown formats the answers of the model as markdown. When it is
	// off the answers are shown as the model wrote them.
	RenderMarkdown bool
}

// defaultPreferences returns the options of a new interface: the output of a
// tool and the reasoning of the model stay compact, previewing only their
// trailing lines, and the answers of the model are formatted as markdown.
func defaultPreferences() preferences {
	return preferences{RenderMarkdown: true}
}

// preference is one option the command center lists and toggles.
type preference struct {
	// Label names the option in the list.
	Label string

	// Note describes what the option does, shown next to the label.
	Note string

	// IsOn reports whether the option is enabled.
	IsOn func(preferences) bool

	// Set enables or disables the option.
	Set func(*preferences, bool)
}

// preferencesList lists the options of the command center in display order.
var preferencesList = []preference{
	{
		Label: "Expand tool output",
		Note:  "show the whole output of a tool instead of its last lines",
		IsOn:  func(current preferences) bool { return current.ExpandToolOutput },
		Set:   func(current *preferences, on bool) { current.ExpandToolOutput = on },
	},
	{
		Label: "Expand thinking",
		Note:  "show the whole reasoning of the model instead of its last lines",
		IsOn:  func(current preferences) bool { return current.ExpandThinking },
		Set:   func(current *preferences, on bool) { current.ExpandThinking = on },
	},
	{
		Label: "Render markdown",
		Note:  "format the answers of the model as markdown",
		IsOn:  func(current preferences) bool { return current.RenderMarkdown },
		Set:   func(current *preferences, on bool) { current.RenderMarkdown = on },
	},
}

// Session is the agent session the interface drives. The harness package
// provides the production implementation.
type Session interface {
	// Info returns the session metadata.
	Info() session.Info

	// History returns the messages of the active branch in conversation order.
	History() []llm.Message

	// Run starts a run and returns the channel carrying its events.
	Run(ctx context.Context, prompt string) <-chan engine.Event

	// Close releases the session.
	Close() error
}

// sessionFactory prepares or opens a session identified by its id: the id of
// an agent for a new session, the id of the session to continue otherwise.
type sessionFactory func(id string) (Session, error)

// contextFactory creates the context of a run together with its cancel
// function. The model stores no context, only the cancel function of the run
// in flight.
type contextFactory func() (context.Context, context.CancelFunc)

// phase is the screen the interface shows.
type phase int

const (
	// phaseMenu asks the user to start a new session or continue a previous one.
	phaseMenu phase = iota
	// phaseSessions asks the user to choose a previous session.
	phaseSessions
	// phasePicker asks the user to choose the agent to run.
	phasePicker
	// phasePreparing creates or opens the session of the choice.
	phasePreparing
	// phaseChat shows the conversation.
	phaseChat
	// phaseSettings shows the command center with the harness options.
	phaseSettings
)

// Menu entries, indexed by the menu cursor.
const (
	// menuNew starts a new session.
	menuNew = iota
	// menuContinue continues a previous session.
	menuContinue

	// menuCount is the number of entries of the start menu.
	menuCount = menuContinue + 1
)

// sessionReadyMsg carries a prepared session into the interface.
type sessionReadyMsg struct {
	session Session
}

// sessionFailedMsg reports that a session could not be prepared.
type sessionFailedMsg struct {
	err error
}

// engineEventsMsg carries a burst of events of a running session.
type engineEventsMsg []engine.Event

// eventsClosedMsg reports that the event channel of a run was closed.
type eventsClosedMsg struct{}

// interruptTimeoutMsg reports that the confirmation window of an interruption
// request expired. The sequence identifies the request that armed it, so a
// stale timer cannot drop a newer request.
type interruptTimeoutMsg struct {
	seq int
}

// modelConfig configures a model.
type modelConfig struct {
	// agents lists the agent definitions the user may run.
	agents []agent.Agent

	// selected preselects the agent to run by index, or -1 to let the user
	// pick one.
	selected int

	// requested reports that the command line asked for the selected agent,
	// which skips the start menu and the agent picker.
	requested bool

	// sessions lists the previous sessions of the workspace, most recently
	// updated first.
	sessions []session.Info

	// newSession prepares a new session for an agent.
	newSession sessionFactory

	// resumeSession opens a previous session.
	resumeSession sessionFactory

	// newRunContext creates the context of a run.
	newRunContext contextFactory

	// scanFiles reads the project paths that complete a mention, or nil when
	// the interface offers no completion.
	scanFiles fileScanner
}

// model is the Bubble Tea model of the interactive interface.
type model struct {
	agents   []agent.Agent
	cursor   int
	selected int
	phase    phase

	sessions []session.Info
	menu     int
	chosen   int

	session  Session
	events   <-chan engine.Event
	cancel   context.CancelFunc
	running  bool
	fatal    error
	usageIn  int
	usageOut int

	// preparing names what the preparation phase is opening, shown by its
	// screen. It is set when the phase starts so the screen never has to
	// derive it from the current selection, which may not exist when a stored
	// session brings its own agent.
	preparing string

	// activity is what the run in flight is doing, shown by the status
	// spinner. activityTool names the tool it belongs to.
	activity     activity
	activityTool string

	// confirmInterrupt reports that an escape press armed an interruption
	// request that waits for a second press. interruptSeq numbers those
	// requests so a stale timeout cannot drop a newer one.
	confirmInterrupt bool
	interruptSeq     int

	preferences   preferences
	returnPhase   phase
	settingCursor int

	transcript   transcript
	conversation conversation
	mention      mention
	input        textarea.Model
	spinner      spinner

	// candidates holds the paths of the project the mentions complete
	// against, read again whenever a mention opens.
	candidates []filecomplete.Suggestion

	width     int
	height    int
	hasDarkBG bool

	newSession    sessionFactory
	resumeSession sessionFactory
	newRunContext contextFactory
	scanFiles     fileScanner
	styles        styles
	markdown      markdownRenderer
}

// newModel builds the interface model.
func newModel(cfg modelConfig) *model {
	styles := newStyles(defaultDarkBackground)

	input := textarea.New()
	input.Placeholder = "Ask the agent something"
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = minInputRows
	// The prompt grows with its content without bound; only its visible height
	// is capped, so the input scrolls instead of refusing further lines.
	input.MaxContentHeight = math.MaxInt
	input.MaxHeight = minInputRows
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
		key.WithHelp("shift+enter", "insert newline"),
	)
	input.SetPromptFunc(inputPromptWidth, inputPrompt)
	input.SetStyles(newInputStyles(styles, defaultDarkBackground))
	input.SetHeight(minInputRows)

	return &model{
		agents:        cfg.agents,
		selected:      cfg.selected,
		sessions:      cfg.sessions,
		phase:         startPhase(cfg),
		preparing:     selectedAgentID(cfg),
		preferences:   defaultPreferences(),
		input:         input,
		conversation:  newConversation(),
		hasDarkBG:     defaultDarkBackground,
		newSession:    cfg.newSession,
		resumeSession: cfg.resumeSession,
		newRunContext: cfg.newRunContext,
		scanFiles:     cfg.scanFiles,
		styles:        styles,
	}
}

// inputPrompt returns the prompt of one line of the input, shown only at the
// first one.
func inputPrompt(info textarea.PromptInfo) string {
	if info.LineNumber == 0 {
		return "› "
	}
	return strings.Repeat(" ", inputPromptWidth)
}

// startPhase returns the phase the interface opens with. The start menu needs
// previous sessions to continue, and the agent picker needs several agents to
// choose from.
func startPhase(cfg modelConfig) phase {
	switch {
	case cfg.requested:
		return phasePreparing
	case len(cfg.sessions) > 0:
		return phaseMenu
	case cfg.selected >= 0:
		return phasePreparing
	default:
		return phasePicker
	}
}

// selectedAgentID returns the ID of the preselected agent, or the empty
// string when the user has not chosen one. It lets the preparation screen name
// its agent before the selection exists.
func selectedAgentID(cfg modelConfig) string {
	if cfg.selected < 0 || cfg.selected >= len(cfg.agents) {
		return ""
	}
	return cfg.agents[cfg.selected].ID
}

// newInputStyles builds the styles of the prompt input.
func newInputStyles(base styles, isDark bool) textarea.Styles {
	input := textarea.DefaultStyles(isDark)
	input.Focused.Prompt = base.inputPrompt
	input.Focused.Placeholder = base.dim
	input.Focused.Text = lipgloss.NewStyle()
	// The defaults tint the line under the cursor and the end of the buffer,
	// which shows as a background band behind the input.
	input.Focused.CursorLine = lipgloss.NewStyle()
	input.Focused.EndOfBuffer = lipgloss.NewStyle()
	input.Blurred = input.Focused
	return input
}

// Init starts the interface.
func (m *model) Init() tea.Cmd {
	if m.phase == phasePreparing {
		return m.prepareNewSession()
	}
	return nil
}

// Update handles one message and returns the command it triggers.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case tea.BackgroundColorMsg:
		m.applyBackground(msg.IsDark())
		return m, nil
	case sessionReadyMsg:
		return m, tea.Batch(m.enterChat(msg.session), tea.RequestBackgroundColor)
	case sessionFailedMsg:
		m.fatal = msg.err
		return m, tea.Quit
	case engineEventsMsg:
		return m, m.handleEvents(msg)
	case filesScannedMsg:
		m.applyScan(msg)
		return m, nil
	case interruptTimeoutMsg:
		m.handleInterruptTimeout(msg)
		return m, nil
	case eventsClosedMsg:
		if m.running {
			m.finishRun(engine.EndReasonError)
			m.refreshTranscript()
		}
		return m, nil
	case spinnerStartMsg:
		if !m.busy() {
			return m, nil
		}
		return m, m.spinner.command()
	case spinnerTickMsg:
		if !m.busy() {
			return m, nil
		}
		m.spinner.advance()
		return m, m.spinner.command()
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, tea.Batch(cmd, m.syncPrompt())
	}
}

// applyBackground rebuilds the styles for the reported terminal background.
func (m *model) applyBackground(isDark bool) {
	if isDark == m.hasDarkBG {
		return
	}

	m.hasDarkBG = isDark
	m.styles = newStyles(isDark)
	m.input.SetStyles(newInputStyles(m.styles, isDark))
	m.invalidateTranscript()
	m.refreshTranscript()
}

// handleKey dispatches a key press to the current phase.
func (m *model) handleKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "ctrl+d":
		if !m.running {
			return m.quit()
		}
		return nil
	case "ctrl+p":
		return m.toggleSettings()
	}

	switch m.phase {
	case phaseMenu:
		return m.handleMenuKey(key)
	case phaseSessions:
		return m.handleSessionsKey(key)
	case phasePicker:
		return m.handlePickerKey(key)
	case phasePreparing:
		return nil
	case phaseSettings:
		return m.handleSettingsKey(key)
	default:
		return m.handleChatKey(key)
	}
}

// moveCursor returns the index that results from moving a cursor delta
// positions through a list of length items. The movement wraps around at both
// ends, so stepping down from the last item lands on the first one and
// stepping up from the first lands on the last, letting the user cycle through
// the list instead of getting stuck at its ends. An empty list keeps the
// cursor at its first index.
func moveCursor(cursor, delta, length int) int {
	if length <= 0 {
		return 0
	}
	return ((cursor+delta)%length + length) % length
}

// handleMenuKey moves the menu selection or starts the chosen action.
func (m *model) handleMenuKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyUp, keyVimUp:
		m.menu = moveCursor(m.menu, -1, menuCount)
	case keyDown, keyVimDown:
		m.menu = moveCursor(m.menu, 1, menuCount)
	case keyEnter:
		if m.menu == menuNew {
			return m.startNewSession()
		}
		m.phase = phaseSessions
	}
	return nil
}

// startNewSession moves to the agent picker, or prepares the only agent.
func (m *model) startNewSession() tea.Cmd {
	if m.selected < 0 {
		m.phase = phasePicker
		return nil
	}
	m.phase = phasePreparing
	return m.prepareNewSession()
}

// handleSessionsKey moves the session selection, opens the chosen session or
// returns to the menu.
func (m *model) handleSessionsKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyEscape:
		m.phase = phaseMenu
	case keyUp, keyVimUp:
		m.chosen = moveCursor(m.chosen, -1, len(m.sessions))
	case keyDown, keyVimDown:
		m.chosen = moveCursor(m.chosen, 1, len(m.sessions))
	case keyEnter:
		m.phase = phasePreparing
		return m.prepareStoredSession()
	}
	return nil
}

// handlePickerKey moves the agent selection or starts the chosen agent.
func (m *model) handlePickerKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyUp, keyVimUp:
		m.cursor = moveCursor(m.cursor, -1, len(m.agents))
	case keyDown, keyVimDown:
		m.cursor = moveCursor(m.cursor, 1, len(m.agents))
	case keyEnter:
		m.selected = m.cursor
		m.phase = phasePreparing
		return m.prepareNewSession()
	}
	return nil
}

// toggleSettings opens the command center, or closes it when it is already
// open. The session being prepared, a phase of its own, has no state to return
// to, so the command center ignores it.
func (m *model) toggleSettings() tea.Cmd {
	if m.phase == phasePreparing {
		return nil
	}
	if m.phase == phaseSettings {
		return m.closeSettings()
	}

	m.returnPhase = m.phase
	m.phase = phaseSettings
	m.settingCursor = 0
	m.mention = mention{}
	m.input.Blur()
	return nil
}

// closeSettings returns to the phase the command center was opened from and
// renders the conversation again with the current preferences.
func (m *model) closeSettings() tea.Cmd {
	m.phase = m.returnPhase
	if m.phase != phaseChat {
		return nil
	}

	m.refreshTranscript()
	return m.input.Focus()
}

// handleSettingsKey moves the cursor of the command center or toggles the
// option under it.
func (m *model) handleSettingsKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyEscape:
		return m.closeSettings()
	case keyUp, keyVimUp:
		m.settingCursor = moveCursor(m.settingCursor, -1, len(preferencesList))
	case keyDown, keyVimDown:
		m.settingCursor = moveCursor(m.settingCursor, 1, len(preferencesList))
	case keyEnter, keySpace:
		m.togglePreference(m.settingCursor)
	}
	return nil
}

// togglePreference flips one option and drops the rendered conversation, which
// changes with the preferences.
func (m *model) togglePreference(index int) {
	option := preferencesList[index]
	next := m.preferences
	option.Set(&next, !option.IsOn(next))
	m.preferences = next
	m.invalidateTranscript()
}

// handleChatKey submits prompts, interrupts runs and scrolls the transcript.
func (m *model) handleChatKey(key tea.KeyPressMsg) tea.Cmd {
	if cmd, handled := m.handleMentionKey(key); handled {
		return cmd
	}

	switch key.String() {
	case keyEscape:
		return m.requestInterrupt()
	case keyEnter:
		return m.submit()
	case keyUp, keyDown:
		// A multi-line prompt owns the arrows; a single-line one leaves them
		// to the transcript.
		if m.input.LineCount() > 1 {
			break
		}
		m.scrollTranscript(key)
		return nil
	case keyHome:
		m.conversation.scrollToTop()
		return nil
	case keyEnd:
		m.conversation.scrollToBottom()
		return nil
	case keyPgUp, keyPgDown:
		m.scrollTranscriptBlock(key)
		return nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return tea.Batch(cmd, m.syncPrompt())
}

// scrollTranscript moves the conversation window one row with the given arrow
// key.
func (m *model) scrollTranscript(key tea.KeyPressMsg) {
	m.conversation.scroll(scrollDelta(key))
}

// scrollDelta returns the rows an arrow key moves, negative towards the newest
// rows and positive towards the oldest ones.
func scrollDelta(key tea.KeyPressMsg) int {
	switch key.String() {
	case keyUp:
		return -1
	case keyDown:
		return 1
	}
	return 0
}

// scrollTranscriptBlock moves the conversation window to the previous block
// with Page Up, or to the next one with Page Down.
func (m *model) scrollTranscriptBlock(key tea.KeyPressMsg) {
	direction := 1
	if key.String() == keyPgUp {
		direction = -1
	}
	m.conversation.scrollBlock(direction)
}

// prepareNewSession returns the command that creates the session of the
// selected agent.
func (m *model) prepareNewSession() tea.Cmd {
	prepare := m.newSession
	agentID := m.agents[m.selected].ID
	m.preparing = agentID
	return tea.Batch(m.spin(), sessionCommand(func() (Session, error) { return prepare(agentID) }))
}

// prepareStoredSession returns the command that opens the selected session.
// The session brings its own agent, so the label names the session instead of
// an agent of the current selection, which may be empty.
func (m *model) prepareStoredSession() tea.Cmd {
	prepare := m.resumeSession
	info := m.sessions[m.chosen]
	m.preparing = info.Agent
	return tea.Batch(
		m.spin(),
		sessionCommand(func() (Session, error) { return prepare(info.ID) }),
	)
}

// sessionCommand returns the command that prepares a session outside the
// update loop, reporting a failure as a message.
func sessionCommand(prepare func() (Session, error)) tea.Cmd {
	return func() tea.Msg {
		prepared, err := prepare()
		if err != nil {
			return sessionFailedMsg{err: err}
		}
		return sessionReadyMsg{session: prepared}
	}
}

// enterChat switches the interface to the conversation of a ready session.
func (m *model) enterChat(prepared Session) tea.Cmd {
	m.session = prepared
	m.phase = phaseChat
	m.cancel = nil
	m.events = nil
	m.setRunning(false)
	m.setActivity(activityIdle, "")
	m.transcript = transcript{}
	m.transcript.load(prepared.History())
	m.mention = mention{}
	m.input.Reset()
	m.syncLayout()
	m.invalidateTranscript()
	m.refreshTranscript()
	return m.input.Focus()
}

// submit sends the prompt held by the input to the agent.
func (m *model) submit() tea.Cmd {
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" || m.running || m.session == nil {
		return nil
	}

	m.input.Reset()
	m.mention = mention{}
	m.transcript.addUser(prompt)
	m.setRunning(true)
	m.setActivity(activityWorking, "")
	m.syncLayout()
	m.refreshTranscript()

	ctx, cancel := m.newRunContext()
	m.cancel = cancel
	m.events = m.session.Run(ctx, prompt)

	return tea.Batch(m.spin(), streamEvents(m.events))
}

// handleEvents folds a burst of engine events into the interface and renders
// the conversation once for the whole burst.
func (m *model) handleEvents(events []engine.Event) tea.Cmd {
	for _, event := range events {
		m.applyEvent(event)
	}
	m.refreshTranscript()

	if m.events == nil {
		return nil
	}
	return streamEvents(m.events)
}

// applyEvent folds one engine event into the interface state.
func (m *model) applyEvent(event engine.Event) {
	m.transcript.apply(event)
	m.trackActivity(event)

	switch event.Type {
	case engine.EventMessageEnd:
		if event.Usage != nil {
			m.usageIn += event.Usage.InputTokens
			m.usageOut += event.Usage.OutputTokens
		}
	case engine.EventRunEnd:
		m.finishRun(event.Reason)
	}
}

// trackActivity records what the run is doing so the status spinner reports
// it: the model writing an answer, the model reasoning or a tool running. A
// finished tool hands back to the model, which is what the run waits for next.
func (m *model) trackActivity(event engine.Event) {
	switch event.Type {
	case engine.EventTextDelta:
		m.setActivity(activityWorking, "")
	case engine.EventThinkingDelta:
		m.setActivity(activityThinking, "")
	case engine.EventToolCall, engine.EventToolOutput:
		m.setActivity(activityTool, event.ToolName)
	case engine.EventToolResult, engine.EventRetry:
		m.setActivity(activityWorking, "")
	}
}

// setActivity records the activity of the run. The status line is rendered
// from it on every frame, so nothing else needs to be refreshed.
func (m *model) setActivity(next activity, tool string) {
	m.activity = next
	m.activityTool = tool
}

// spin resets the status spinner and returns the command that starts its
// animation. It is issued whenever a busy phase begins, either preparing a
// session or running one. The command is delivered at once; the spinner waits
// for the hold of its first frame when it arms it.
func (m *model) spin() tea.Cmd {
	m.spinner.reset()
	return func() tea.Msg { return spinnerStartMsg{} }
}

// setRunning records whether a run is in flight, dropping the rendered
// conversation when the state changes because it renders differently.
func (m *model) setRunning(running bool) {
	if m.running == running {
		return
	}
	m.running = running
	m.invalidateTranscript()
}

// finishRun marks the run as finished and reports unusual endings.
func (m *model) finishRun(reason engine.EndReason) {
	m.setRunning(false)
	m.cancel = nil
	m.events = nil
	m.setActivity(activityIdle, "")
	m.clearInterrupt()

	if reason == engine.EndReasonInterrupted {
		m.transcript.addNotice("the run was interrupted")
	}
}

// requestInterrupt asks the user to confirm the interruption of the run in
// flight. The first escape arms the confirmation and the second one cancels
// the run; the request expires after interruptConfirmWindow without a second
// press. It does nothing when no run is in flight.
func (m *model) requestInterrupt() tea.Cmd {
	if !m.running {
		return nil
	}
	if m.confirmInterrupt {
		m.clearInterrupt()
		m.cancelRun()
		return nil
	}

	m.confirmInterrupt = true
	m.interruptSeq++
	seq := m.interruptSeq
	return tea.Tick(interruptConfirmWindow, func(time.Time) tea.Msg {
		return interruptTimeoutMsg{seq: seq}
	})
}

// handleInterruptTimeout drops a confirmation that expired without a second
// press, ignoring the timers of requests that a newer press already replaced.
func (m *model) handleInterruptTimeout(msg interruptTimeoutMsg) {
	if msg.seq != m.interruptSeq {
		return
	}
	m.clearInterrupt()
}

// clearInterrupt drops any pending interruption confirmation.
func (m *model) clearInterrupt() {
	m.confirmInterrupt = false
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
	m.input.SetWidth(max(1, width-inputGutterWidth))
	m.syncInputHeight()
	m.invalidateTranscript()
	m.refreshTranscript()
}

// syncInputHeight caps the visible rows of the prompt input so a long prompt
// neither hides the conversation nor takes over the interface: the input grows
// with its content up to maxInputRows, or fewer on a short terminal where even
// that would leave no room for the transcript.
func (m *model) syncInputHeight() {
	room := m.height - chatChrome - minTranscriptRows
	m.input.MaxHeight = max(minInputRows, min(maxInputRows, room))
}

// syncLayout resizes the transcript when the input height changed.
func (m *model) syncLayout() {
	height := m.transcriptHeight()
	if height == m.conversation.height {
		return
	}
	m.conversation.setHeight(height)
	m.refreshTranscript()
}

// transcriptHeight returns the rows the transcript occupies, computed from the
// status block the terminal can actually afford.
func (m *model) transcriptHeight() int {
	return max(
		1,
		m.height-brandRows-m.activityHeight()-m.mentionHeight()-inputBoxRows-chatFooterRows-
			m.input.Height(),
	)
}

// activityHeight returns the rows the status block occupies between the
// conversation and the prompt. While a run is in flight it is the full block,
// with a blank row above and below the status line; once the run is over it
// keeps a single blank row, so the content never touches the input. A very
// short terminal falls back to a single row.
func (m *model) activityHeight() int {
	want := 1
	if m.running {
		want = activityRows
	}
	room := m.height - brandRows - inputBoxRows - chatFooterRows - m.input.Height() - minInputRows
	if room >= want {
		return want
	}
	return max(0, min(room, 1))
}

// mentionHeight returns the rows the completion popup occupies between the
// status block and the prompt, or zero when the popup is closed or the
// terminal cannot spare a row for it beyond the conversation the interface
// always keeps visible.
func (m *model) mentionHeight() int {
	items := len(m.mention.items)
	if items == 0 {
		return 0
	}

	room := m.height - brandRows - m.activityHeight() - inputBoxRows - chatFooterRows -
		m.input.Height() - minTranscriptRows
	if room <= 0 {
		return 0
	}
	return min(items, room)
}

// invalidateTranscript drops the rendered conversation, used whenever
// something it depends on changes: the terminal width, the styles, the
// preferences or the state of a run.
func (m *model) invalidateTranscript() {
	m.conversation.invalidate()
}

// refreshTranscript renders the entries of the conversation that changed into
// the window the interface shows.
func (m *model) refreshTranscript() {
	if m.phase != phaseChat || m.width <= 0 {
		return
	}

	m.conversation.setHeight(m.transcriptHeight())
	from := min(m.transcript.changedFrom(), m.conversation.blockCount())
	m.conversation.truncate(from)
	for index := from; index < len(m.transcript.entries); index++ {
		m.conversation.appendBlock(m.renderEntry(index), isTurn(m.transcript.entries[index].kind))
	}
	m.transcript.markRendered()
}

// streamEvents returns the command that delivers the next burst of events of a
// run. Folding the events that are already available into one message keeps a
// fast stream from costing one update and one frame per event.
func streamEvents(events <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-events
		if !ok {
			return eventsClosedMsg{}
		}

		burst := make(engineEventsMsg, 0, maxEventBurst)
		burst = append(burst, first)
		for len(burst) < maxEventBurst {
			select {
			case event, ok := <-events:
				if !ok {
					return burst
				}
				burst = append(burst, event)
			default:
				return burst
			}
		}
		return burst
	}
}
