package tui

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/filecomplete"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
)

// Reasons a command of the command center cannot run, shared by the commands
// that need an open session whose run is not in flight.
const (
	// noteNoSession explains that a command needs an open session.
	noteNoSession = "open a session first"
	// noteRunInFlight explains that the run in flight holds the session.
	noteRunInFlight = "a run is in flight"
)

// renamePrompt opens the input that names the session, which reads as the
// label of the session it is about to name.
const renamePrompt = "name: "

// renamePromptWidth is the number of columns the rename input spends on its
// prompt.
const renamePromptWidth = len(renamePrompt)

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

// wheelRows is the number of rows one notch of the mouse wheel scrolls the
// conversation, matching the step a common mouse sends per detent so the wheel
// reads like the arrows do.
const wheelRows = 3

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

// confirmWindow is how long the interface waits for the second press that
// confirms an action, such as leaving the interface or interrupting the run in
// flight, before dropping the pending request.
const confirmWindow = 3 * time.Second

// defaultDarkBackground is the terminal background assumed until the terminal
// reports the real one.
const defaultDarkBackground = true

// keyLeader opens the leader key that prefixes the commands of a session. It
// starts a two-key sequence, so a command never collides with a key the prompt
// or a phase already uses, and every command added to it keeps the same shape.
const keyLeader = "ctrl+x"

// Chords of the leader key that select what a session runs. Each one opens the
// same picker over its own roster, so the two selections behave alike.
const (
	// keyLeaderAgent changes the agent the branch runs.
	keyLeaderAgent = "a"

	// keyLeaderModel changes the model the branch runs.
	keyLeaderModel = "m"
)

// pickerMode selects what the picker offers, which the interface shows and what
// choosing an entry does.
type pickerMode int

const (
	// pickerNewAgent chooses the agent of a session that is about to be
	// created, which is the mode the picker opens in when a new session needs
	// an agent.
	pickerNewAgent pickerMode = iota
	// pickerAgent changes the agent the open conversation runs from now on.
	pickerAgent
	// pickerModel changes the model the open conversation runs from now on.
	pickerModel
)

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

// Keys of the session tree that fold it. Each one is a toggle or repeats
// safely, so a fold is one decision the reader can take without remembering
// whether it is already in effect, and a single key stays free of the pair of
// keys a fold and an unfold would take.
const (
	// keyFold toggles the turns that follow the highlighted turn of the tree,
	// which lets the reader walk a long tree a subtree at a time.
	keyFold = "ctrl+f"

	// keyFoldAll toggles the whole tree between folded and unfolded, folding
	// every turn that holds a subtree or unfolding the tree when every one of
	// them is already folded.
	keyFoldAll = "ctrl+a"

	// keyFoldOthers folds every turn that holds a subtree except the turns of
	// the branch the session runs, so the tree shows that branch whole beside
	// the branches left folded.
	keyFoldOthers = "ctrl+o"
)

// confirmAction is the action a confirmation waits to run, which is what the
// key that arms it does when pressed twice within confirmWindow.
type confirmAction int

const (
	// confirmNone reports that no confirmation is pending, the zero value.
	confirmNone confirmAction = iota
	// confirmInterrupt cancels the run in flight.
	confirmInterrupt
	// confirmQuit leaves the interface.
	confirmQuit
)

// confirmation is the action waiting for the second press of the key that
// armed it. The zero value reports that nothing is pending.
type confirmation struct {
	// action is the action the confirmation runs once it is confirmed.
	action confirmAction

	// key is the key that armed the request. Several keys can arm the same
	// action, so the request remembers the one the user pressed and names it
	// when it asks for the press that confirms it.
	key string

	// seq numbers the requests, so the timeout of a request that a newer press
	// already replaced can tell itself apart from the pending one.
	seq int
}

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
	// activityCompacting marks the conversation being summarized.
	activityCompacting
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

	// NoteOff replaces Note while the command cannot run, so the list explains
	// why instead of only fading the entry. It is nil when the reason is the
	// same for the whole command.
	NoteOff func(*model) string

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
		Label:   "Rename session",
		Note:    "name the session to find it again in the list",
		NoteOff: (*model).renameNote,
		Open:    (*model).renameSession,
		Enabled: (*model).renameReady,
	},
	{
		Label:   "Tree",
		Note:    "navigate the session tree and return to an earlier turn",
		Open:    (*model).openTree,
		Enabled: (*model).treeReady,
	},
	{
		Label:   "Switch agent",
		Note:    "run the conversation onwards with another agent",
		NoteOff: (*model).switchNote,
		Open:    (*model).openAgentPicker,
		Enabled: (*model).switchReady,
	},
	{
		Label:   "Switch model",
		Note:    "run the conversation onwards with another model",
		NoteOff: (*model).switchNote,
		Open:    (*model).openModelPicker,
		Enabled: (*model).switchReady,
	},
	{
		Label:   "Compact context",
		Note:    "summarize the oldest turns into a checkpoint",
		NoteOff: (*model).compactNote,
		Open:    (*model).compactContext,
		Enabled: (*model).compactReady,
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

	// DisplayedBranch returns the entries of the active branch the user reads,
	// with the newest compaction applied.
	DisplayedBranch() []session.Entry

	// Context reports the estimated context of the active branch, measured
	// against the context window of the session model.
	Context() (tokens.Report, error)

	// CompactRefusal reports why the active branch holds nothing to compact,
	// and false when it holds something. The interface offers the manual
	// compaction only when it holds something, and explains the reason when it
	// does not.
	CompactRefusal() (compaction.Refusal, bool)

	// Compact summarizes the active branch on demand and returns the channel
	// carrying its events. It ignores the compaction threshold.
	Compact(ctx context.Context) <-chan engine.Event

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

	// SetTitle names the session, an empty title removing the name it carries
	// and leaving the one derived from its first user message in its place.
	SetTitle(title string) error

	// ActiveAgent returns the identifier of the agent the branch of the
	// session runs now, which is the newest selection of the branch or the
	// agent the session was created with.
	ActiveAgent() string

	// SetAgent selects the agent the branch of the session runs from now on.
	// The selection belongs to the branch, so returning to an earlier turn
	// runs on the agent that was in effect there.
	SetAgent(ctx context.Context, id string) error

	// ActiveModel returns the provider/model reference the branch of the
	// session runs now, which is the newest selection of the branch or the
	// model the session was created with.
	ActiveModel() string

	// Models returns the provider/model references the session may run, which
	// is the roster the picker offers.
	Models() []string

	// SetModel selects the model the branch of the session runs from now on.
	// The selection belongs to the branch, so returning to an earlier turn
	// runs on the model that was in effect there.
	SetModel(ctx context.Context, ref string) error

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

// confirmTimeoutMsg reports that the confirmation window of a request expired
// without the second press that confirms it. The sequence identifies the
// request that armed it, so a stale timer cannot drop a newer request.
type confirmTimeoutMsg struct {
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

	// rename edits the name of the session while renaming is set.
	rename textinput.Model

	// renaming reports that the rename input holds the keys instead of the
	// query of the command center.
	renaming bool

	// renameErr reports the failure of the last change to the session, which
	// the command center shows instead of leaving it with the user.
	renameErr string

	session Session
	events  <-chan engine.Event
	cancel  context.CancelFunc
	running bool
	fatal   error

	// context is the last measurement of the active branch, shown by the chat
	// footer. It is cached because building the request it measures reads the
	// project instructions from disk, which must never happen once per frame.
	context tokens.Report

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

	// confirm is the action waiting for the second press of the key that armed
	// it, such as interrupting the run in flight or leaving the interface.
	confirm confirmation

	// leader reports that the leader key was pressed and the interface waits
	// for the key that completes the chord.
	leader bool

	// pickerMode is what the picker offers: the agent of a new session, the
	// agent of the open conversation, or the model of the open conversation.
	pickerMode pickerMode

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
		rename:        newRenameInput(styles, defaultDarkBackground),
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
	m.picker = newFilter(len(m.agents), m.pickerText, "Search agents", m.styles, m.hasDarkBG)
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

// pickerText returns the text of one entry of the picker that the query is
// matched against: the identifier and the description of an agent, or the
// reference of a model. The picker serves both rosters, so the text follows the
// mode the picker was opened in.
func (m *model) pickerText(index int) string {
	if m.pickerMode == pickerModel {
		return m.session.Models()[index]
	}
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

// newRenameInput builds the input that names the session, which shares the
// styles of the query inputs so it reads as another line of the command
// center.
func newRenameInput(base styles, isDark bool) textinput.Model {
	input := textinput.New()
	input.Prompt = renamePrompt
	input.Placeholder = "name, empty removes it"
	input.SetStyles(newFilterStyles(base, isDark))
	input.Blur()
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
	case tea.MouseWheelMsg:
		m.handleWheel(msg)
		return m, nil
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseMotionMsg:
		// The interface reports the mouse only for the wheel; every other
		// event is ignored so a click never reaches a widget.
		return m, nil
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
	case confirmTimeoutMsg:
		m.handleConfirmTimeout(msg)
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
		if m.renaming {
			var cmd tea.Cmd
			m.rename, cmd = m.rename.Update(msg)
			return cmd, true
		}
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
	m.rename.SetStyles(newFilterStyles(m.styles, isDark))
	m.tree.setStyles(m.styles, isDark)
	m.invalidateTranscript()
	m.refreshTranscript()
}

// handleKey dispatches a key press to the current phase. The leader key is
// read first, because its chord belongs to the interface rather than to the
// phase that holds the keys.
func (m *model) handleKey(key tea.KeyPressMsg) tea.Cmd {
	if cmd, handled := m.handleLeaderKey(key); handled {
		return cmd
	}

	switch key.String() {
	case "ctrl+c":
		return m.requestConfirm(confirmQuit, key.String())
	case "ctrl+d":
		if !m.running {
			return m.requestConfirm(confirmQuit, key.String())
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

// handleLeaderKey reads the leader sequence: the leader key arms it and the
// key that follows completes a chord. It reports whether it consumed the key,
// which is true for the leader itself and for every chord, so a chord never
// reaches the prompt as text. A key that completes no chord leaves the
// interface as it was, so a mistyped chord is harmless.
func (m *model) handleLeaderKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.leader {
		if key.String() != keyLeader {
			return nil, false
		}
		m.leader = true
		return nil, true
	}

	m.leader = false
	switch key.String() {
	case keyLeaderAgent:
		return m.openSwitchPicker(pickerAgent), true
	case keyLeaderModel:
		return m.openSwitchPicker(pickerModel), true
	}
	return nil, true
}

// openSwitchPicker opens the picker over the roster of what the conversation
// runs, which is where the leader chords and the command center lead. It needs
// an open session whose run is not in flight, because a store is not safe for
// concurrent use and a selection moves the branch a run appends to.
func (m *model) openSwitchPicker(mode pickerMode) tea.Cmd {
	if !m.switchReady() {
		return nil
	}

	m.pickerMode = mode
	m.picker.setCount(len(m.roster()))
	m.picker.reset()
	if index := slices.Index(m.roster(), m.activeRef()); index >= 0 {
		m.selectPickerEntry(index)
	}
	m.phase = phasePicker
	m.input.Blur()
	return nil
}

// openAgentPicker opens the picker over the agents, which the command center
// uses to change the agent of the conversation.
func (m *model) openAgentPicker() tea.Cmd {
	return m.openSwitchPicker(pickerAgent)
}

// openModelPicker opens the picker over the models of the configuration, which
// the command center uses to change the model of the conversation.
func (m *model) openModelPicker() tea.Cmd {
	return m.openSwitchPicker(pickerModel)
}

// roster returns the entries the picker offers in its current mode: the agent
// definitions of the interface, or the model references of the session.
func (m *model) roster() []string {
	if m.pickerMode == pickerModel && m.session != nil {
		return m.session.Models()
	}
	ids := make([]string, 0, len(m.agents))
	for _, definition := range m.agents {
		ids = append(ids, definition.ID)
	}
	return ids
}

// activeRef returns what the session runs in the current mode, so the picker
// opens on the agent or the model the conversation already runs.
func (m *model) activeRef() string {
	if m.session == nil {
		return ""
	}
	if m.pickerMode == pickerModel {
		return m.session.ActiveModel()
	}
	return m.session.ActiveAgent()
}

// selectPickerEntry puts the highlight of the picker on the entry at an index,
// which is where the picker opens when the session already runs on it.
func (m *model) selectPickerEntry(index int) {
	for position, shown := range m.picker.shown {
		if shown == index {
			m.picker.cursor = position
			return
		}
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
		if m.pickerMode != pickerNewAgent {
			return m.applySwitch(m.roster()[index])
		}
		m.selected = index
		m.phase = phasePreparing
		return m.prepareNewSession()
	case keyEscape:
		if m.picker.clear() {
			return nil
		}
		if m.pickerMode != pickerNewAgent {
			m.pickerMode = pickerNewAgent
			return m.showChat()
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

// applySwitch selects what the session runs from the active leaf onward, in the
// mode the picker was opened with, and shows the conversation again, which is
// where the change is read: the identity names the new agent or model, and
// every turn keeps the one that wrote it. The selection is appended to the
// branch, so the turns written before it stay on what ran them and another
// branch of the session keeps its own. A selection the session cannot honor is
// reported by the conversation, which refuses to send until another one is
// selected.
func (m *model) applySwitch(ref string) tea.Cmd {
	mode := m.pickerMode
	m.pickerMode = pickerNewAgent

	var err error
	if mode == pickerModel {
		err = m.session.SetModel(context.Background(), ref)
	} else {
		err = m.session.SetAgent(context.Background(), ref)
	}
	if err != nil {
		m.fatal = err
		return tea.Quit
	}

	// The conversation keeps its turns and changes what runs them from here
	// on, so the transcript is rebuilt to label and measure it with the
	// selection.
	m.reloadTranscript()
	m.refreshContext()
	return m.showChat()
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
	m.clearRename()
	m.mention = mention{}
	m.input.Blur()
	return nil
}

// closeSettings returns to the phase the command center was opened from.
func (m *model) closeSettings() tea.Cmd {
	m.clearRename()
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

// compactReady reports whether the conversation can be compacted on demand: it
// needs an open session, no run in flight, because a store is not safe for
// concurrent use, and something left to summarize.
func (m *model) compactReady() bool {
	if m.session == nil || m.running {
		return false
	}
	_, refused := m.session.CompactRefusal()
	return !refused
}

// compactNote explains why the manual compaction cannot run, ready to be shown
// in place of the note of the command. It returns nothing when the command can
// run, which lets the caller keep the default note. The reason comes from the
// same check the command runs on, so the explanation can never disagree with
// the availability of the command.
func (m *model) compactNote() string {
	switch {
	case m.session == nil:
		return noteNoSession
	case m.running:
		return noteRunInFlight
	}

	refusal, refused := m.session.CompactRefusal()
	if !refused {
		return ""
	}
	return compactionRefusalNote(refusal)
}

// compactionRefusalNote renders why a branch holds nothing to compact.
func compactionRefusalNote(refusal compaction.Refusal) string {
	switch refusal.Kind {
	case compaction.RefusalCompacted:
		return "the conversation already ends in a summary"
	case compaction.RefusalNoTurn:
		return "the conversation holds no turn to summarize"
	case compaction.RefusalShort:
		if refusal.Needed > 0 {
			return "needs " + formatTokens(refusal.Needed) +
				" more tokens of history"
		}
		return "there is not enough history to summarize yet"
	default:
		return "the session holds no conversation yet"
	}
}

// compactContext summarizes the conversation on demand, through the same code
// path, prompt and events as an automatic compaction. It ignores the
// compaction threshold, because the user asked for it, and it reports nothing
// about the origin of the compaction.
func (m *model) compactContext() tea.Cmd {
	if !m.compactReady() {
		return nil
	}

	show := m.showChat()
	m.setRunning(true)
	m.setActivity(activityWorking, "")
	m.syncLayout()
	m.refreshTranscript()

	m.runStart = time.Now()
	ctx, cancel := m.newRunContext()
	m.cancel = cancel
	m.events = m.session.Compact(ctx)

	return tea.Batch(show, m.spin(), streamEvents(m.events))
}

// renameReady reports whether the session can be named: it needs an open
// session whose run is not in flight, because a store is not safe for
// concurrent use.
func (m *model) renameReady() bool {
	return m.session != nil && !m.running
}

// renameNote explains why the session cannot be named, ready to be shown in
// place of the note of the command. It returns nothing when the command can
// run, which lets the caller keep the default note. The reason comes from the
// same check the command runs on, so the explanation can never disagree with
// the availability of the command.
func (m *model) renameNote() string {
	switch {
	case m.session == nil:
		return noteNoSession
	case m.running:
		return noteRunInFlight
	default:
		return ""
	}
}

// switchReady reports whether the agent of the session can be changed: it
// needs an open session whose run is not in flight, because a store is not
// safe for concurrent use.
func (m *model) switchReady() bool {
	return m.session != nil && !m.running
}

// switchNote explains why the agent of the session cannot be changed, ready to
// be shown in place of the note of the command. It returns nothing when the
// command can run, which lets the caller keep the default note. The reason
// comes from the same check the command runs on, so the explanation can never
// disagree with the availability of the command.
func (m *model) switchNote() string {
	switch {
	case m.session == nil:
		return noteNoSession
	case m.running:
		return noteRunInFlight
	default:
		return ""
	}
}

// renameSession opens the input that names the session, offering the name the
// user gave it. A session the user never named opens an empty input, so saving
// it unchanged never turns the name derived from the first message into one
// the user did not write.
func (m *model) renameSession() tea.Cmd {
	if !m.renameReady() {
		return nil
	}

	info := m.session.Info()
	m.renaming = true
	m.renameErr = ""
	m.commands.blur()
	if info.Named {
		m.rename.SetValue(info.Title)
	}
	m.rename.CursorEnd()
	return m.rename.Focus()
}

// handleRenameKey edits the name of the session: enter stores it and escape
// leaves it as it was.
func (m *model) handleRenameKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyEnter:
		return m.saveRename()
	case keyEscape:
		m.clearRename()
		return m.commands.focus()
	}

	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(key)
	return cmd
}

// saveRename names the session with the text typed into the input. An empty
// name removes the one the session carries, so the list falls back to the one
// derived from its first message.
func (m *model) saveRename() tea.Cmd {
	if err := m.session.SetTitle(m.rename.Value()); err != nil {
		m.renameErr = err.Error()
		return nil
	}

	m.clearRename()
	return m.commands.focus()
}

// clearRename drops the rename in progress, if any, leaving the command center
// as it found it. It is safe to call when no rename is in progress.
func (m *model) clearRename() {
	m.renaming = false
	m.renameErr = ""
	m.rename.Reset()
	m.rename.Blur()
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
	m.clearRename()
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
	m.tree.owner = m.session.Info().Agent
	m.tree.folded = make(map[string]bool)
	m.tree.nodes = treeNodes(m.tree.entries, m.tree.branch, m.tree.folded, m.session.Info().Agent)
	m.tree.filter.setCount(len(m.tree.nodes))
	m.tree.filter.reset()
	m.tree.editing = false
	m.tree.err = ""
	m.tree.tag.Reset()
	m.tree.focus()
}

// handleTreeKey walks the session tree: the query narrows it, the arrows move
// the highlight, enter returns the session to the highlighted turn, ctrl+t
// labels it and ctrl+f, ctrl+a and ctrl+o fold it, so a long tree is walked a
// subtree at a time. Escape clears the query first and then leaves the tree.
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
		m.tree.toggleFolded()
	case keyFoldAll:
		m.tree.toggleAll()
	case keyFoldOthers:
		m.tree.foldOthers()
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
	m.refreshContext()

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
	if m.renaming {
		return m.handleRenameKey(key)
	}

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

// handleWheel scrolls the conversation one notch of the mouse wheel. The wheel
// only acts on the conversation, and only while it is shown, so a wheel over a
// list neither scrolls the list nor moves the conversation the list hides.
//
// The wheel always scrolls the conversation, wherever the pointer rests: the
// prompt keeps the arrows for its own cursor, so a long message is read while a
// long prompt is written.
func (m *model) handleWheel(wheel tea.MouseWheelMsg) {
	if m.phase != phaseChat {
		return
	}

	switch wheel.Button {
	case tea.MouseWheelUp:
		m.conversation.scroll(-wheelRows)
	case tea.MouseWheelDown:
		m.conversation.scroll(wheelRows)
	}
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
	m.refreshContext()
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
	case engine.EventCompactionEnd:
		// The conversation the user reads changed: the summarized turns are
		// replaced by the checkpoint, which is what DisplayedBranch returns.
		m.reloadTranscript()
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
	case engine.EventCompactionStart:
		m.setActivity(activityCompacting, "")
	case engine.EventCompactionEnd:
		// The checkpoint is written, so the run goes back to the model.
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
	m.dropConfirm(confirmInterrupt)
	m.refreshContext()
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
// flight. It does nothing when there is no run to interrupt, so an escape
// pressed over an idle conversation arms nothing.
func (m *model) requestInterrupt() tea.Cmd {
	if !m.running {
		return nil
	}
	return m.requestConfirm(confirmInterrupt, keyEscape)
}

// requestConfirm asks the user to confirm an action: the first press of key
// arms the request and the second one, pressed within confirmWindow, runs it.
// Arming returns the timer that drops the request once the window expires, so a
// single stray press never triggers anything on its own. The request waits for
// the action rather than for the key, so any key that arms the same action
// confirms it.
func (m *model) requestConfirm(action confirmAction, key string) tea.Cmd {
	if m.confirm.action == action {
		m.clearConfirm()
		return m.runConfirmed(action)
	}

	m.confirm = confirmation{action: action, key: key, seq: m.confirm.seq + 1}
	seq := m.confirm.seq
	return tea.Tick(confirmWindow, func(time.Time) tea.Msg {
		return confirmTimeoutMsg{seq: seq}
	})
}

// runConfirmed runs the action the second press of its key confirmed.
func (m *model) runConfirmed(action confirmAction) tea.Cmd {
	switch action {
	case confirmInterrupt:
		m.cancelRun()
	case confirmQuit:
		return m.quit()
	}
	return nil
}

// handleConfirmTimeout drops a confirmation that expired without a second
// press, ignoring the timers of requests that a newer press already replaced.
func (m *model) handleConfirmTimeout(msg confirmTimeoutMsg) {
	if msg.seq != m.confirm.seq {
		return
	}
	m.clearConfirm()
}

// clearConfirm drops the pending confirmation, if any.
func (m *model) clearConfirm() {
	m.confirm.action = confirmNone
}

// dropConfirm drops the pending confirmation of an action that no longer
// applies, keeping any other one armed.
func (m *model) dropConfirm(action confirmAction) {
	if m.confirm.action == action {
		m.clearConfirm()
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
	m.input.SetWidth(max(1, width-inputGutterWidth))
	m.start.setWidth(width)
	m.picker.setWidth(width)
	m.commands.setWidth(width)
	m.rename.SetWidth(max(1, width-renamePromptWidth))
	m.tree.setWidth(width)

	// The lists window their entries to the rows the terminal leaves them, so
	// the tree can keep the margin of turns the reader needs to see what comes
	// next. The lists of the other phases show the same rows without a margin,
	// so their highlight rests on the bottom edge as before.
	rows := m.listRows()
	m.start.setWindowRows(rows)
	m.picker.setWindowRows(rows)
	m.commands.setWindowRows(rows)
	m.tree.filter.setWindowRows(rows)
	m.tree.filter.setWindowMargin(treeWindowMargin(rows))

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

// refreshContext recomputes the context figure of the active branch. It runs
// when the branch changes — a session opened, a run finished, a session
// returned to an earlier turn — never once per frame. A failed measurement
// keeps the previous figure instead of leaving the footer blank.
func (m *model) refreshContext() {
	if m.session == nil {
		return
	}

	report, err := m.session.Context()
	if err != nil {
		return
	}
	m.context = report
}

// reloadTranscript rebuilds the conversation from the active branch of the
// session, used whenever the branch the interface shows changes: a session that
// was opened and a session the user returned to an earlier turn.
func (m *model) reloadTranscript() {
	m.transcript = transcript{}
	m.transcript.load(m.session.DisplayedBranch(), m.session.Info().Agent)
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
