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
	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
)

// fakeSession is a scripted Session implementation. It serves the entries it
// holds as its whole tree and, since it branches nothing of its own, as its
// active branch too: the tests that exercise branching open a session over a
// real store instead.
type fakeSession struct {
	info         session.Info
	entries      []session.Entry
	events       chan engine.Event
	prompts      []string
	leaves       []string
	tags         map[string]string
	titles       []string
	agents       []string
	activeAgent  string
	modelRefs    []string
	models       []string
	activeModel  string
	thinking     string
	agentErr     error
	modelErr     error
	leafErr      error
	tagErr       error
	titleErr     error
	context      tokens.Report
	contextErr   error
	runnable     engine.RunnableRefusal
	stopped      bool
	refusal      compaction.Refusal
	refused      bool
	compact      chan engine.Event
	compacted    int
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
		activeAgent: "coder",
		activeModel: "fake/test-model",
		events:      make(chan engine.Event, 16),
		canceled:    make(chan struct{}),
	}
}

// Info returns the session metadata.
func (s *fakeSession) Info() session.Info { return s.info }

// Branch returns the entries of the active branch of the session.
func (s *fakeSession) Branch() []session.Entry { return s.entries }

// Tree returns the stored entries of the session.
func (s *fakeSession) Tree() []session.Entry { return s.entries }

// DisplayedBranch returns the entries of the active branch the user reads.
func (s *fakeSession) DisplayedBranch() []session.Entry { return s.entries }

// Context returns the scripted context report of the session.
func (s *fakeSession) Context() (tokens.Report, error) { return s.context, s.contextErr }

// Runnable reports the scripted reason the branch holds nothing to run.
func (s *fakeSession) Runnable() (engine.RunnableRefusal, bool) { return s.runnable, s.stopped }

// CompactRefusal reports the scripted reason the manual compaction cannot run.
func (s *fakeSession) CompactRefusal() (compaction.Refusal, bool) { return s.refusal, s.refused }

// Compact records the request and returns the scripted event channel.
func (s *fakeSession) Compact(ctx context.Context) <-chan engine.Event {
	s.compacted++
	if s.compact != nil {
		return s.compact
	}
	return s.events
}

// SetLeaf records the entry the session is moved to.
func (s *fakeSession) SetLeaf(id string) error {
	if s.leafErr != nil {
		return s.leafErr
	}
	s.leaves = append(s.leaves, id)
	return nil
}

// SetTag records the tag of an entry.
func (s *fakeSession) SetTag(id, tag string) error {
	if s.tagErr != nil {
		return s.tagErr
	}
	if s.tags == nil {
		s.tags = make(map[string]string)
	}
	s.tags[id] = tag
	return nil
}

// SetTitle records the name given to the session.
func (s *fakeSession) SetTitle(title string) error {
	if s.titleErr != nil {
		return s.titleErr
	}
	s.titles = append(s.titles, title)
	s.info.Title = title
	s.info.Named = title != ""
	return nil
}

// ActiveAgent returns the scripted agent the session runs.
func (s *fakeSession) ActiveAgent() string { return s.activeAgent }

// SetAgent records the agent the session is moved to.
func (s *fakeSession) SetAgent(_ context.Context, id string) error {
	if s.agentErr != nil {
		return s.agentErr
	}
	s.agents = append(s.agents, id)
	s.activeAgent = id
	return nil
}

// ActiveModel returns the scripted model the session runs.
func (s *fakeSession) ActiveModel() string { return s.activeModel }

// ThinkingLevel returns the scripted thinking level of the model the session
// runs.
func (s *fakeSession) ThinkingLevel() string { return s.thinking }

// Models returns the scripted model roster of the session.
func (s *fakeSession) Models() []string { return s.models }

// SetModel records the model the session is moved to.
func (s *fakeSession) SetModel(_ context.Context, ref string) error {
	if s.modelErr != nil {
		return s.modelErr
	}
	s.modelRefs = append(s.modelRefs, ref)
	s.activeModel = ref
	return nil
}

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
	pressCtrlT  = tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	pressCtrlF  = tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}
	pressCtrlA  = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	pressCtrlO  = tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	pressCtrlX  = tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}
	pressA      = tea.KeyPressMsg{Code: 'a'}
	pressM      = tea.KeyPressMsg{Code: 'm'}
	pressPgUp   = tea.KeyPressMsg{Code: tea.KeyPgUp}
	pressPgDown = tea.KeyPressMsg{Code: tea.KeyPgDown}
	wheelUp     = tea.MouseWheelMsg{Button: tea.MouseWheelUp}
	wheelDown   = tea.MouseWheelMsg{Button: tea.MouseWheelDown}
	pressHome   = tea.KeyPressMsg{Code: tea.KeyHome}
	pressEnd    = tea.KeyPressMsg{Code: tea.KeyEnd}
)

// storeSession drives the interface over a real session store, so the tests
// exercise the branching the store enforces instead of a reimplementation of
// it.
type storeSession struct {
	t         *testing.T
	dir       string
	store     *session.Store
	events    chan engine.Event
	prompts   []string
	modelRefs []string
	thinking  string
}

// newStoreSession opens a session whose store holds the given messages, linked
// in one branch, so the interface can return to any of its turns.
func newStoreSession(t *testing.T, messages ...llm.Message) *storeSession {
	t.Helper()

	dir := t.TempDir()
	store, err := session.Create(t.Context(), dir, session.Header{
		Agent: "coder",
		Model: "fake/test-model",
	}, id.NewIDGenerator())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	for _, message := range messages {
		_, err := store.Append(t.Context(), session.Entry{Message: message})
		require.NoError(t, err)
	}
	return &storeSession{t: t, dir: dir, store: store, events: make(chan engine.Event, 16)}
}

// Info returns the session metadata.
func (s *storeSession) Info() session.Info { return s.store.Info() }

// Branch returns the entries of the active branch.
func (s *storeSession) Branch() []session.Entry { return s.store.Branch() }

// Tree returns every entry of the session.
func (s *storeSession) Tree() []session.Entry { return s.store.Entries() }

// DisplayedBranch returns the entries of the active branch the user reads.
func (s *storeSession) DisplayedBranch() []session.Entry { return s.store.DisplayedBranch() }

// Context reports a measurement derived from the active branch, so the tests
// can assert that the figure follows the branch the session runs.
func (s *storeSession) Context() (tokens.Report, error) {
	used := len(s.store.Branch()) * 100
	return tokens.Measure(used, 1000), nil
}

// Runnable reports whether the branch the store runs still has an agent and a
// model the session may run. A store-backed session knows no roster of its
// own, so the agent the store runs is trusted, which keeps the interface tests
// over a real store running their branch as the store enforces it.
func (s *storeSession) Runnable() (engine.RunnableRefusal, bool) {
	return engine.RunnableRefusal{}, false
}

// CompactRefusal reports why the active branch holds nothing to compact, and
// false when it holds something. The readiness is derived from the branch the
// fake session holds, exactly as the engine derives it, so the interface tests
// exercise the same contract.
func (s *storeSession) CompactRefusal() (compaction.Refusal, bool) {
	branch := s.store.Branch()
	if len(branch) > 1 {
		return compaction.Refusal{}, false
	}
	return compaction.Refusal{Kind: compaction.RefusalShort, Needed: 20000}, true
}

// Compact returns the scripted event channel, recording the request.
func (s *storeSession) Compact(context.Context) <-chan engine.Event { return s.events }

// SetLeaf moves the active leaf of the store.
//
//nolint:wrapcheck // the session reports the failure of the store as it is.
func (s *storeSession) SetLeaf(id string) error { return s.store.SetLeaf(id) }

// SetTag labels an entry of the store.
//
//nolint:wrapcheck // the session reports the failure of the store as it is.
func (s *storeSession) SetTag(id, tag string) error { return s.store.SetTag(id, tag) }

// SetTitle names the session in the store.
//
//nolint:wrapcheck // the session reports the failure of the store as it is.
func (s *storeSession) SetTitle(title string) error { return s.store.SetTitle(title) }

// ActiveAgent reports the agent the active branch of the store runs.
func (s *storeSession) ActiveAgent() string { return s.store.ActiveAgent() }

// ActiveModel reports the model the active branch of the store runs.
func (s *storeSession) ActiveModel() string { return s.store.ActiveModel() }

// ThinkingLevel reports the scripted thinking level of the model the active
// branch runs. A store-backed session never declares one, so tests that need a
// level set it on the model.
func (s *storeSession) ThinkingLevel() string { return s.thinking }

// Models reports the models a store-backed session may run. The store itself
// holds no roster, so the interface tests that need one set it on the model.
func (s *storeSession) Models() []string { return s.modelRefs }

// SetModel selects the model of the active branch of the store.
func (s *storeSession) SetModel(ctx context.Context, ref string) error {
	s.modelRefs = append(s.modelRefs, ref)
	//nolint:wrapcheck // the session reports the failure of the store as it is.
	return s.store.SetModel(ctx, ref)
}

// SetAgent selects the agent of the active branch of the store.
func (s *storeSession) SetAgent(ctx context.Context, id string) error {
	//nolint:wrapcheck // the session reports the failure of the store as it is.
	return s.store.SetAgent(ctx, id)
}

// Run records the prompt, appends it to the store the way a run does, so the
// tests see the branch a run opens, and returns the scripted event channel.
func (s *storeSession) Run(ctx context.Context, prompt string) <-chan engine.Event {
	s.prompts = append(s.prompts, prompt)

	_, err := s.store.Append(ctx, session.Entry{Message: textMessage(llm.RoleUser, prompt)})
	require.NoError(s.t, err)
	return s.events
}

// Close releases the store.
//
//nolint:wrapcheck // the session reports the failure of the store as it is.
func (s *storeSession) Close() error { return s.store.Close() }

// textMessage builds a message carrying a single text block.
func textMessage(role llm.Role, text string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

// storeChat opens an interface over a store-backed session holding the given
// messages, showing the conversation it runs.
func storeChat(t *testing.T, messages ...llm.Message) (*model, *storeSession) {
	t.Helper()

	stored := newStoreSession(t, messages...)
	m := newTestModelWith(t, modelConfig{
		agents:     []agent.Agent{{ID: "coder"}},
		selected:   0,
		newSession: func(string) (Session, error) { return stored, nil },
	})
	update(t, m, windowMsg(80, 24))
	run(t, m, m.Init())
	require.Equal(t, phaseChat, m.phase)

	return m, stored
}

// treeModel opens an interface over a store-backed session holding the given
// messages and shows its tree, which is where the tests of the tree screen
// start.
func treeModel(t *testing.T, messages ...llm.Message) (*model, *storeSession) {
	t.Helper()

	m, stored := storeChat(t, messages...)
	update(t, m, pressCtrlT)
	require.Equal(t, phaseTree, m.phase)
	return m, stored
}

// pickTurn moves the highlight of the tree to the turn that carries the given
// message, which lets the tests return to a turn without counting rows.
func pickTurn(t *testing.T, m *model, message string) {
	t.Helper()

	for index, node := range m.tree.nodes {
		if node.text != message {
			continue
		}
		for position, shown := range m.tree.filter.shown {
			if shown == index {
				m.tree.filter.cursor = position
				return
			}
		}
	}
	t.Fatalf("the tree holds no turn %q", message)
}

// typeTag pushes text into the tag input of the tree, one keystroke at a time.
func typeTag(t *testing.T, m *model, tag string) {
	t.Helper()

	for _, glyph := range tag {
		update(t, m, tea.KeyPressMsg{Code: glyph, Text: string(glyph)})
	}
}

// eraseTag removes the trailing characters of the tag input of the tree, the
// way the keyboard delivers a backspace.
func eraseTag(t *testing.T, m *model, count int) {
	t.Helper()

	for range count {
		update(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
}

// typeName pushes text into the rename input of the command center, one
// keystroke at a time.
func typeName(t *testing.T, m *model, name string) {
	t.Helper()

	for _, glyph := range name {
		update(t, m, tea.KeyPressMsg{Code: glyph, Text: string(glyph)})
	}
}

// eraseName removes the trailing characters of the rename input, the way the
// keyboard delivers a backspace.
func eraseName(t *testing.T, m *model, count int) {
	t.Helper()

	for range count {
		update(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
}

// typeFilter narrows the list the phase shows with a query, one keystroke at a
// time, the way the keyboard delivers it.
func typeFilter(t *testing.T, m *model, query string) {
	t.Helper()

	for _, glyph := range query {
		update(t, m, tea.KeyPressMsg{Code: glyph, Text: string(glyph)})
	}
}

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

	t.Run("cycles through the agents", func(t *testing.T) {
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}, {ID: "writer"}},
			-1,
			func(string) (Session, error) { return newFakeSession(), nil },
		)

		update(t, m, pressUp)
		require.Equal(t, 1, m.picker.cursor, "stepping up from the first agent wraps to the last")

		update(t, m, pressDown)
		require.Equal(t, 0, m.picker.cursor, "stepping down from the last agent wraps to the first")
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
		scripted.context = tokens.Report{Used: 68000, Window: 200000, Percent: 34.0}
		sendEvent(t, m, engine.Event{Type: engine.EventMessageEnd})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		require.False(t, m.running)

		view := plain(m.render())
		require.Contains(t, view, "hello")
		require.Contains(t, view, "hi there")
		require.Contains(t, view, "ctx 34% · 68k/200k")
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

		require.Equal(t, confirmInterrupt, m.confirm.action)
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
		require.Equal(t, confirmInterrupt, m.confirm.action)

		require.Nil(t, update(t, m, pressEscape), "confirming needs no timeout")

		select {
		case <-scripted.canceled:
		case <-time.After(2 * time.Second):
			require.Fail(t, "the run was not canceled")
		}
		require.Equal(t, confirmNone, m.confirm.action)

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
		require.Equal(t, confirmInterrupt, m.confirm.action)

		update(t, m, confirmTimeoutMsg{seq: m.confirm.seq})

		require.Equal(t, confirmNone, m.confirm.action)
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
		update(t, m, confirmTimeoutMsg{seq: m.confirm.seq - 1})

		require.Equal(
			t,
			confirmInterrupt,
			m.confirm.action,
			"a stale timer must not drop the request",
		)
	})

	t.Run("ignores escape when no run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)

		require.Nil(t, update(t, m, pressEscape))
		require.Equal(t, confirmNone, m.confirm.action)
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

	t.Run("scrolls the transcript with the mouse wheel", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 20))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 30 {
			sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "line\n\n"})
		}
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		require.True(t, m.conversation.atBottom())

		update(t, m, wheelUp)

		require.Less(t, m.conversation.offsetRows(), m.conversation.maxOffset())
		before := m.conversation.offsetRows()
		update(t, m, wheelDown)
		require.Greater(t, m.conversation.offsetRows(), before)
	})

	t.Run(
		"scrolls the transcript with the wheel even while a long prompt is written",
		func(t *testing.T) {
			m, _ := chatModel(t)
			update(t, m, windowMsg(80, 20))
			m.input.SetValue("go")
			update(t, m, pressEnter)
			for range 30 {
				sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "line\n\n"})
			}
			sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

			// A multi-line prompt: the wheel must move the conversation and leave
			// the prompt cursor where it is, which is the failure the wheel once
			// had when the terminal reported it as arrow keys.
			m.input.SetValue("first line\nsecond line\nthird line")
			beforeLine := m.input.Line()
			beforeOffset := m.conversation.offsetRows()

			update(t, m, wheelUp)

			require.Less(t, m.conversation.offsetRows(), beforeOffset)
			require.Equal(t, beforeLine, m.input.Line(), "the prompt cursor does not move")
			require.Equal(t, "first line\nsecond line\nthird line", m.input.Value())
		},
	)

	t.Run("leaves the wheel alone outside the conversation", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		for range 30 {
			sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "line\n\n"})
		}
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		before := m.conversation.offsetRows()

		update(t, m, pressCtrlP)
		require.Equal(t, phaseSettings, m.phase)
		update(t, m, wheelUp)
		update(t, m, wheelDown)

		require.Equal(t, before, m.conversation.offsetRows(), "the hidden conversation stays put")
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

	t.Run("moves turn by turn with page up and page down", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventThinkingDelta, Text: "reasoning"})
		sendEvent(
			t,
			m,
			engine.Event{Type: engine.EventToolCall, ToolCallID: "c1", ToolName: "shell"},
		)
		sendEvent(t, m, engine.Event{Type: engine.EventToolResult, ToolCallID: "c1", Text: "ok"})
		sendEvent(t, m, engine.Event{
			Type: engine.EventTextDelta,
			Text: strings.Repeat("answer line\n\n", 6),
		})
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		update(t, m, pressHome)
		require.Zero(t, m.conversation.offsetRows(), "home lands on the prompt, the first turn")

		update(t, m, pressPgDown)
		last := m.conversation.starts[m.conversation.blockCount()-1]
		require.Equal(t, last, m.conversation.offsetRows(), "page down lands on the answer")
		require.NotContains(
			t,
			plain(m.conversation.view()),
			"reasoning",
			"the reasoning is skipped",
		)
		require.NotContains(t, plain(m.conversation.view()), "shell", "the tool is skipped")

		update(t, m, pressPgUp)
		require.Zero(t, m.conversation.offsetRows(), "page up returns to the prompt")
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
		require.Equal(t, "draft", m.input.Value(), "the query of the list stays out of the prompt")

		update(t, m, pressSpace)
		require.Equal(t, "a ", m.commands.query(), "space narrows the list, it does not toggle")

		update(t, m, pressEscape)
		require.Equal(t, phaseSettings, m.phase, "escape clears the query first")

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
		require.Equal(t, phaseStart, m.phase)

		update(t, m, pressCtrlP)
		require.Equal(t, phaseSettings, m.phase)

		update(t, m, pressCtrlP)

		require.Equal(t, phaseStart, m.phase)
	})

	t.Run("toggles the harness options", func(t *testing.T) {
		m, _ := chatModel(t)
		require.False(t, m.preferences.ExpandToolOutput, "the blocks start compact")
		require.False(t, m.preferences.ExpandThinking, "the blocks start compact")
		require.True(t, m.preferences.RenderMarkdown)

		update(t, m, pressCtrlP)
		require.Equal(t, 0, m.commands.cursor, "the commands that open a screen lead the list")

		update(t, m, pressDown)
		update(t, m, pressDown)
		update(t, m, pressDown)
		update(t, m, pressDown)
		update(t, m, pressDown)
		update(t, m, pressDown)
		update(t, m, pressDown)
		require.Equal(t, 7, m.commands.cursor, "the options follow the commands")
		update(t, m, pressEnter)
		require.True(t, m.preferences.ExpandToolOutput)

		update(t, m, pressEnter)
		require.False(t, m.preferences.ExpandToolOutput)

		update(t, m, pressDown)
		update(t, m, pressEnter)
		require.True(t, m.preferences.ExpandThinking)

		update(t, m, pressDown)
		update(t, m, pressEnter)
		require.False(t, m.preferences.RenderMarkdown)

		require.Equal(t, 9, m.commands.cursor)

		update(t, m, pressDown)
		require.Equal(
			t,
			0,
			m.commands.cursor,
			"stepping down from the last command wraps to the first",
		)

		update(t, m, pressUp)
		require.Equal(
			t,
			9,
			m.commands.cursor,
			"stepping up from the first command wraps to the last",
		)
	})

	t.Run("expands the tool output from the command center", func(t *testing.T) {
		m, _ := chatModel(t)
		update(t, m, windowMsg(80, 24))
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
			Text:       "first\nsecond\nthird\nfourth",
		})
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})

		compact := plain(m.render())
		require.Contains(t, compact, "fourth", "the preview shows the trailing lines")
		require.NotContains(t, compact, "first", "the preview drops the earlier lines")

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Expand tool output")
		update(t, m, pressEnter)
		update(t, m, pressCtrlP)

		require.Equal(t, phaseChat, m.phase)

		expanded := plain(m.render())
		require.Contains(t, expanded, "first", "expanding reveals the whole output")
		require.Contains(t, expanded, "fourth")
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

	t.Run("asks for confirmation before quitting", func(t *testing.T) {
		m, _ := chatModel(t)

		cmd := update(t, m, pressCtrlC)

		require.Equal(t, confirmQuit, m.confirm.action)
		require.NotNil(t, cmd, "arming the confirmation starts its timeout")
		require.Contains(t, plain(m.render()), "ctrl+c again to quit")
		require.NotContains(t, plain(m.render()), "ctrl+c quit")
	})

	t.Run("quits on the confirming ctrl+c", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, pressCtrlC)
		cmd := update(t, m, pressCtrlC)

		require.Equal(t, confirmNone, m.confirm.action)
		require.NotNil(t, cmd)
		require.IsType(t, tea.QuitMsg{}, cmd())
	})

	t.Run("drops the quit request when it times out", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, pressCtrlC)
		update(t, m, confirmTimeoutMsg{seq: m.confirm.seq})

		require.Equal(t, confirmNone, m.confirm.action)
		require.Contains(t, plain(m.render()), "ctrl+c quit")
		require.NotContains(t, plain(m.render()), "ctrl+c again to quit")
	})

	t.Run("keeps the request a newer press replaced", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)

		// The escape replaces the quit request with an interruption one, so the
		// timer of the quit request carries an older sequence.
		update(t, m, pressCtrlC)
		update(t, m, pressEscape)
		require.Equal(t, confirmInterrupt, m.confirm.action)

		update(t, m, confirmTimeoutMsg{seq: m.confirm.seq - 1})

		require.Equal(
			t,
			confirmInterrupt,
			m.confirm.action,
			"a stale timer must not drop the request that replaced it",
		)
	})

	t.Run("keeps a pending quit while a run ends", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		update(t, m, pressCtrlC)

		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.Equal(
			t,
			confirmQuit,
			m.confirm.action,
			"the confirmation belongs to the interface, not to the run",
		)
	})

	t.Run("drops a pending interruption when the run ends", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		update(t, m, pressEscape)

		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.Equal(t, confirmNone, m.confirm.action)

		// A run that starts next must not find the confirmation of the run that
		// ended and cancel itself on the first escape.
		m.input.SetValue("again")
		update(t, m, pressEnter)
		update(t, m, pressEscape)

		require.True(t, m.running, "the first escape never cancels a run by itself")
		require.Equal(t, confirmInterrupt, m.confirm.action)
	})

	t.Run("asks for confirmation before quitting with ctrl+d", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, pressCtrlD)
		require.Equal(t, confirmQuit, m.confirm.action)

		require.IsType(t, tea.QuitMsg{}, update(t, m, pressCtrlD)())
	})

	t.Run("names the key that armed the quit request", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, pressCtrlD)
		require.Contains(t, plain(m.render()), "ctrl+d again to quit")

		update(t, m, confirmTimeoutMsg{seq: m.confirm.seq})
		update(t, m, pressCtrlC)

		require.Contains(t, plain(m.render()), "ctrl+c again to quit")
	})

	t.Run("confirms a pending quit with either quit key", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, pressCtrlD)
		require.Equal(t, confirmQuit, m.confirm.action)

		// Both keys ask to leave the interface, so any of them confirms the
		// request the other one armed.
		require.IsType(t, tea.QuitMsg{}, update(t, m, pressCtrlC)())
	})

	t.Run("arms no quit with ctrl+d while a run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)

		require.Nil(t, update(t, m, pressCtrlD))
		require.Equal(t, confirmNone, m.confirm.action)
		require.True(t, m.running)
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
		scripted.entries = []session.Entry{
			{
				Message: llm.Message{
					Role:   llm.RoleUser,
					Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}},
				},
			},
			{
				Message: llm.Message{
					Role:   llm.RoleAssistant,
					Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}},
				},
			},
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
		require.Equal(t, phaseStart, m.phase)
		require.Nil(t, m.Init())

		update(t, m, pressDown)

		cmd := update(t, m, pressEnter)
		require.Equal(t, phasePreparing, m.phase)
		require.NotNil(t, cmd)
		run(t, m, cmd)

		require.Equal(t, phaseChat, m.phase)
		view := plain(m.render())
		require.Contains(t, view, "hello")
		require.Contains(t, view, "hi")
	})

	t.Run("returns from the agent picker to the start list", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		require.Equal(t, phaseStart, m.phase)

		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phasePicker, m.phase)

		update(t, m, pressEscape)

		require.Equal(t, phaseStart, m.phase)
	})

	t.Run("cycles through the start list", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{
				{ID: "session-1", Agent: "coder", Title: "one"},
				{ID: "session-2", Agent: "coder", Title: "two"},
			},
		})
		m.phase = phaseStart

		update(t, m, pressUp)
		require.Equal(t, 2, m.start.cursor, "stepping up from the first entry wraps to the last")

		update(t, m, pressDown)
		require.Equal(t, 0, m.start.cursor, "stepping down from the last entry wraps to the first")
	})

	t.Run("starts a new session from the command center", func(t *testing.T) {
		stored := newFakeSession()
		fresh := newFakeSession()
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
			newSession: func(agentID string) (Session, error) {
				require.Equal(t, "coder", agentID)
				return fresh, nil
			},
			resumeSession: func(string) (Session, error) { return stored, nil },
		})
		update(t, m, windowMsg(80, 24))

		// Continue the stored session and send it a prompt.
		update(t, m, pressDown)
		run(t, m, update(t, m, pressEnter))
		require.Equal(t, phaseChat, m.phase)
		m.input.SetValue("hello")
		update(t, m, pressEnter)
		require.True(t, m.running)

		// The first command of the command center starts a session from
		// scratch, releasing the one in flight.
		update(t, m, pressCtrlP)
		require.Equal(t, "New session", commandList[m.commands.selected()].Label)
		run(t, m, update(t, m, pressEnter))

		require.Equal(t, phaseChat, m.phase)
		require.Same(t, fresh, m.session, "the new session takes over")
		require.True(t, stored.closed, "the session left behind is released")
		require.False(t, m.running, "the run in flight is interrupted")
		require.Empty(t, m.transcript.entries, "the new session starts empty")

		// A burst of the abandoned run can no longer reach the interface.
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "stale output"})
		require.Empty(t, m.transcript.entries, "a late burst is dropped")
	})

	t.Run("reads the sessions again when the list opens", func(t *testing.T) {
		offered := []session.Info{
			{ID: "session-1", Agent: "coder", Title: "hello"},
		}
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: offered,
			scanSessions: func() []session.Info {
				return offered
			},
		})
		require.Equal(t, 2, len(m.starts), "the list opens with the sessions known at startup")

		// A session created while the interface runs shows up when the list
		// opens again, most recently updated first.
		offered = []session.Info{
			{ID: "session-2", Agent: "coder", Title: "second"},
			{ID: "session-1", Agent: "coder", Title: "hello"},
		}
		update(t, m, windowMsg(80, 24))
		update(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
		update(t, m, pressDown)
		run(t, m, update(t, m, pressEnter))

		require.Equal(t, phaseStart, m.phase)
		require.Equal(t, 3, len(m.starts))
		require.Equal(t, "session-2", m.starts[1].info.ID)

		view := plain(m.render())
		require.Contains(t, view, "second")
		require.Contains(t, view, "hello")
	})

	t.Run("keeps the highlight when the list is read again", func(t *testing.T) {
		offered := []session.Info{
			{ID: "session-1", Agent: "coder", Title: "hello"},
			{ID: "session-2", Agent: "coder", Title: "bye"},
		}
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: offered,
			scanSessions: func() []session.Info {
				return offered
			},
		})

		// The list opens whole again, on the offer of a new session.
		cmd := m.openStart()
		require.Zero(t, m.start.cursor)

		// The user moves the highlight while the read of the sessions is in
		// flight, and a session created elsewhere leads the list by the time
		// it lands, pushing the highlighted entry one position down.
		m.start.cursor = 1
		offered = []session.Info{
			{ID: "session-3", Agent: "coder", Title: "new"},
			offered[0],
			offered[1],
		}
		run(t, m, cmd)

		require.Equal(t, "session-1", m.starts[m.start.selected()].info.ID)
		require.Equal(t, 2, m.start.cursor, "the highlight follows the entry it held")
	})

	t.Run("reads the sessions again when the picker leads back to the list", func(t *testing.T) {
		offered := []session.Info{{ID: "session-1", Agent: "coder", Title: "hello"}}
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
			sessions: offered,
			scanSessions: func() []session.Info {
				return offered
			},
		})
		update(t, m, windowMsg(80, 24))
		require.Equal(t, phaseStart, m.phase)
		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phasePicker, m.phase)

		offered = append(offered, session.Info{ID: "session-9", Agent: "coder", Title: "later"})

		run(t, m, update(t, m, pressEscape))

		require.Equal(t, phaseStart, m.phase)
		require.Contains(t, plain(m.render()), "later")
	})

	t.Run("reopens the session list from the command center", func(t *testing.T) {
		m, _ := chatModel(t)
		require.Equal(t, phaseChat, m.phase)

		update(t, m, pressCtrlP)
		update(t, m, pressDown)
		require.Equal(t, "Sessions", commandList[m.commands.selected()].Label)

		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phaseStart, m.phase)
		require.Contains(t, plain(m.render()), "Start a new session or continue a previous one")

		update(t, m, pressEscape)

		require.Equal(t, phaseChat, m.phase, "escape returns to the conversation")
		require.Contains(t, plain(m.render()), "coder", "the conversation shows again")
	})

	t.Run("returns from the agent picker to the conversation it opened over", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:        []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected:      -1,
			sessions:      []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
			resumeSession: func(string) (Session, error) { return newFakeSession(), nil },
		})
		update(t, m, windowMsg(80, 24))
		update(t, m, pressDown)
		run(t, m, update(t, m, pressEnter))
		require.Equal(t, phaseChat, m.phase)

		// The picker of a new session, opened over the conversation, leads
		// back to it when it is dismissed.
		update(t, m, pressCtrlP)
		update(t, m, pressEnter)
		require.Equal(t, phasePicker, m.phase)

		update(t, m, pressEscape)

		require.Equal(t, phaseChat, m.phase)
	})

	t.Run("starts a new session from the menu", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		require.Equal(t, phaseStart, m.phase)

		require.Nil(t, update(t, m, pressEnter))

		require.Equal(t, phasePicker, m.phase)
	})

	t.Run("narrows the start list and the picker by typing", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}, {ID: "writer"}},
			selected: -1,
			sessions: []session.Info{
				{ID: "session-1", Agent: "coder", Title: "refactor parser"},
				{ID: "session-2", Agent: "coder", Title: "write docs"},
			},
		})

		update(t, m, tea.KeyPressMsg{Code: 'd', Text: "docs"})

		require.Equal(t, []int{2}, m.start.shown, "only the matching session stays")
		require.Equal(t, 2, m.start.selected())

		// The offer of a new session leads the list and is matched like any
		// other entry.
		require.True(t, m.start.clear())
		update(t, m, tea.KeyPressMsg{Code: 'n', Text: "new"})
		require.Equal(t, 0, m.start.selected())

		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phasePicker, m.phase)

		update(t, m, tea.KeyPressMsg{Code: 'w', Text: "writ"})

		require.Equal(t, []int{1}, m.picker.shown)
		require.Equal(t, 1, m.picker.selected())
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

// TestTree verifies the screen that walks the session tree.
func TestTree(t *testing.T) {
	t.Run("opens on the turn the session is at", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)

		require.Equal(t, phaseTree, m.phase)
		require.Empty(t, m.tree.filter.query())
		require.Len(t, m.tree.nodes, 2)
		require.Equal(t, 1, m.tree.filter.cursor, "the highlight lands where the session stands")
		require.Equal(t, stored.store.Leaf(), m.tree.nodes[1].entry.ID)
		require.Contains(t, plain(m.render()), "You: hello")
		require.Contains(t, plain(m.render()), "Agent (coder): hi")
	})

	t.Run("opens from the command center", func(t *testing.T) {
		m, _ := storeChat(t, textMessage(llm.RoleUser, "hello"))

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Tree")
		update(t, m, pressEnter)

		require.Equal(t, phaseTree, m.phase)
	})

	t.Run("stays away while the agent works", func(t *testing.T) {
		m, _ := storeChat(t, textMessage(llm.RoleUser, "hello"))
		m.running = true

		require.Nil(t, update(t, m, pressCtrlT))
		require.Equal(t, phaseChat, m.phase)

		// The command center offers the command without running it.
		update(t, m, pressCtrlP)
		typeFilter(t, m, "Tree")
		require.Nil(t, update(t, m, pressEnter))
		require.Equal(t, phaseSettings, m.phase)
	})

	t.Run("stays away without a session", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})
		require.Equal(t, phaseStart, m.phase)

		require.Nil(t, update(t, m, pressCtrlT))

		require.Equal(t, phaseStart, m.phase)
	})

	t.Run("clears the query before leaving", func(t *testing.T) {
		m, _ := treeModel(t, textMessage(llm.RoleUser, "hello"))

		typeFilter(t, m, "hello")
		update(t, m, pressEscape)
		require.Equal(t, phaseTree, m.phase, "escape clears the query first")
		require.Empty(t, m.tree.filter.query())

		update(t, m, pressEscape)
		require.Equal(t, phaseChat, m.phase)
	})

	t.Run("returns to the answer it highlights", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		answer := stored.store.Entries()[1]

		update(t, m, pressUp)
		update(t, m, pressUp)
		require.Equal(t, 1, m.tree.filter.cursor, "the highlight walks the tree one turn at a time")
		update(t, m, pressEnter)

		require.Equal(t, phaseChat, m.phase)
		require.Equal(t, answer.ID, stored.store.Leaf())
		require.Empty(t, m.input.Value(), "an answer brings no message of its own")
		require.True(t, m.fork, "writing there opens a branch beside the turns that follow")

		view := plain(m.render())
		require.Contains(t, view, "first")
		require.Contains(t, view, "one")
		require.NotContains(t, view, "second", "the conversation keeps the branch it returned to")
		require.Contains(t, view, "starts a new branch")
	})

	t.Run("continues the branch when the answer closes it", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)

		update(t, m, pressEnter)

		require.Equal(t, phaseChat, m.phase)
		require.Equal(t, stored.store.Entries()[1].ID, stored.store.Leaf())
		require.False(t, m.fork, "the last turn of a branch is where writing continues")
		require.Contains(t, plain(m.render()), "the conversation continues from here")
	})

	t.Run("offers the prompt it returns to", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)

		update(t, m, pressUp)
		update(t, m, pressEnter)

		require.Equal(t, phaseChat, m.phase)
		require.Empty(t, stored.store.Leaf(), "the session returns to the turn before the prompt")
		require.Equal(t, "first", m.input.Value(), "the prompt comes back for the user to edit")
		require.True(t, m.fork)
	})

	t.Run("drops the notice once the message is sent", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		update(t, m, pressUp)
		update(t, m, pressUp)
		update(t, m, pressEnter)
		require.True(t, m.fork)

		m.input.SetValue("again")
		update(t, m, pressEnter)

		require.False(t, m.fork, "the message wrote the branch the notice announced")
		require.NotContains(t, plain(m.render()), "starts a new branch")
	})

	t.Run("labels a turn and finds it again", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "fix the parser"),
			textMessage(llm.RoleAssistant, "done"),
		)

		update(t, m, pressUp)
		update(t, m, pressCtrlT)
		require.True(t, m.tree.editing)
		require.Contains(t, plain(m.render()), "enter save")

		typeTag(t, m, "bug")
		update(t, m, pressEnter)

		require.False(t, m.tree.editing)
		require.Equal(t, "bug", stored.store.Entries()[0].Tag)
		require.Contains(t, plain(m.render()), "#bug")

		typeFilter(t, m, "bug")
		require.Equal(t, 0, m.tree.filter.selected(), "the query finds the turn by its tag")
	})

	t.Run("drops the sharp of a tag written as shown", func(t *testing.T) {
		m, stored := treeModel(t, textMessage(llm.RoleUser, "fix the parser"))

		update(t, m, pressCtrlT)
		typeTag(t, m, "#bug")
		update(t, m, pressEnter)

		require.Equal(t, "bug", stored.store.Entries()[0].Tag)
	})

	t.Run("removes the tag of a turn", func(t *testing.T) {
		m, stored := treeModel(t, textMessage(llm.RoleUser, "fix the parser"))
		turn := stored.store.Entries()[0]
		require.NoError(t, stored.store.SetTag(turn.ID, "bug"))
		m.buildTree()

		update(t, m, pressCtrlT)
		require.Equal(t, "bug", m.tree.tag.Value(), "the input offers the tag the turn carries")
		eraseTag(t, m, len("bug"))
		update(t, m, pressEnter)

		require.False(t, m.tree.editing)
		require.Empty(t, stored.store.Entries()[0].Tag)
		require.NotContains(t, plain(m.render()), "#bug")
	})

	t.Run("leaves the tag as it was on escape", func(t *testing.T) {
		m, stored := treeModel(t, textMessage(llm.RoleUser, "fix the parser"))

		update(t, m, pressCtrlT)
		typeTag(t, m, "bug")
		update(t, m, pressEscape)

		require.False(t, m.tree.editing)
		require.Empty(t, stored.store.Entries()[0].Tag)
		require.Empty(t, m.tree.tag.Value())

		typeFilter(t, m, "fix")
		require.Equal(t, 0, m.tree.filter.selected(), "the keys reach the query again")
	})

	t.Run("folds and unfolds the turns that follow the highlighted turn", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		update(t, m, pressUp)
		update(t, m, pressUp)
		require.Equal(t, "one", m.tree.nodes[m.tree.filter.selected()].text)

		update(t, m, pressCtrlF)

		require.Len(t, m.tree.nodes, 2, "folding hides the turns that follow the turn")
		require.Contains(t, plain(m.render()), "⊟─", "the folded turn says so")

		update(t, m, pressCtrlF)

		require.Len(t, m.tree.nodes, 4)
		require.Equal(t, "one", m.tree.nodes[m.tree.filter.selected()].text)
	})

	t.Run("keeps the highlight on the turn that folds a subtree", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		update(t, m, pressUp)
		update(t, m, pressUp)
		require.Equal(t, "one", m.tree.nodes[m.tree.filter.selected()].text)

		update(t, m, pressCtrlF)

		require.Equal(
			t,
			"one",
			m.tree.nodes[m.tree.filter.selected()].text,
			"the highlight stays on the turn it folded instead of jumping to the first one",
		)
	})

	t.Run("folds the whole tree and unfolds it again with one key", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)

		update(t, m, pressCtrlA)

		require.Len(
			t,
			m.tree.nodes,
			1,
			"the outline keeps only the turn that opens the conversation",
		)
		require.Contains(t, plain(m.render()), "⊟─")

		update(t, m, pressCtrlA)

		require.Len(t, m.tree.nodes, 4)
		require.NotContains(t, plain(m.render()), "⊟─")
	})

	t.Run("folds every subtree except the branch the session runs", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		// The session returns to the first answer and writes again from it, so
		// the tree holds a branch beside the one the session runs.
		update(t, m, pressUp)
		update(t, m, pressUp)
		update(t, m, pressEnter)
		m.input.SetValue("other")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		update(t, m, pressCtrlT)
		require.Equal(
			t,
			[]string{"first", "one", "second", "two", "other"},
			nodesField(m.tree.nodes, func(node treeNode) string { return node.text }),
			"the branch the session left stays in the tree beside the one it runs",
		)

		update(t, m, pressCtrlO)

		require.Equal(
			t,
			[]string{"first", "one", "second", "other"},
			nodesField(m.tree.nodes, func(node treeNode) string { return node.text }),
			"the branch the session runs stays whole and the branch it left folds",
		)
		require.True(
			t,
			m.tree.folded[stored.store.Entries()[2].ID],
			"the turn the abandoned branch hangs from folds, so its turns hide",
		)
		require.NotContains(
			t,
			nodesField(m.tree.nodes, func(node treeNode) string { return node.text }),
			"two",
			"the turns under the abandoned branch hide",
		)

		update(t, m, pressCtrlO)

		require.Equal(
			t,
			[]string{"first", "one", "second", "other"},
			nodesField(m.tree.nodes, func(node treeNode) string { return node.text }),
			"folding the others again keeps the tree as it is",
		)
	})

	t.Run("ignores folding a turn that holds nothing", func(t *testing.T) {
		m, _ := treeModel(t, textMessage(llm.RoleUser, "first"))

		update(t, m, pressCtrlF)

		require.Len(t, m.tree.nodes, 1)
		require.Equal(t, "first", m.tree.nodes[m.tree.filter.selected()].text)
	})

	t.Run("opens a branch from the turn it selected", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		update(t, m, pressCtrlT)
		pickTurn(t, m, "one")
		update(t, m, pressEnter)
		m.input.SetValue("other")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		// The session returned to the first answer, so the turn that followed
		// it stays in the tree and the new message hangs from that answer.
		update(t, m, pressCtrlT)
		pickTurn(t, m, "two")
		update(t, m, pressEnter)
		require.Equal(t, stored.store.Entries()[3].ID, stored.store.Leaf())
		m.input.SetValue("third")
		update(t, m, pressEnter)

		entries := stored.store.Entries()
		require.Equal(
			t,
			stored.store.Entries()[3].ID,
			entries[len(entries)-1].ParentID,
			"the message follows the turn the tree selected, not the branch it stood on",
		)
	})

	t.Run("draws a branch under the turn it selected", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
			textMessage(llm.RoleUser, "second"),
			textMessage(llm.RoleAssistant, "two"),
		)
		pickTurn(t, m, "one")
		update(t, m, pressEnter)
		m.input.SetValue("other")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})
		update(t, m, pressCtrlT)

		view := plain(m.render())
		require.Contains(t, view, "├─ You: second")
		require.Contains(
			t,
			view,
			"│  Agent (coder): two",
			"the continuation keeps the column of its branch",
		)
		require.Contains(t, view, "└─ You: other", "the branch closes the group of the answer")
		require.Less(
			t,
			strings.Index(view, "two"),
			strings.Index(view, "other"),
			"the turns of the subtree stay together under the turn they follow",
		)
	})

	t.Run("reports every rewind above the prompt", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)
		update(t, m, pressUp)
		update(t, m, pressEnter)

		view := plain(m.render())
		require.Contains(t, view, "rewound")
		require.Contains(t, view, "starts a new branch", "the prompt opens a branch beside itself")
	})

	t.Run("reports a rewind that continues the branch", func(t *testing.T) {
		m, _ := treeModel(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)
		update(t, m, pressEnter)

		view := plain(m.render())
		require.Contains(t, view, "rewound")
		require.Contains(t, view, "the conversation continues from here")
	})

	t.Run("reports the failures of the session", func(t *testing.T) {
		scripted := newFakeSession()
		scripted.entries = []session.Entry{{
			ID:      "m1",
			Message: textMessage(llm.RoleUser, "hello"),
		}}
		scripted.leafErr = errors.New("boom")
		m := newTestModelWith(t, modelConfig{
			agents:     []agent.Agent{{ID: "coder"}},
			selected:   0,
			newSession: func(string) (Session, error) { return scripted, nil },
		})
		update(t, m, windowMsg(80, 24))
		run(t, m, m.Init())
		update(t, m, pressCtrlT)

		require.Nil(t, update(t, m, pressEnter))

		require.Equal(t, phaseTree, m.phase, "the tree reports the failure instead of leaving")
		require.Contains(t, plain(m.render()), "error: boom")
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
		m.preferences = preferences{}
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventThinkingDelta, Text: "hmm"})
		sendEvent(
			t,
			m,
			engine.Event{Type: engine.EventToolCall, ToolCallID: "c1", ToolName: "shell"},
		)

		for _, block := range []string{
			ansi.Strip(m.renderThinkingEntry(&m.transcript.entries[1], 40)),
			ansi.Strip(m.renderToolEntry(&m.transcript.entries[2], 40)),
		} {
			require.NotContains(t, block, "▀▄▀", "the blocks carry no spinner")
			require.NotContains(t, block, "■■■", "the blocks carry no spinner")
			require.NotContains(t, block, "▄▀▄", "the blocks carry no spinner")
		}
		require.Contains(t, ansi.Strip(m.renderThinkingEntry(&m.transcript.entries[1], 40)),
			markerActivity+" Agent: thinking")
		require.Contains(t, ansi.Strip(m.renderToolEntry(&m.transcript.entries[2], 40)),
			markerActivity+" Tool: shell")
	})
}

// TestRunElapsed verifies the time the agent took to answer a turn: the timer
// starts with the prompt, follows the run while it is in flight and closes the
// turn with the time it took once the run ends.
func TestRunElapsed(t *testing.T) {
	t.Run("starts the timer when the prompt is sent", func(t *testing.T) {
		m, _ := chatModel(t)
		require.True(t, m.runStart.IsZero(), "no run is in flight before the prompt")

		m.input.SetValue("go")
		update(t, m, pressEnter)

		require.False(t, m.runStart.IsZero(), "the timer starts with the run")
	})

	t.Run("shows the time in flight beside the status line", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		m.runStart = time.Now().Add(-2 * time.Minute)

		require.Contains(t, plain(m.activityLine()), formatElapsed(2*time.Minute))
	})

	t.Run("closes the turn with the time it took", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("go")
		update(t, m, pressEnter)
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "answer"})

		m.runStart = time.Now().Add(-90 * time.Second)
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.True(t, m.runStart.IsZero(), "the timer stops with the run")
		last := m.transcript.entries[len(m.transcript.entries)-1]
		require.Equal(t, entryAssistant, last.kind, "the time closes the answer")
		require.GreaterOrEqual(t, last.elapsed, 90*time.Second)
	})

	t.Run("does nothing while no run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)

		m.recordElapsed()

		require.Empty(t, m.transcript.entries)
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
		require.Equal(t, phaseStart, startPhase(modelConfig{
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

// TestMoveCursor verifies the cyclic movement shared by every list of the
// interface.
func TestMoveCursor(t *testing.T) {
	t.Run("moves within the list without wrapping", func(t *testing.T) {
		require.Equal(t, 2, moveCursor(1, 1, 4))
		require.Equal(t, 0, moveCursor(1, -1, 4))
	})

	t.Run("wraps around at both ends", func(t *testing.T) {
		require.Equal(t, 0, moveCursor(3, 1, 4), "the last item steps down to the first")
		require.Equal(t, 3, moveCursor(0, -1, 4), "the first item steps up to the last")
	})

	t.Run("handles single item lists", func(t *testing.T) {
		require.Equal(t, 0, moveCursor(0, 1, 1))
		require.Equal(t, 0, moveCursor(0, -1, 1))
	})

	t.Run("keeps the cursor on the first index of empty lists", func(t *testing.T) {
		require.Equal(t, 0, moveCursor(0, 1, 0))
		require.Equal(t, 0, moveCursor(0, -1, 0))
	})

	t.Run("ignores movements that cover the whole list", func(t *testing.T) {
		require.Equal(t, 1, moveCursor(1, 5, 5))
		require.Equal(t, 3, moveCursor(3, -5, 5))
	})

	t.Run("returns the first index when the cursor is stale", func(t *testing.T) {
		require.Equal(t, 1, moveCursor(7, 0, 3))
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

// TestRefreshContext verifies the context figure the footer shows.
func TestRefreshContext(t *testing.T) {
	t.Run("reports the measurement of the branch", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.context = tokens.Report{Used: 68000, Window: 200000, Percent: 34.0}

		m.refreshContext()

		require.Equal(t, 34.0, m.context.Percent)
		require.Contains(t, plain(m.render()), "ctx 34% · 68k/200k")
	})

	t.Run("follows the branch the interface shows", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.context = tokens.Report{Used: 100, Window: 1000, Percent: 10.0}
		m.refreshContext()
		require.Equal(t, 10.0, m.context.Percent)

		// A run that ends changes the figure: the session answers a different
		// measurement and the interface picks it up.
		scripted.context = tokens.Report{Used: 900, Window: 1000, Percent: 90.0}
		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))
		sendEvent(t, m, engine.Event{Type: engine.EventRunEnd, Reason: engine.EndReasonTurn})

		require.Equal(t, 90.0, m.context.Percent)
	})

	t.Run("keeps the previous figure when the measurement fails", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.context = tokens.Report{Used: 100, Window: 1000, Percent: 10.0}
		m.refreshContext()

		scripted.contextErr = errors.New("boom")
		m.refreshContext()

		require.Equal(t, 10.0, m.context.Percent)
		require.Contains(t, plain(m.render()), "ctx 10%")
	})

	t.Run("follows a run in flight through the context events", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))
		m.context = tokens.Report{}

		// The engine measures the branch after every change to the
		// conversation and reports it, so the footer follows the run as it grows
		// instead of waiting for it to end.
		sendEvent(t, m, engine.Event{
			Type:    engine.EventContext,
			Context: &engine.ContextInfo{Used: 30000, Window: 100000, Percent: 30},
		})
		require.Contains(t, plain(m.render()), "ctx 30% · 30k/100k")

		sendEvent(t, m, engine.Event{
			Type:    engine.EventContext,
			Context: &engine.ContextInfo{Used: 55000, Window: 100000, Percent: 55},
		})
		require.Contains(t, plain(m.render()), "ctx 55% · 55k/100k")
	})

	t.Run("ignores a context event that carries no measurement", func(t *testing.T) {
		m, _ := chatModel(t)
		m.context = tokens.Report{Used: 100, Window: 1000, Percent: 10.0}

		sendEvent(t, m, engine.Event{Type: engine.EventContext})

		require.Equal(t, 10.0, m.context.Percent, "the previous figure stands")
	})

	t.Run("reports nothing without a session", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, 0, nil)

		m.refreshContext()

		require.Zero(t, m.context)
	})

	t.Run("shows a different figure for two branches of one session", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "one"),
			textMessage(llm.RoleAssistant, "two"),
			textMessage(llm.RoleUser, "three"),
			textMessage(llm.RoleAssistant, "four"),
		)
		require.Equal(t, 40.0, m.context.Percent, "the whole branch is measured")

		// Returning to an earlier turn shortens the branch the session runs,
		// so the figure of the same session changes with it.
		require.NoError(t, stored.store.SetLeaf(stored.store.Branch()[1].ID))
		m.refreshContext()

		require.Equal(t, 20.0, m.context.Percent)
	})
}

// TestCompactContext verifies the manual compaction command.
func TestCompactContext(t *testing.T) {
	t.Run("lists the command and disables it with nothing to compact", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.refused = true
		scripted.refusal = compaction.Refusal{Kind: compaction.RefusalShort, Needed: 20000}

		var listed bool
		for _, entry := range commandList {
			if entry.Label == "Compact context" {
				listed = true
				require.False(t, entry.Enabled(m), "nothing to compact yet")
			}
		}
		require.True(t, listed, "the command center offers the command")

		update(t, m, pressCtrlP)
		require.Contains(t, plain(m.render()), "Compact context")
		require.Contains(t, plain(m.render()), "needs 20k more tokens of history")
	})

	t.Run("enables the command when the branch holds something to compact", func(t *testing.T) {
		m, _ := chatModel(t)

		require.True(t, m.compactReady())

		update(t, m, pressCtrlP)
		require.Contains(t, plain(m.render()), "Compact context")
	})

	t.Run("disables the command while a run is in flight", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))

		require.False(t, m.compactReady())
	})

	t.Run("compacts a session the threshold would leave alone", func(t *testing.T) {
		m, scripted := chatModel(t)

		cmd := m.compactContext()

		require.NotNil(t, cmd)
		require.Equal(t, 1, scripted.compacted)
		require.True(t, m.running)
		require.Equal(t, activityWorking, m.activity)
	})

	t.Run("refuses to compact with nothing to summarize", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.refused = true

		require.Nil(t, m.compactContext())
		require.Zero(t, scripted.compacted)
	})
}

// TestLiveCompaction verifies the interface while a compaction is in flight.
func TestLiveCompaction(t *testing.T) {
	t.Run("reloads the conversation when the checkpoint is written", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		m.input.SetValue("go")
		require.NotNil(t, update(t, m, pressEnter))

		sendEvent(t, m, engine.Event{Type: engine.EventCompactionStart})
		require.Equal(t, activityCompacting, m.activity)

		first := stored.store.Branch()[0]
		_, err := stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			first.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)
		sendEvent(t, m, engine.Event{Type: engine.EventCompactionEnd})

		require.Equal(t, activityWorking, m.activity)

		view := plain(m.render())
		require.Contains(t, view, "Compaction")
		require.Contains(t, view, compactionBody)
	})
}

// TestCompactNote verifies the explanation of a disabled compaction command.
func TestCompactNote(t *testing.T) {
	t.Run("explains every reason the command cannot run", func(t *testing.T) {
		tests := []struct {
			name    string
			prepare func(*model, *fakeSession)
			want    string
		}{
			{
				name: "nothing to summarize with no tokens lacking",
				prepare: func(_ *model, scripted *fakeSession) {
					scripted.refused = true
					scripted.refusal = compaction.Refusal{Kind: compaction.RefusalShort}
				},
				want: "there is not enough history to summarize yet",
			},
			{
				name: "nothing to summarize with tokens lacking",
				prepare: func(_ *model, scripted *fakeSession) {
					scripted.refused = true
					scripted.refusal = compaction.Refusal{
						Kind:   compaction.RefusalShort,
						Needed: 20000,
					}
				},
				want: "needs 20k more tokens of history",
			},
			{
				name: "the conversation already ends in a summary",
				prepare: func(_ *model, scripted *fakeSession) {
					scripted.refused = true
					scripted.refusal = compaction.Refusal{Kind: compaction.RefusalCompacted}
				},
				want: "the conversation already ends in a summary",
			},
			{
				name: "the branch holds no turn to summarize",
				prepare: func(_ *model, scripted *fakeSession) {
					scripted.refused = true
					scripted.refusal = compaction.Refusal{Kind: compaction.RefusalNoTurn}
				},
				want: "the conversation holds no turn to summarize",
			},
			{
				name: "the session holds no conversation",
				prepare: func(_ *model, scripted *fakeSession) {
					scripted.refused = true
					scripted.refusal = compaction.Refusal{Kind: compaction.RefusalEmpty}
				},
				want: "the session holds no conversation yet",
			},
			{
				name: "a run is in flight",
				prepare: func(m *model, _ *fakeSession) {
					m.input.SetValue("hello")
					require.NotNil(t, update(t, m, pressEnter))
				},
				want: "a run is in flight",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				m, scripted := chatModel(t)
				test.prepare(m, scripted)

				require.Equal(t, test.want, m.compactNote())

				update(t, m, pressCtrlP)
				require.Contains(
					t,
					plain(m.render()),
					"Compact context  "+test.want,
					"the note reaches the command center",
				)
			})
		}
	})

	t.Run("explains an open session with no reason", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, 0, nil)
		update(t, m, windowMsg(80, 24))

		require.Equal(t, "open a session first", m.compactNote())
	})

	t.Run("reports nothing while the command can run", func(t *testing.T) {
		m, _ := chatModel(t)

		require.True(t, m.compactReady())
		require.Empty(t, m.compactNote())

		update(t, m, pressCtrlP)
		require.Contains(
			t,
			plain(m.render()),
			"summarize the oldest turns into a checkpoint",
		)
	})
}

// TestRenameSession verifies naming the session from the command center, which
// is what lets the user find it again in the list of stored sessions.
func TestRenameSession(t *testing.T) {
	t.Run("names the session and offers the name it carries", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.info.Title = "named"
		scripted.info.Named = true

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)

		require.True(t, m.renaming)
		require.Equal(t, "named", m.rename.Value(), "the input opens with the name it carries")
		require.Contains(t, plain(m.render()), "name: named")
		require.Contains(t, plain(m.render()), "type a name · enter save · esc cancel")

		eraseName(t, m, len("named"))
		typeName(t, m, "Fix the parser")
		update(t, m, pressEnter)

		require.False(t, m.renaming, "enter leaves the input")
		require.Equal(t, []string{"Fix the parser"}, scripted.titles)
		require.Equal(t, "Fix the parser", m.session.Info().Title)
	})

	t.Run("opens an empty input for a session the user never named", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.info.Title = "hello"

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)

		require.Empty(t, m.rename.Value(), "the derived title is not offered as a name")
	})

	t.Run("removes the name with an empty input", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.info.Title = "named"
		scripted.info.Named = true

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		eraseName(t, m, len("named"))
		update(t, m, pressEnter)

		require.Equal(t, []string{""}, scripted.titles)
		require.Empty(t, m.session.Info().Title)
	})

	t.Run("leaves the name as it was on escape", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.info.Title = "named"
		scripted.info.Named = true

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		typeName(t, m, " other")
		update(t, m, pressEscape)

		require.False(t, m.renaming)
		require.Empty(t, scripted.titles, "escape stores nothing")
		require.Equal(t, "named", m.session.Info().Title)
		require.Contains(t, plain(m.render()), "Command center", "escape returns to the commands")
	})

	t.Run("reports the failure of the session", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.titleErr = errors.New("boom")

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		typeName(t, m, "named")
		update(t, m, pressEnter)

		require.True(t, m.renaming, "the input stays open so the name is not lost")
		require.Contains(t, plain(m.render()), "error: boom")
		require.Contains(t, plain(m.render()), "esc cancel")
	})

	t.Run("drops the rename when the command center closes", func(t *testing.T) {
		m, _ := chatModel(t)

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		require.True(t, m.renaming)

		update(t, m, pressEscape)
		require.False(t, m.renaming)

		update(t, m, pressCtrlP)
		require.False(t, m.renaming, "the command center opens clean")
		require.NotContains(t, plain(m.render()), "type a name")
	})

	t.Run("drops the rename when another screen opens", func(t *testing.T) {
		m, _ := storeChat(t, textMessage(llm.RoleUser, "hello"))

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		require.True(t, m.renaming)

		update(t, m, pressCtrlT)
		require.Equal(t, phaseTree, m.phase)
		require.False(t, m.renaming, "the tree opens clean")

		update(t, m, pressEscape)
		update(t, m, pressCtrlP)
		require.NotContains(t, plain(m.render()), "type a name")
	})

	t.Run("stays away while the agent works", func(t *testing.T) {
		m, scripted := chatModel(t)
		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))

		require.False(t, m.renameReady())
		require.Equal(t, "a run is in flight", m.renameNote())

		update(t, m, pressCtrlP)
		require.Contains(t, plain(m.render()), "Rename session  a run is in flight")

		update(t, m, pressEnter)
		require.False(t, m.renaming, "the command cannot open the input")
		require.Empty(t, scripted.titles)
	})

	t.Run("stays away without a session", func(t *testing.T) {
		m := newTestModel(t, []agent.Agent{{ID: "coder"}}, 0, nil)
		update(t, m, windowMsg(80, 24))

		require.False(t, m.renameReady())
		require.Equal(t, "open a session first", m.renameNote())
	})

	t.Run("names the session of the store and lists it under its name", func(t *testing.T) {
		m, stored := storeChat(t, textMessage(llm.RoleUser, "hello"))

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		typeName(t, m, "Fix the parser")
		update(t, m, pressEnter)

		infos, err := session.List(stored.dir)
		require.NoError(t, err)
		require.Len(t, infos, 1)
		require.Equal(t, "Fix the parser", infos[0].Title)
		require.True(t, infos[0].Named)
	})

	t.Run("shows the name in the identity of the session", func(t *testing.T) {
		m, _ := chatModel(t)
		// The identity gathers the agent, the model, the identifier and the
		// name, so the terminal must be wide enough for the name to survive
		// the clipping of the line.
		update(t, m, windowMsg(100, 24))
		require.NotContains(t, plain(m.render()), "Fix the parser")

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		typeName(t, m, "Fix the parser")
		update(t, m, pressEnter)
		// The first escape clears the query of the command center and the
		// second one returns to the conversation, which is where the header
		// shows the name.
		update(t, m, pressEscape)
		update(t, m, pressEscape)

		require.Equal(t, phaseChat, m.phase)
		require.Contains(t, plain(m.render()), "session-1 · Fix the parser")
	})

	t.Run("names the session of the store from an empty input", func(t *testing.T) {
		m, stored := storeChat(t, textMessage(llm.RoleUser, "hello"))

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Rename session")
		update(t, m, pressEnter)
		require.Empty(t, m.rename.Value())

		typeName(t, m, "Fix the parser")
		update(t, m, pressEnter)

		infos, err := session.List(stored.dir)
		require.NoError(t, err)
		require.Equal(t, "Fix the parser", infos[0].Title)
	})

	t.Run("hides the derived title from the identity of the session", func(t *testing.T) {
		m, scripted := chatModel(t)
		scripted.info.Title = "hello"
		scripted.info.Named = false

		require.NotContains(
			t,
			plain(m.render()),
			"hello",
			"a session the user did not name shows no name",
		)
	})
}

// TestSwitchAgent verifies the agent selection of an open conversation: the
// leader chord, the picker it opens and the command center entry that reaches
// the same place.
func TestSwitchAgent(t *testing.T) {
	t.Run("opens the picker with the leader chord", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)

		require.Nil(t, update(t, m, pressCtrlX), "the leader only arms the chord")
		require.Equal(t, phaseChat, m.phase)

		update(t, m, pressA)

		require.Equal(t, phasePicker, m.phase)
		require.True(t, m.pickerMode != pickerNewAgent)
		require.Contains(t, plain(m.render()), "Switch the agent of the conversation")
		require.Contains(t, plain(m.render()), "current", "the running agent is marked")
		require.Empty(t, stored.store.Branch()[0].AgentID, "nothing is selected yet")
	})

	t.Run("opens the picker from the command center", func(t *testing.T) {
		m, _ := storeChat(t, textMessage(llm.RoleUser, "first"))

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Switch agent")
		update(t, m, pressEnter)

		require.Equal(t, phasePicker, m.phase)
		require.True(t, m.pickerMode != pickerNewAgent)
	})

	t.Run("selects the agent the conversation runs onward", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)
		m.agents = append(m.agents, agent.Agent{ID: "reviewer", Description: "Another agent"})

		update(t, m, pressCtrlX)
		update(t, m, pressA)
		typeFilter(t, m, "reviewer")
		update(t, m, pressEnter)

		require.Equal(t, phaseChat, m.phase)
		require.False(t, m.pickerMode != pickerNewAgent)
		require.Equal(t, "reviewer", stored.store.ActiveAgent())
		require.Contains(t, plain(m.render()), "reviewer", "the identity follows the agent")
	})

	t.Run("returns to the conversation on escape", func(t *testing.T) {
		m, stored := storeChat(t, textMessage(llm.RoleUser, "first"))

		update(t, m, pressCtrlX)
		update(t, m, pressA)
		update(t, m, pressEscape)

		require.Equal(t, phaseChat, m.phase)
		require.False(t, m.pickerMode != pickerNewAgent)
		require.Equal(t, "coder", stored.store.ActiveAgent(), "escape selects nothing")
	})

	t.Run("clears the query before leaving the picker", func(t *testing.T) {
		m, _ := storeChat(t, textMessage(llm.RoleUser, "first"))

		update(t, m, pressCtrlX)
		update(t, m, pressA)
		typeFilter(t, m, "coder")
		update(t, m, pressEscape)

		require.Equal(t, phasePicker, m.phase, "escape clears the query first")
		update(t, m, pressEscape)
		require.Equal(t, phaseChat, m.phase)
	})

	t.Run("stays away while the agent works", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))

		// The leader chord is spent on its second key, so the run guard is
		// reached from a conversation that is not waiting for a chord.
		require.Nil(t, update(t, m, pressCtrlX))
		update(t, m, pressA)
		require.Equal(t, phaseChat, m.phase)

		require.False(t, m.switchReady())
		require.Equal(t, "a run is in flight", m.switchNote())

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Switch agent")
		require.Contains(t, plain(m.render()), "a run is in flight")
		update(t, m, pressEnter)
		require.Equal(t, phaseSettings, m.phase, "the command cannot open the picker")
	})

	t.Run("stays away without a session", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "coder"}},
			selected: 0,
			sessions: []session.Info{{ID: "session-7", Agent: "coder", Title: "hello"}},
		})

		require.False(t, m.switchReady())
		require.Equal(t, "open a session first", m.switchNote())
	})

	t.Run("ignores a key that completes no chord", func(t *testing.T) {
		m, _ := storeChat(t, textMessage(llm.RoleUser, "first"))

		update(t, m, pressCtrlX)
		update(t, m, pressDown)

		require.Equal(t, phaseChat, m.phase)
		require.False(t, m.leader, "the chord is spent")
	})

	t.Run("labels the answers with the agent that wrote them", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)
		m.agents = append(m.agents, agent.Agent{ID: "reviewer"})

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Switch agent")
		update(t, m, pressEnter)
		typeFilter(t, m, "reviewer")
		update(t, m, pressEnter)
		// The switch writes a row of its own, so the window grows to keep both
		// turns of the conversation in view.
		update(t, m, windowMsg(80, 40))

		// The answer written before the selection keeps its author, and the
		// one written after carries the agent that wrote it.
		_, err := stored.store.Append(t.Context(), session.Entry{
			Message: textMessage(llm.RoleAssistant, "two"),
		})
		require.NoError(t, err)
		m.reloadTranscript()

		view := plain(m.render())
		require.Contains(t, view, "Agent: coder", "the turn before the switch keeps its agent")
		require.Contains(t, view, "Agent: reviewer", "the turn after carries the new agent")
	})

	t.Run("draws the tree turn by turn with its author", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "first"),
			textMessage(llm.RoleAssistant, "one"),
		)
		m.agents = append(m.agents, agent.Agent{ID: "reviewer"})

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Switch agent")
		update(t, m, pressEnter)
		typeFilter(t, m, "reviewer")
		update(t, m, pressEnter)
		_, err := stored.store.Append(t.Context(), session.Entry{
			Message: textMessage(llm.RoleAssistant, "two"),
		})
		require.NoError(t, err)

		update(t, m, pressCtrlT)

		view := plain(m.render())
		require.Contains(t, view, "Agent (coder): one")
		require.Contains(t, view, "Agent (reviewer): two")
	})
}

// TestSwitchModel verifies the model selection of an open conversation: the
// leader chord, the picker it opens over the models of the configuration and
// the command center entry that reaches the same place.
func TestSwitchModel(t *testing.T) {
	// modelChat opens a conversation over a store-backed session whose model
	// roster offers two models.
	modelChat := func(t *testing.T) (*model, *storeSession) {
		t.Helper()
		m, stored := storeChat(t, textMessage(llm.RoleUser, "first"))
		stored.modelRefs = []string{"fake/other-model", "fake/test-model"}
		return m, stored
	}

	t.Run("opens the picker with the leader chord", func(t *testing.T) {
		m, _ := modelChat(t)

		require.Nil(t, update(t, m, pressCtrlX), "the leader only arms the chord")
		update(t, m, pressM)

		require.Equal(t, phasePicker, m.phase)
		require.Equal(t, pickerModel, m.pickerMode)
		view := plain(m.render())
		require.Contains(t, view, "Switch the model of the conversation")
		require.Contains(t, view, "fake/other-model")
		require.Contains(t, view, "fake/test-model")
		require.NotContains(t, view, "A test agent", "the model rows carry no description")
	})

	t.Run("opens on the model the conversation runs", func(t *testing.T) {
		m, _ := modelChat(t)

		update(t, m, pressCtrlX)
		update(t, m, pressM)

		// The list shows the models in the order of the roster and the
		// highlight lands on the running one.
		require.Equal(t, "fake/test-model", m.roster()[m.picker.selected()])
	})

	t.Run("opens the picker from the command center", func(t *testing.T) {
		m, _ := modelChat(t)

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Switch model")
		update(t, m, pressEnter)

		require.Equal(t, phasePicker, m.phase)
		require.Equal(t, pickerModel, m.pickerMode)
	})

	t.Run("selects the model the conversation runs onward", func(t *testing.T) {
		m, stored := modelChat(t)

		update(t, m, pressCtrlX)
		update(t, m, pressM)
		typeFilter(t, m, "other")
		update(t, m, pressEnter)

		require.Equal(t, phaseChat, m.phase)
		require.Equal(t, pickerNewAgent, m.pickerMode)
		require.Equal(t, "fake/other-model", stored.store.ActiveModel())
		require.Contains(t, plain(m.render()), "fake/other-model", "the identity follows the model")
	})

	t.Run("leaves the conversation on escape", func(t *testing.T) {
		m, stored := modelChat(t)

		update(t, m, pressCtrlX)
		update(t, m, pressM)
		update(t, m, pressEscape)

		require.Equal(t, phaseChat, m.phase)
		require.Equal(t, "fake/test-model", stored.store.ActiveModel(), "escape selects nothing")
	})

	t.Run("reports the failure of the session", func(t *testing.T) {
		m, stored := modelChat(t)
		require.NoError(t, stored.store.Close())

		// The picker opens on the model the conversation runs, which selecting
		// would leave unchanged, so the query moves the highlight to another
		// one before the selection is made.
		update(t, m, pressCtrlX)
		update(t, m, pressM)
		typeFilter(t, m, "other")
		update(t, m, pressEnter)

		require.Error(t, m.fatal, "a selection the session cannot write fails the interface")
		require.Contains(t, m.fatal.Error(), "store is closed")
	})

	t.Run("stays away while the agent works", func(t *testing.T) {
		m, _ := chatModel(t)
		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))

		update(t, m, pressCtrlX)
		update(t, m, pressM)
		require.Equal(t, phaseChat, m.phase)

		update(t, m, pressCtrlP)
		typeFilter(t, m, "Switch model")
		require.Contains(t, plain(m.render()), "a run is in flight")
	})
}

// readCountingSession wraps a session and records every read the interface
// makes of it while a run is in flight. The interface renders the conversation
// from what it caches and from what the run reports, so a frame never touches
// the session the run owns; the single deliberate exception is the reload of
// the conversation when the run writes a checkpoint.
type readCountingSession struct {
	Session

	// reads collects the name of every method called while counting is on.
	reads []string

	// counting reports whether the calls are recorded, so the reads the
	// interface makes around a run are not mistaken for reads during one.
	counting bool
}

// count records the name of a read method when counting is on.
func (s *readCountingSession) count(name string) {
	if s.counting {
		s.reads = append(s.reads, name)
	}
}

// Info records the read of the session metadata.
func (s *readCountingSession) Info() session.Info {
	s.count("Info")
	return s.Session.Info()
}

// Branch records the read of the active branch.
func (s *readCountingSession) Branch() []session.Entry {
	s.count("Branch")
	return s.Session.Branch()
}

// DisplayedBranch records the read of the conversation the user reads.
func (s *readCountingSession) DisplayedBranch() []session.Entry {
	s.count("DisplayedBranch")
	return s.Session.DisplayedBranch()
}

// Context records the read of the context measurement.
//
//nolint:wrapcheck // the session reports the failure of the store as it is.
func (s *readCountingSession) Context() (tokens.Report, error) {
	s.count("Context")
	return s.Session.Context()
}

// CompactRefusal records the read of the refusal of the manual compaction.
func (s *readCountingSession) CompactRefusal() (compaction.Refusal, bool) {
	s.count("CompactRefusal")
	return s.Session.CompactRefusal()
}

// Runnable records the read of what the branch can run.
func (s *readCountingSession) Runnable() (engine.RunnableRefusal, bool) {
	s.count("Runnable")
	return s.Session.Runnable()
}

// Tree records the read of the whole tree.
func (s *readCountingSession) Tree() []session.Entry {
	s.count("Tree")
	return s.Session.Tree()
}

// ActiveAgent records the read of the agent the branch runs.
func (s *readCountingSession) ActiveAgent() string {
	s.count("ActiveAgent")
	return s.Session.ActiveAgent()
}

// ActiveModel records the read of the model the branch runs.
func (s *readCountingSession) ActiveModel() string {
	s.count("ActiveModel")
	return s.Session.ActiveModel()
}

// ThinkingLevel records the read of the thinking level of the model the branch
// runs.
func (s *readCountingSession) ThinkingLevel() string {
	s.count("ThinkingLevel")
	return s.Session.ThinkingLevel()
}

// Models records the read of the model roster.
func (s *readCountingSession) Models() []string {
	s.count("Models")
	return s.Session.Models()
}

// TestRunReadsNoSession verifies that rendering the interface while a run is
// in flight never reads the session. The run owns the session from the moment
// it starts until it ends, so the header, the footer, the conversation and the
// command center all render from what the interface caches and from what the
// run reports, never from the session itself. This is what keeps a frame from
// racing the run that is appending to the store.
func TestRunReadsNoSession(t *testing.T) {
	t.Run("renders the conversation without reading the session", func(t *testing.T) {
		m, stored := storeChat(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		counting := &readCountingSession{Session: stored}
		m.session = counting

		m.input.SetValue("go")
		require.NotNil(t, update(t, m, pressEnter))

		// The header is rendered from the identity the interface cached when
		// the branch changed, so even a run start event that carries the agent
		// and the model never reaches the session.
		counting.counting = true
		sendEvent(t, m, engine.Event{
			Type:    engine.EventRunStart,
			AgentID: "coder",
			ModelID: "fake/test-model",
		})
		sendEvent(t, m, engine.Event{Type: engine.EventTextDelta, Text: "working"})
		require.True(t, m.running)

		// Every screen the user can reach while the run works must render from
		// what the interface caches: the conversation, the command center and
		// the picker it offers.
		require.NotEmpty(t, m.render())
		update(t, m, pressCtrlP)
		require.NotEmpty(t, m.render())
		require.Contains(t, plain(m.render()), "a run is in flight")
		update(t, m, pressEscape)
		require.Equal(t, phaseChat, m.phase)
		require.NotEmpty(t, m.render())

		require.Empty(t, counting.reads, "no read reaches the session while the run works")
	})

	t.Run("reloads the conversation when the run writes a checkpoint", func(t *testing.T) {
		m, stored := storeChat(t, textMessage(llm.RoleUser, "hello"))
		counting := &readCountingSession{Session: stored}
		m.session = counting

		m.input.SetValue("go")
		require.NotNil(t, update(t, m, pressEnter))
		require.True(t, m.running)

		first := stored.store.Branch()[0]
		_, err := stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			first.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)

		// The checkpoint replaces the turns the user reads, so the conversation
		// is rebuilt while the run is still in flight. This is the one read of
		// the session the interface makes during a run, and it is safe because
		// the store serializes it.
		counting.counting = true
		sendEvent(t, m, engine.Event{Type: engine.EventCompactionEnd})
		require.True(t, m.running)
		require.Contains(t, counting.reads, "DisplayedBranch")
		require.Contains(t, plain(m.render()), "Compaction")
	})

	t.Run("reads the session again once the run ends", func(t *testing.T) {
		m, stored := storeChat(t, textMessage(llm.RoleUser, "hello"))
		counting := &readCountingSession{Session: stored}
		m.session = counting

		m.input.SetValue("go")
		require.NotNil(t, update(t, m, pressEnter))

		counting.counting = true
		sendEvent(t, m, engine.Event{
			Type:   engine.EventRunEnd,
			Reason: engine.EndReasonTurn,
		})
		require.False(t, m.running)

		// The run is over, so the interface may measure the branch again and
		// the footer follows it.
		require.Contains(t, counting.reads, "Context")
	})
}

// resumedAgentModel opens an interface over a session whose branch runs an
// agent the interface no longer holds, which is the case of a session the user
// continued after renaming the agent definition it ran.
func resumedAgentModel(t *testing.T) (*model, *fakeSession) {
	t.Helper()

	scripted := newFakeSession()
	scripted.info.Agent = "coder"
	scripted.activeAgent = "coder"
	scripted.runnable = engine.RunnableRefusal{Kind: engine.RunnableUnknownAgent, ID: "coder"}
	scripted.stopped = true

	m := newTestModelWith(t, modelConfig{
		agents:   []agent.Agent{{ID: "new-coder"}},
		selected: -1,
		sessions: []session.Info{{ID: "session-1", Agent: "coder", Title: "old"}},
		resumeSession: func(string) (Session, error) {
			return scripted, nil
		},
	})
	update(t, m, windowMsg(80, 24))
	require.Equal(t, phaseStart, m.phase)

	// The offer to begin a new session leads the list, so the stored session
	// is one row down.
	update(t, m, pressDown)
	run(t, m, update(t, m, pressEnter))

	require.Equal(t, phaseChat, m.phase)
	return m, scripted
}

// TestStoppedBranch verifies the conversation whose branch holds nothing to
// run: the session opens and is read, sending waits, and selecting an agent
// that exists takes it forward again.
func TestStoppedBranch(t *testing.T) {
	t.Run("opens a session whose agent is gone", func(t *testing.T) {
		m, _ := resumedAgentModel(t)

		require.NotNil(t, m.session)
		require.NotNil(t, m.runnable)
		require.Equal(t, engine.RunnableUnknownAgent, m.runnable.Kind)
	})

	t.Run("refuses to send a prompt", func(t *testing.T) {
		m, scripted := resumedAgentModel(t)

		m.input.SetValue("hello")
		cmd := update(t, m, pressEnter)

		require.Nil(t, cmd, "sending waits until an agent that exists is selected")
		require.False(t, m.running)
		require.Empty(t, scripted.prompts)
		require.Equal(t, "hello", m.input.Value(), "the prompt stays where it was")
	})

	t.Run("says what is missing in the status line", func(t *testing.T) {
		m, _ := resumedAgentModel(t)

		require.Contains(t, plain(m.render()), "the agent coder is gone")
		require.Contains(t, plain(m.render()), "select a running one to send")
		require.Equal(t, noticeRows, m.activityHeight(), "the notice breathes like a branch one")
	})

	t.Run("runs again once an agent that exists is selected", func(t *testing.T) {
		m, scripted := resumedAgentModel(t)

		// The selection clears the reason and the prompt sends again.
		scripted.stopped = false
		update(t, m, pressCtrlX)
		update(t, m, pressA)
		require.Equal(t, phasePicker, m.phase)
		run(t, m, update(t, m, pressEnter))
		require.Equal(t, phaseChat, m.phase)
		require.Nil(t, m.runnable)
		require.Equal(t, []string{"new-coder"}, scripted.agents)

		m.input.SetValue("hello")
		require.NotNil(t, update(t, m, pressEnter))
		require.True(t, m.running)
		require.Equal(t, []string{"hello"}, scripted.prompts)
	})

	t.Run("marks a stored session whose agent is gone", func(t *testing.T) {
		m := newTestModelWith(t, modelConfig{
			agents:   []agent.Agent{{ID: "new-coder"}},
			selected: 0,
			sessions: []session.Info{
				{ID: "session-1", Agent: "coder", ActiveAgent: "coder", Title: "old"},
				{ID: "session-2", Agent: "new-coder", ActiveAgent: "new-coder", Title: "current"},
			},
		})
		update(t, m, windowMsg(80, 24))

		view := plain(m.render())

		require.Contains(t, view, "old")
		require.Contains(t, view, "agent missing")
		require.NotContains(
			t,
			lineOf(view, "current"),
			"agent missing",
			"a session whose agent exists is offered unmarked",
		)
	})

	t.Run("reports the model that is gone", func(t *testing.T) {
		scripted := newFakeSession()
		scripted.runnable = engine.RunnableRefusal{
			Kind: engine.RunnableUnknownModel,
			ID:   "fake/old-model",
		}
		scripted.stopped = true
		m := newTestModel(
			t,
			[]agent.Agent{{ID: "coder"}},
			0,
			func(string) (Session, error) { return scripted, nil },
		)
		run(t, m, m.Init())
		update(t, m, windowMsg(80, 24))

		require.Contains(t, plain(m.render()), "the model fake/old-model is gone")
	})
}

// lineOf returns the line of a rendered view that holds a text.
func lineOf(view, text string) string {
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}
