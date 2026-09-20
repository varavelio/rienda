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

// noticeRows is the number of rows the block between the transcript and the
// input box occupies while it reports something that is not a run in flight: a
// blank row above the notice and one below, so the notice never touches the
// content or the prompt. It is the height of the full status block without the
// second blank row above the line, which the run keeps for the spinner.
const noticeRows = 3

// chatFooterRows is the number of rows the chat footer occupies under the
// input box.
const chatFooterRows = 1

// listPromptRows is the number of rows a list phase spends on its question and
// the blank row after it.
const listPromptRows = 2

// listFilterRows is the number of rows a list phase spends on the query input
// that narrows it and the blank row after it.
const listFilterRows = 2

// listFooterRows is the number of rows that close a list phase: the blank row,
// the separator, the blank row after it and the hints.
const listFooterRows = 4

// chatChrome is the number of rows the chat phase reserves outside the
// transcript and the input.
const chatChrome = brandRows + activityRows + inputBoxRows + chatFooterRows

// listChrome is the number of rows the list phases (the start list, the agent
// picker and the command center) reserve outside their rows.
const listChrome = brandRows + listPromptRows + listFilterRows + listFooterRows

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
	keyUp     = "up"
	keyDown   = "down"
	keyEnter  = "enter"
	keyEscape = "esc"
	keyTab    = "tab"
	keyHome   = "home"
	keyEnd    = "end"
	keyPgUp   = "pgup"
	keyPgDown = "pgdown"
)

// keyFold toggles the turns that follow the highlighted turn of the tree. It is
// a toggle instead of a pair of keys because folding is one decision, and a
// single key leaves the horizontal arrows free of a meaning the reader would
// have to remember.
const keyFold = "ctrl+f"

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

// preferences groups the options of the harness the command center flips.
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

// command is one entry of the command center. A command either opens another
// screen of the interface or flips an option of the harness in place.
type command struct {
	// Label names the command in the list.
	Label string

	// Note describes what the command does, shown next to the label.
	Note string

	// Open opens the screen of the command and returns the command the
	// interface runs next, or nil when the screen needs none. It is nil for
	// the commands that flip an option in place.
	Open func(*model) tea.Cmd

	// IsOn reports whether the option of the command is enabled. It is nil
	// for the commands that open a screen, which hold no state.
	IsOn func(preferences) bool

	// Set enables or disables the option of the command. It is nil for the
	// commands that open a screen.
	Set func(*preferences, bool)

	// Enabled reports whether the command can run right now. It is nil for
	// the commands that are always available.
	Enabled func(*model) bool
}

// commandList lists the commands of the command center in display order: the
// ones that open a screen lead the list, followed by the options of the
// harness.
var commandList = []command{
	{
		Label: "New session",
		Note:  "start a new session with an agent of your choice",
		Open:  (*model).startNewSession,
	},
	{
		Label: "Sessions",
		Note:  "start a new session or continue a previous one",
		Open:  (*model).openStart,
	},
	{
		Label:   "Tree",
		Note:    "navigate the session tree and return to an earlier turn",
		Open:    (*model).openTree,
		Enabled: (*model).treeReady,
	},
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

	// Branch returns the entries of the active branch in conversation order,
	// which is the conversation the interface shows.
	Branch() []session.Entry

	// Tree returns every entry of the session in append order, the branches
	// the user left behind included, which is what the tree screen navigates.
	Tree() []session.Entry

	// SetLeaf moves the active leaf of the session to the entry identified by
	// id, or before the first message when id is empty, so the next run
	// continues from it.
	SetLeaf(id string) error

	// SetTag replaces the tag of the entry identified by id, an empty tag
	// removing the one it carries.
	SetTag(id, tag string) error

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

// sessionScanner lists the sessions the start list offers, most recently
// updated first. It runs outside the update loop, so the interface never waits
// for the files to be read, and it is called again whenever the start list
// opens, so a session created while the interface runs shows up. The harness
// package provides the production implementation, which keeps the model free
// of the filesystem.
type sessionScanner func() []session.Info

// sessionsScannedMsg carries the sessions of the workspace read again.
type sessionsScannedMsg struct {
	items []session.Info
}

// phase is the screen the interface shows.
type phase int

const (
	// phaseStart asks the user to start a new session or continue a previous
	// one, both from the same list.
	phaseStart phase = iota
	// phasePicker asks the user to choose the agent to run.
	phasePicker
	// phasePreparing creates or opens the session of the choice.
	phasePreparing
	// phaseChat shows the conversation.
	phaseChat
	// phaseSettings shows the command center with the harness options.
	phaseSettings
	// phaseTree shows the tree of the session, where the user walks the turns
	// of the conversation and returns to an earlier one.
	phaseTree
)

// startItem is one entry of the start list: the offer to begin a new session,
// which always leads the list, or a previous session to continue.
type startItem struct {
	// newSession reports that the entry starts a new session instead of
	// opening a stored one.
	newSession bool

	// info is the previous session the entry continues. It is zero for the
	// entry that starts a new one.
	info session.Info
}

// key returns the identifier of the entry, which lets a list read again keep
// the highlight on the entry it held. The entry that starts a new session is
// identified by its kind, since it has no stored session behind it.
func (item startItem) key() string {
	if item.newSession {
		return ""
	}
	return item.info.ID
}

// startItems builds the entries of the start list: the offer to begin a new
// session, which always leads the list, followed by the stored sessions.
func startItems(sessions []session.Info) []startItem {
	items := make([]startItem, 0, len(sessions)+1)
	items = append(items, startItem{newSession: true})
	for _, info := range sessions {
		items = append(items, startItem{info: info})
	}
	return items
}

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
	// which skips the start list and the agent picker.
	requested bool

	// sessions lists the previous sessions of the workspace, most recently
	// updated first. It fills the start list when the interface opens.
	sessions []session.Info

	// scanSessions reads the sessions of the workspace again whenever the
	// start list opens, or nil when the interface never re-reads them.
	scanSessions sessionScanner

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
	// agents lists the agent definitions the user may run.
	agents []agent.Agent

	// selected is the index of the chosen agent, or -1 while the user has not
	// picked one.
	selected int

	// phase is the screen shown.
	phase phase

	// starts holds the entries of the start list: the offer to begin a new
	// session followed by the previous sessions of the workspace.
	starts []startItem

	// start narrows the start list, picker the agent definitions and commands
	// the entries of the command center, each with the fuzzy query the user
	// types.
	start    filter
	picker   filter
	commands filter

	session  Session
	events   <-chan engine.Event
	cancel   context.CancelFunc
	running  bool
	fatal    error
	usageIn  int
	usageOut int

	// runStart is when the run in flight started, kept to report how long the
	// turn takes. It is the zero time while no run is in flight.
	runStart time.Time

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

	// rewound reports that the session was moved back to an earlier turn, which
	// the block above the prompt announces until the next message is sent.
	rewound bool

	// fork reports that the next message opens a branch: the session was moved
	// back to a turn that already has turns after it, so what the user writes
	// starts a second attempt beside them. Sending the message clears it,
	// because the branch is then written.
	fork bool

	preferences preferences
	returnPhase phase

	transcript   transcript
	conversation conversation
	tree         tree
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
	scanSessions  sessionScanner
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

	built := &model{
		agents:        cfg.agents,
		selected:      cfg.selected,
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
		scanSessions:  cfg.scanSessions,
		styles:        styles,
	}
	built.tree = newTreeScreen(built.treeText, styles, defaultDarkBackground)
	built.buildLists(cfg.sessions)
	return built
}

// buildLists fills the lists the interface narrows by typing: the start list,
// with the offer of a new session and the stored ones, the agent picker and
// the commands of the command center. Every list reads the text of its items
// from the model, so it never holds a copy of them.
func (m *model) buildLists(sessions []session.Info) {
	m.starts = startItems(sessions)
	m.start = newFilter(len(m.starts), m.startText, "Search sessions", m.styles, m.hasDarkBG)
	m.picker = newFilter(len(m.agents), m.agentText, "Search agents", m.styles, m.hasDarkBG)
	m.commands = newFilter(
		len(commandList),
		m.commandText,
		"Search commands",
		m.styles,
		m.hasDarkBG,
	)
}

// startText returns the text of one entry of the start list that the query is
// matched against.
func (m *model) startText(index int) string {
	item := m.starts[index]
	if item.newSession {
		return "new session"
	}
	return sessionTitle(item.info) + " " + item.info.Agent
}

// agentText returns the text of one agent that the query is matched against.
func (m *model) agentText(index int) string {
	definition := m.agents[index]
	return definition.ID + " " + definition.Description
}

// commandText returns the text of one entry of the command center that the
// query is matched against.
func (m *model) commandText(index int) string {
	entry := commandList[index]
	return entry.Label + " " + entry.Note
}

// treeText returns the text of one turn of the tree that the query is matched
// against: the message it carries and the tag that labels it.
func (m *model) treeText(index int) string {
	return m.tree.nodes[index].search()
}

// inputPrompt returns the prompt of one line of the input, shown only at the
// first one.
func inputPrompt(info textarea.PromptInfo) string {
	if info.LineNumber == 0 {
		return "› "
	}
	return strings.Repeat(" ", inputPromptWidth)
}

// startPhase returns the phase the interface opens with. The start list needs
// previous sessions to continue, and the agent picker needs several agents to
// choose from.
func startPhase(cfg modelConfig) phase {
	switch {
	case cfg.requested:
		return phasePreparing
	case len(cfg.sessions) > 0:
		return phaseStart
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
	case sessionsScannedMsg:
		m.applySessions(msg)
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
		if cmd, handled := m.routeInput(msg); handled {
			return m, cmd
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, tea.Batch(cmd, m.syncPrompt())
	}
}

// routeInput forwards a message a key press does not carry, such as a paste,
// to the input the visible phase listens to. It reports whether a phase took
// the message; the chat prompt takes the rest.
func (m *model) routeInput(msg tea.Msg) (tea.Cmd, bool) {
	switch m.phase {
	case phaseStart:
		return m.start.update(msg), true
	case phasePicker:
		return m.picker.update(msg), true
	case phaseSettings:
		return m.commands.update(msg), true
	case phaseTree:
		return m.tree.update(msg), true
	}
	return nil, false
}

// applyBackground rebuilds the styles for the reported terminal background.
func (m *model) applyBackground(isDark bool) {
	if isDark == m.hasDarkBG {
		return
	}

	m.hasDarkBG = isDark
	m.styles = newStyles(isDark)
	m.input.SetStyles(newInputStyles(m.styles, isDark))
	m.start.setStyles(m.styles, isDark)
	m.picker.setStyles(m.styles, isDark)
	m.commands.setStyles(m.styles, isDark)
	m.tree.setStyles(m.styles, isDark)
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
	case "ctrl+t":
		// The tree takes the key itself: there it labels the turn under the
		// cursor instead of opening the screen.
		if m.phase != phaseTree {
			return m.openTree()
		}
	}

	switch m.phase {
	case phaseStart:
		return m.handleStartKey(key)
	case phasePicker:
		return m.handlePickerKey(key)
	case phasePreparing:
		return nil
	case phaseSettings:
		return m.handleSettingsKey(key)
	case phaseTree:
		return m.handleTreeKey(key)
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

// handleStartKey narrows the start list, moves its highlight and opens the
// chosen entry, either a new session or a stored one. Escape clears the query
// first and then returns to the conversation the list was opened over, when
// there is one.
func (m *model) handleStartKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyUp:
		m.start.move(-1)
	case keyDown:
		m.start.move(1)
	case keyEnter:
		return m.selectStart()
	case keyEscape:
		if m.start.clear() || m.session == nil {
			return nil
		}
		return m.showChat()
	default:
		return m.start.update(key)
	}
	return nil
}

// selectStart opens the highlighted entry of the start list: a new session,
// which moves to the agent picker or prepares the only agent, or a stored
// session, which is opened with the agent it already carries.
func (m *model) selectStart() tea.Cmd {
	index := m.start.selected()
	if index < 0 {
		return nil
	}

	item := m.starts[index]
	if item.newSession {
		return m.startNewSession()
	}

	m.phase = phasePreparing
	return m.prepareStoredSession(item.info)
}

// startNewSession asks for the agent of a new session, or prepares the one
// already selected, so the session that follows starts empty.
func (m *model) startNewSession() tea.Cmd {
	if m.selected < 0 {
		m.phase = phasePicker
		return nil
	}
	m.phase = phasePreparing
	return m.prepareNewSession()
}

// openStart shows the list that starts a new session or continues a previous
// one, opening it whole again and reading the stored sessions again, so a
// session created while the interface runs is offered.
func (m *model) openStart() tea.Cmd {
	m.start.reset()
	m.phase = phaseStart
	return m.scanSessionsCmd()
}

// scanSessionsCmd returns the command that reads the sessions of the workspace
// again and reports them as a sessionsScannedMsg. The read runs outside the
// update loop, so a slow filesystem never stalls the interface.
func (m *model) scanSessionsCmd() tea.Cmd {
	if m.scanSessions == nil {
		return nil
	}

	scan := m.scanSessions
	return func() tea.Msg { return sessionsScannedMsg{items: scan()} }
}

// applySessions replaces the sessions the start list offers with the ones read
// again, keeping the highlight on the entry it held whenever that entry is
// still offered, so the list gains the sessions created while the interface
// runs without moving under the user.
func (m *model) applySessions(msg sessionsScannedMsg) {
	held := m.highlightedStart()
	m.starts = startItems(msg.items)
	m.start.setCount(len(m.starts))

	for position, index := range m.start.shown {
		if m.starts[index].key() == held {
			m.start.cursor = position
			return
		}
	}
}

// highlightedStart returns the identity of the entry the start list
// highlights, or the empty string when it highlights none, which happens while
// the query matches no entry.
func (m *model) highlightedStart() string {
	if index := m.start.selected(); index >= 0 {
		return m.starts[index].key()
	}
	return ""
}

// handlePickerKey narrows the agent list, moves its highlight and starts the
// chosen agent. Escape clears the query first and then returns to the
// conversation the picker was opened over, when there is one, or to the start
// list.
func (m *model) handlePickerKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyUp:
		m.picker.move(-1)
	case keyDown:
		m.picker.move(1)
	case keyEnter:
		index := m.picker.selected()
		if index < 0 {
			return nil
		}
		m.selected = index
		m.phase = phasePreparing
		return m.prepareNewSession()
	case keyEscape:
		if m.picker.clear() {
			return nil
		}
		if m.session != nil {
			return m.showChat()
		}
		if len(m.starts) <= 1 {
			return nil
		}
		return m.openStart()
	default:
		return m.picker.update(key)
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
	m.commands.clear()
	m.mention = mention{}
	m.input.Blur()
	return nil
}

// closeSettings returns to the phase the command center was opened from.
func (m *model) closeSettings() tea.Cmd {
	if m.returnPhase != phaseChat {
		m.phase = m.returnPhase
		return nil
	}
	return m.showChat()
}

// treeReady reports whether the session tree can be shown and walked: it needs
// an open session whose run is not in flight, because moving the session while
// the agent works would leave the run writing to a branch the user abandoned.
func (m *model) treeReady() bool {
	return m.session != nil && !m.running
}

// openTree shows the tree of the open session: the turns of its conversation,
// the branch the session runs and the tags that label them. The tree always
// returns to the conversation it walks.
func (m *model) openTree() tea.Cmd {
	if !m.treeReady() {
		return nil
	}

	m.buildTree()
	m.phase = phaseTree
	m.mention = mention{}
	m.input.Blur()
	return nil
}

// closeTree shows the conversation again, the screen the tree walks, and
// focuses the prompt.
func (m *model) closeTree() tea.Cmd {
	return m.showChat()
}

// buildTree fills the tree screen from the open session: the turns of its
// conversation, the query that narrows them and the input that labels one. The
// query opens clean and the highlight lands on the turn the session is at, so
// the tree opens where the conversation stands.
func (m *model) buildTree() {
	m.tree.entries = m.session.Tree()
	m.tree.branch = m.session.Branch()
	m.tree.folded = make(map[string]bool)
	m.tree.nodes = treeNodes(m.tree.entries, m.tree.branch, m.tree.folded)
	m.tree.filter.setCount(len(m.tree.nodes))
	m.tree.filter.reset()
	m.tree.editing = false
	m.tree.err = ""
	m.tree.tag.Reset()
	m.tree.focus()
}

// handleTreeKey walks the session tree: the query narrows it, the arrows move
// the highlight, enter returns the session to the highlighted turn, ctrl+t
// labels it and ctrl+f folds or unfolds the turns that follow it, so a long
// tree is walked a subtree at a time. Escape clears the query first and then
// leaves the tree.
func (m *model) handleTreeKey(key tea.KeyPressMsg) tea.Cmd {
	if m.tree.editing {
		return m.handleTagKey(key)
	}

	switch key.String() {
	case keyUp:
		m.tree.filter.move(-1)
	case keyDown:
		m.tree.filter.move(1)
	case keyFold:
		m.foldTree()
	case keyEnter:
		return m.rewind()
	case keyEscape:
		if m.tree.filter.clear() {
			return nil
		}
		return m.closeTree()
	case "ctrl+t":
		return m.editTag()
	default:
		return m.tree.filter.update(key)
	}
	return nil
}

// foldTree hides or shows the turns that follow the highlighted one, which lets
// the reader walk a long tree a subtree at a time. The highlight stays on the
// turn it holds, or climbs to the turn that folded the subtree it was in.
func (m *model) foldTree() {
	if index := m.tree.filter.selected(); index >= 0 {
		m.tree.foldChildren(index)
	}
}

// handleTagKey edits the tag of the highlighted turn: enter stores it and
// escape leaves it as it was.
func (m *model) handleTagKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyEnter:
		return m.saveTag()
	case keyEscape:
		return m.closeTag()
	}

	var cmd tea.Cmd
	m.tree.tag, cmd = m.tree.tag.Update(key)
	return cmd
}

// editTag opens the tag of the highlighted turn for editing, offering the tag
// it already carries.
func (m *model) editTag() tea.Cmd {
	index := m.tree.filter.selected()
	if index < 0 {
		return nil
	}

	m.tree.editing = true
	m.tree.err = ""
	m.tree.filter.blur()
	m.tree.tag.SetValue(m.tree.nodes[index].entry.Tag)
	m.tree.tag.CursorEnd()
	return m.tree.tag.Focus()
}

// saveTag stores the tag typed for the highlighted turn. A leading sharp is
// dropped, so a tag written the way the tree shows it reads the same, and an
// empty tag removes the one the turn carried.
func (m *model) saveTag() tea.Cmd {
	index := m.tree.filter.selected()
	if index < 0 {
		return m.closeTag()
	}

	tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.tree.tag.Value()), "#"))
	if err := m.session.SetTag(m.tree.nodes[index].entry.ID, tag); err != nil {
		m.tree.err = err.Error()
		return nil
	}

	m.tree.nodes[index].entry.Tag = tag
	return m.closeTag()
}

// closeTag leaves the tag input and hands the keys back to the query.
func (m *model) closeTag() tea.Cmd {
	m.tree.editing = false
	m.tree.tag.Reset()
	m.tree.tag.Blur()
	return m.tree.filter.focus()
}

// rewind returns the session to the turn the tree highlights: the conversation
// keeps the branch that leads to it, and the turns written after it stay in the
// tree as a branch of their own. Returning to a prompt rewinds to the turn
// before it and offers the prompt in the input, so the user edits it and sends
// it again, which is the same as returning to the answer it followed. The next
// message opens a branch when the turn it hangs from already has turns after
// it.
func (m *model) rewind() tea.Cmd {
	index := m.tree.filter.selected()
	if index < 0 {
		return nil
	}

	node := m.tree.nodes[index]
	target, prompt := node.entry.ID, ""
	if node.entry.Message.Role == llm.RoleUser {
		target = node.entry.ParentID
		prompt = node.text
	}
	if err := m.session.SetLeaf(target); err != nil {
		m.tree.err = err.Error()
		return nil
	}

	m.rewound = true
	m.fork = m.tree.forks(index)
	m.input.SetValue(prompt)
	m.reloadTranscript()

	// The reader lands on the newest turn of the branch the session returned
	// to, which is where the conversation goes on.
	show := m.showChat()
	m.conversation.scrollToBottom()
	return show
}

// showChat shows the open conversation again, rendering what changed while the
// interface was away, and focuses the prompt.
func (m *model) showChat() tea.Cmd {
	m.phase = phaseChat
	m.refreshTranscript()
	return m.input.Focus()
}

// handleSettingsKey narrows the commands of the command center, moves its
// highlight and activates the command under it. Escape clears the query first
// and then closes the command center.
func (m *model) handleSettingsKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyUp:
		m.commands.move(-1)
	case keyDown:
		m.commands.move(1)
	case keyEnter:
		return m.activateCommand(m.commands.selected())
	case keyEscape:
		if m.commands.clear() {
			return nil
		}
		return m.closeSettings()
	default:
		return m.commands.update(key)
	}
	return nil
}

// activateCommand runs the command the command center highlights: it opens the
// screen of a command that moves the interface, or flips the option of one that
// configures the harness. A flipped option drops the rendered conversation,
// which changes with the preferences. It does nothing when no command is
// highlighted, which happens while the query matches none.
func (m *model) activateCommand(index int) tea.Cmd {
	if index < 0 {
		return nil
	}

	entry := commandList[index]
	if entry.Open != nil {
		if entry.Enabled != nil && !entry.Enabled(m) {
			return nil
		}
		return entry.Open(m)
	}

	next := m.preferences
	entry.Set(&next, !entry.IsOn(next))
	m.preferences = next
	m.invalidateTranscript()
	return nil
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
// selected agent, releasing the session it replaces.
func (m *model) prepareNewSession() tea.Cmd {
	m.abandonSession()

	prepare := m.newSession
	agentID := m.agents[m.selected].ID
	m.preparing = agentID
	return tea.Batch(m.spin(), sessionCommand(func() (Session, error) { return prepare(agentID) }))
}

// prepareStoredSession returns the command that opens the given session,
// releasing the session it replaces. The session brings its own agent, so the
// label names the session instead of an agent of the current selection, which
// may be empty.
func (m *model) prepareStoredSession(info session.Info) tea.Cmd {
	m.abandonSession()

	prepare := m.resumeSession
	m.preparing = info.Agent
	return tea.Batch(
		m.spin(),
		sessionCommand(func() (Session, error) { return prepare(info.ID) }),
	)
}

// abandonSession releases the session the interface leaves behind: it
// interrupts the run in flight and closes the session, so it can neither keep
// running nor hold its file once another session takes its place. It does
// nothing when no session is open.
func (m *model) abandonSession() {
	if m.session == nil {
		return
	}

	if m.running {
		m.cancelRun()
		m.finishRun(engine.EndReasonInterrupted)
	}
	m.Close()
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
	m.rewound = false
	m.fork = false
	m.setRunning(false)
	m.setActivity(activityIdle, "")
	m.mention = mention{}
	m.input.Reset()
	m.syncLayout()
	m.reloadTranscript()
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
	m.rewound = false
	m.fork = false
	m.transcript.addUser(prompt)
	m.setRunning(true)
	m.setActivity(activityWorking, "")
	m.syncLayout()
	m.refreshTranscript()

	m.runStart = time.Now()
	ctx, cancel := m.newRunContext()
	m.cancel = cancel
	m.events = m.session.Run(ctx, prompt)

	return tea.Batch(m.spin(), streamEvents(m.events))
}

// handleEvents folds a burst of engine events into the interface and renders
// the conversation once for the whole burst. A burst that arrives after its run
// was abandoned is dropped, so a late event cannot reach the session that
// replaced it.
func (m *model) handleEvents(events []engine.Event) tea.Cmd {
	if m.events == nil {
		return nil
	}

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
	m.recordElapsed()
	if reason == engine.EndReasonInterrupted {
		m.transcript.addNotice("the run was interrupted")
	}

	m.setRunning(false)
	m.cancel = nil
	m.events = nil
	m.setActivity(activityIdle, "")
	m.clearInterrupt()
}

// recordElapsed closes the turn in flight with the time the agent worked on it,
// and clears the start of the run. It does nothing when no run was in flight.
func (m *model) recordElapsed() {
	if m.runStart.IsZero() {
		return
	}

	m.transcript.finishTurn(time.Since(m.runStart))
	m.runStart = time.Time{}
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
	m.start.setWidth(width)
	m.picker.setWidth(width)
	m.commands.setWidth(width)
	m.tree.setWidth(width)
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
// with two blank rows above the status line and one below; while it announces
// that the next message opens a branch it keeps a blank row above the notice
// and one below; once neither holds it keeps a single blank row, so the content
// never touches the input. A very short terminal falls back to a single row.
func (m *model) activityHeight() int {
	want := 1
	switch {
	case m.running:
		want = activityRows
	case m.rewound:
		want = noticeRows
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

// reloadTranscript rebuilds the conversation from the active branch of the
// session, used whenever the branch the interface shows changes: a session that
// was opened and a session the user returned to an earlier turn.
func (m *model) reloadTranscript() {
	m.transcript = transcript{}
	m.transcript.load(m.session.Branch())
	m.invalidateTranscript()
	m.refreshTranscript()
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
