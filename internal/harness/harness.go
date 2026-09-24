package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/config"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
	"github.com/varavelio/rienda/internal/tool"
)

// configEnvVar names the environment variable that overrides the
// configuration file path when Options leaves it unset.
const configEnvVar = "RIENDA_CONFIG"

// Options configures the preparation of a session.
type Options struct {
	// AgentID is the identifier of the agent definition to run. It is required
	// to create a session. Together with SessionID it selects the agent the
	// resumed session runs from now on, without touching the turns it already
	// holds.
	AgentID string

	// SessionID resumes the session with this identifier instead of creating
	// a new one. The agent of the resumed session comes from its stored
	// header.
	SessionID string

	// ModelRef overrides the model the session runs, as a provider/model
	// reference the configuration holds. For a new session it replaces the
	// model of the agent definition, which is what the header records; for a
	// resumed one it selects the model of the branch that continues the
	// conversation.
	ModelRef string

	// Workdir is the directory the session runs commands in. It defaults to
	// the process working directory.
	Workdir string

	// ConfigPath overrides the path of the configuration file. It defaults to
	// the RIENDA_CONFIG environment variable and then to the default path.
	ConfigPath string

	// AgentsDir overrides the directory holding the agent definitions. It
	// defaults to the global agent directory.
	AgentsDir string

	// SessionsDir overrides the base directory holding the session files. It
	// defaults to the global sessions directory.
	SessionsDir string
}

// Session is a prepared agent session ready to run.
type Session struct {
	store  *session.Store
	engine *engine.Engine

	// resolver resolves the model references of the branch against the
	// configuration. It is kept so the session can report settings that are
	// declared in the configuration and never stored, such as the extended
	// thinking level of the model it runs.
	resolver *modelResolver
}

// Branch returns the entries of the active branch in conversation order, which
// is the conversation the session runs.
func (s *Session) Branch() []session.Entry {
	return s.store.Branch()
}

// Context reports the estimated context of the active branch, measured against
// the context window resolved for the model of the session.
func (s *Session) Context() (tokens.Report, error) {
	report, err := s.engine.Context()
	if err != nil {
		return tokens.Report{}, fmt.Errorf("harness: %w", err)
	}
	return report, nil
}

// DisplayedBranch returns the entries of the active branch the user reads, with
// the newest compaction applied: the summarized turns are replaced by the
// checkpoint that summarizes them.
func (s *Session) DisplayedBranch() []session.Entry {
	return s.store.DisplayedBranch()
}

// CompactRefusal reports why the active branch holds nothing to compact, and
// false when it holds something. The manual command is offered only when it
// holds something, so the reason explains a command the interface cannot run.
func (s *Session) CompactRefusal() (compaction.Refusal, bool) {
	refusal, refused := s.engine.CompactRefusal()
	if !refused {
		return compaction.Refusal{}, false
	}
	return refusal, true
}

// CanCompact reports whether the active branch still holds something to
// summarize, which is what the manual compaction command is offered on.
func (s *Session) CanCompact() bool {
	return s.engine.CanCompact()
}

// Compact summarizes the active branch on demand and returns the channel
// carrying its events. It ignores the compaction threshold, because the user
// asked for it, and it refuses to run while another run is in flight.
func (s *Session) Compact(ctx context.Context) <-chan engine.Event {
	return s.engine.Compact(ctx)
}

// ActiveAgent returns the identifier of the agent the branch of the session
// runs now, which is the newest selection of the branch or the agent the
// session was created with.
func (s *Session) ActiveAgent() string {
	return s.store.ActiveAgent()
}

// Tree returns every entry of the session in append order, the branches it
// leaves behind included, which is what lets a front end show the whole
// conversation and return to an earlier turn of it.
func (s *Session) Tree() []session.Entry {
	return s.store.Entries()
}

// SetLeaf moves the active leaf of the session to the entry identified by id,
// or before the first message when id is empty, so the next run continues from
// it. Writing after a turn that already has messages opens a branch beside
// them, while writing after a leaf continues the branch.
func (s *Session) SetLeaf(id string) error {
	if err := s.store.SetLeaf(id); err != nil {
		return fmt.Errorf("harness: %w", err)
	}
	return nil
}

// SetAgent selects the agent the branch of the session runs from now on. The
// selection is appended to the session and belongs to the branch that wrote
// it, so returning to an earlier turn runs on the agent that was in effect
// there. An agent the harness does not know is refused before anything is
// written, so a session never ends up pointing at an agent no run can honor.
func (s *Session) SetAgent(ctx context.Context, id string) error {
	if !s.engine.KnowsAgent(id) {
		return fmt.Errorf("harness: unknown agent %q", id)
	}
	if err := s.store.SetAgent(ctx, id); err != nil {
		return fmt.Errorf("harness: %w", err)
	}
	return nil
}

// ActiveModel returns the provider/model reference the branch of the session
// runs now, which is the newest selection of the branch or the model the
// session was created with.
func (s *Session) ActiveModel() string {
	return s.store.ActiveModel()
}

// ModelInfo returns how a model reference is described beyond the reference
// itself: the wire identifier the provider receives and the extended thinking
// level the configuration declares. Both are generation settings of the model,
// never part of the session, so they are read from the configuration instead of
// the stored conversation, and a reference the configuration no longer holds
// describes nothing.
func (s *Session) ModelInfo(ref string) engine.ModelInfo {
	return s.resolver.Info(ref)
}

// Models returns the provider/model references the session may run, sorted,
// which is the roster a front end offers to switch the model of the session.
func (s *Session) Models() []string {
	return s.engine.Models()
}

// SetModel selects the model the branch of the session runs from now on. The
// selection is appended to the session and belongs to the branch that wrote it,
// so returning to an earlier turn runs on the model that was in effect there. A
// model reference the configuration does not hold is refused before anything is
// written, so a session never ends up pointing at a model no run can honor.
func (s *Session) SetModel(ctx context.Context, ref string) error {
	if !s.engine.KnowsModel(ref) {
		return fmt.Errorf("harness: unknown model %q", ref)
	}
	if err := s.store.SetModel(ctx, ref); err != nil {
		return fmt.Errorf("harness: %w", err)
	}
	return nil
}

// Runnable reports why the branch of the session holds nothing to run and
// false when it holds something: the agent the branch runs must be one of the
// definitions of the roster and the model it runs one the configuration holds.
// A branch that holds nothing to run still walks and reads, so a front end
// shows the conversation and waits for a selection instead of refusing the
// session.
func (s *Session) Runnable() (engine.RunnableRefusal, bool) {
	return s.engine.Runnable()
}

// SetTag replaces the tag of the entry identified by id, an empty tag removing
// the one it carries.
func (s *Session) SetTag(id, tag string) error {
	if err := s.store.SetTag(id, tag); err != nil {
		return fmt.Errorf("harness: %w", err)
	}
	return nil
}

// SetTitle names the session, an empty title removing the name it carries and
// leaving the one derived from its first user message in its place.
func (s *Session) SetTitle(title string) error {
	if err := s.store.SetTitle(title); err != nil {
		return fmt.Errorf("harness: %w", err)
	}
	return nil
}

// Prepare resolves the options and opens a session: the one identified by
// Options.SessionID when it is set, or a new one owned by Options.AgentID.
func Prepare(ctx context.Context, opts Options) (*Session, error) {
	workdir, err := resolveWorkdir(opts.Workdir)
	if err != nil {
		return nil, err
	}

	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("harness: load configuration: %w", err)
	}

	agentsDir, err := resolveAgentsDir(opts.AgentsDir)
	if err != nil {
		return nil, err
	}
	sessionsDir, err := resolveSessionsDir(opts.SessionsDir)
	if err != nil {
		return nil, err
	}
	projectDir, err := projectDir(sessionsDir, workdir)
	if err != nil {
		return nil, err
	}

	// Every definition is loaded eagerly, so a broken file is reported before
	// a session is opened and the roster the engine may select from is
	// complete: a session can run on any agent of the roster, not only on the
	// one it was created with.
	definitions, err := agent.LoadAll(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("harness: load agents: %w", err)
	}

	store, err := openSession(ctx, opts, cfg, sessionDir{
		dir:     projectDir,
		workdir: workdir,
		agents:  agentsDir,
		roster:  definitions,
		models:  cfg.ModelRefs(),
	})
	if err != nil {
		return nil, err
	}

	// Every model the configuration holds is resolvable by the engine, and the
	// resolver caches one client per reference, so a session that switches
	// model reuses the connections of the models it already talked to. The
	// resolver stamps every client with the identity of the session, so every
	// call identifies itself with it, the summarizations included.
	resolver := newModelResolver(cfg, store.ID())

	registry, err := newTools()
	if err != nil {
		closeStore(store)
		return nil, err
	}

	summarizer, err := newCompactor(cfg, resolver)
	if err != nil {
		closeStore(store)
		return nil, err
	}

	runner, err := engine.New(engine.Config{
		Store:     store,
		Agents:    definitions,
		Resolver:  resolver,
		Registry:  registry,
		Workdir:   workdir,
		Compactor: summarizer,
		Compaction: engine.Compaction{
			Enabled:       cfg.Compaction.Enabled,
			ReserveTokens: cfg.Compaction.ReserveTokens,
		},
	})
	if err != nil {
		closeStore(store)
		return nil, fmt.Errorf("harness: build engine: %w", err)
	}

	return &Session{store: store, engine: runner, resolver: resolver}, nil
}

// sessionDir gathers where a session lives and what it may run, so the
// preparation helpers stay readable as the options of a session grow.
type sessionDir struct {
	// dir holds the session files of the workspace.
	dir string

	// workdir is the absolute directory the session runs tools in.
	workdir string

	// agents holds the agent definition files, named in the errors.
	agents string

	// roster lists the agents a session may run.
	roster []agent.Agent

	// models lists the provider/model references a session may run, which is
	// the roster the configuration holds.
	models []string
}

// openSession returns the store of the session to run: the one identified by
// opts.SessionID, or a new one created for the agent identified by
// opts.AgentID. Every agent and model the session may run comes from the roster
// the caller resolved, and either named by the options must be one of them, so
// a mistyped identifier fails with the path or the reference it expected.
//
// Resuming with an agent or a model selects it on the branch that continues the
// conversation, which is the same change SetAgent and SetModel make. The
// selection is validated before it is written, so a session never ends up
// pointing at an agent or a model no run can honor. Resuming without them is
// lenient instead: a stored agent or model that is gone opens the conversation
// for reading, with nothing to run until another selection names one that
// exists, which is what lets a front end read a session whose definition was
// renamed or removed.
//
// Creating a session is strict: the user asked to run the agent now, so the
// model it runs is resolved against the configuration here, and a broken
// definition fails before anything is written.
func openSession(
	ctx context.Context,
	opts Options,
	cfg *config.Config,
	place sessionDir,
) (*session.Store, error) {
	sessionID := strings.TrimSpace(opts.SessionID)
	agentID := strings.TrimSpace(opts.AgentID)
	modelRef := strings.TrimSpace(opts.ModelRef)
	if sessionID == "" && agentID == "" {
		return nil, errors.New("harness: an agent id is required")
	}

	if sessionID != "" {
		store, err := session.Open(place.dir, sessionID, id.NewIDGenerator())
		if err != nil {
			return nil, fmt.Errorf("harness: open session %q: %w", sessionID, err)
		}
		switch {
		case agentID != "" && !holds(place.roster, agentID):
			closeStore(store)
			return nil, undefinedAgent(place.agents, agentID)
		case modelRef != "" && !slices.Contains(place.models, modelRef):
			closeStore(store)
			return nil, undefinedModel(modelRef)
		}
		if agentID != "" {
			if err := store.SetAgent(ctx, agentID); err != nil {
				closeStore(store)
				return nil, fmt.Errorf("harness: %w", err)
			}
		}
		if modelRef != "" {
			if err := store.SetModel(ctx, modelRef); err != nil {
				closeStore(store)
				return nil, fmt.Errorf("harness: %w", err)
			}
		}
		return store, nil
	}

	definition, found := findAgent(place.roster, agentID)
	if !found {
		return nil, undefinedAgent(place.agents, agentID)
	}
	// A model named for a new session is the model the session is created with,
	// which is what its header records.
	model := definition.Model
	if modelRef != "" {
		if !slices.Contains(place.models, modelRef) {
			return nil, undefinedModel(modelRef)
		}
		model = modelRef
	}
	// The model the session is created with is resolved here, so a definition
	// that declares a model the configuration does not hold fails before the
	// session is written rather than at its first run.
	if _, err := cfg.Resolve(model); err != nil {
		return nil, fmt.Errorf("harness: model %q: %w", model, err)
	}
	store, err := session.Create(ctx, place.dir, session.Header{
		Agent:   definition.ID,
		Model:   model,
		Workdir: place.workdir,
	}, id.NewIDGenerator())
	if err != nil {
		return nil, fmt.Errorf("harness: create session: %w", err)
	}
	return store, nil
}

// holds reports whether a roster of agents holds the one identified by id.
func holds(roster []agent.Agent, id string) bool {
	_, found := findAgent(roster, id)
	return found
}

// undefinedModel reports a model reference the configuration does not hold,
// naming the reference so a typo points at itself.
func undefinedModel(ref string) error {
	return fmt.Errorf(
		"harness: model %q is not declared in the configuration",
		ref,
	)
}

// undefinedAgent reports an agent identifier the roster does not hold, naming
// the definition file the session expected so a typo points at its own path.
func undefinedAgent(agentsDir, id string) error {
	path := filepath.Join(agentsDir, id+agent.Extension)
	return fmt.Errorf(
		"harness: agent %q is not defined in the agent directory, which holds no %s",
		id,
		path,
	)
}

// findAgent returns the definition of the agent identified by id and whether
// the roster holds one.
func findAgent(definitions []agent.Agent, id string) (agent.Agent, bool) {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return agent.Agent{}, false
}

// Sessions returns the sessions stored for the workspace of the options, most
// recently updated first. A workspace without sessions yields no sessions.
// Sessions that cannot be read are skipped, and the returned error reports
// them together with the sessions that could be read.
func Sessions(opts Options) ([]session.Info, error) {
	workdir, err := resolveWorkdir(opts.Workdir)
	if err != nil {
		return nil, err
	}
	sessionsDir, err := resolveSessionsDir(opts.SessionsDir)
	if err != nil {
		return nil, err
	}
	dir, err := projectDir(sessionsDir, workdir)
	if err != nil {
		return nil, err
	}

	infos, err := session.List(dir)
	if err != nil {
		return infos, fmt.Errorf("harness: list sessions: %w", err)
	}
	return infos, nil
}

// closeStore releases a session store when there is one, discarding the close
// error because the caller is already handling the failure that triggers it.
func closeStore(store *session.Store) {
	if store != nil {
		_ = store.Close()
	}
}

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.store.ID()
}

// Info returns the session metadata.
func (s *Session) Info() session.Info {
	return s.store.Info()
}

// Run starts a run of the session and returns the channel carrying its events.
// A non-empty prompt starts a new turn from the active leaf, which opens a
// branch when the leaf already has turns after it; an empty prompt continues
// the conversation from that leaf.
func (s *Session) Run(ctx context.Context, prompt string) <-chan engine.Event {
	return s.engine.Run(ctx, prompt)
}

// Close releases the session file.
func (s *Session) Close() error {
	if err := s.store.Close(); err != nil {
		return fmt.Errorf("harness: close session: %w", err)
	}
	return nil
}

// newTools builds the registry of built-in tools. Every built-in is
// registered; the agent definition selects the ones its runs may call.
func newTools() (*tool.Registry, error) {
	shell, err := tool.NewShell(tool.ShellOptions{})
	if err != nil {
		return nil, fmt.Errorf("harness: build shell tool: %w", err)
	}
	devcontainerShell, err := tool.NewDevcontainerShell(tool.DevcontainerShellOptions{})
	if err != nil {
		return nil, fmt.Errorf("harness: build devcontainer shell tool: %w", err)
	}
	registry, err := tool.NewRegistry(shell, devcontainerShell)
	if err != nil {
		return nil, fmt.Errorf("harness: build tool registry: %w", err)
	}
	return registry, nil
}

// projectDir returns the directory holding the sessions of a workspace.
func projectDir(sessionsDir, workdir string) (string, error) {
	projectID, err := session.ProjectID(workdir)
	if err != nil {
		return "", fmt.Errorf("harness: %w", err)
	}
	dir, err := session.ProjectDir(sessionsDir, projectID)
	if err != nil {
		return "", fmt.Errorf("harness: %w", err)
	}
	return dir, nil
}

// resolveWorkdir returns the directory a session runs in, defaulting to the
// process working directory. The returned path is absolute.
func resolveWorkdir(requested string) (string, error) {
	dir := strings.TrimSpace(requested)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("harness: locate process working directory: %w", err)
		}
		dir = cwd
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("harness: resolve workdir %s: %w", dir, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("harness: workdir %s: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("harness: workdir %s is not a directory", absolute)
	}
	return absolute, nil
}

// resolveConfigPath returns the configuration file path: an explicit path
// wins over the environment variable, which wins over the default path.
func resolveConfigPath(explicit string) (string, error) {
	if path := strings.TrimSpace(explicit); path != "" {
		return path, nil
	}
	if path := strings.TrimSpace(os.Getenv(configEnvVar)); path != "" {
		return path, nil
	}
	path, err := config.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("harness: %w", err)
	}
	return path, nil
}

// resolveAgentsDir returns the directory holding the agent definitions: the
// requested one, or the global agent directory.
func resolveAgentsDir(requested string) (string, error) {
	if dir := strings.TrimSpace(requested); dir != "" {
		return dir, nil
	}
	dir, err := agent.DefaultDir()
	if err != nil {
		return "", fmt.Errorf("harness: locate agent directory: %w", err)
	}
	return dir, nil
}

// resolveSessionsDir returns the base directory holding the session files: the
// requested one, or the global sessions directory.
func resolveSessionsDir(requested string) (string, error) {
	if dir := strings.TrimSpace(requested); dir != "" {
		return dir, nil
	}
	dir, err := session.DefaultDir()
	if err != nil {
		return "", fmt.Errorf("harness: locate sessions directory: %w", err)
	}
	return dir, nil
}
