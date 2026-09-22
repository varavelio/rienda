package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/catalog"
	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/config"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/provider"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tokens"
	"github.com/varavelio/rienda/internal/tool"
)

// configEnvVar names the environment variable that overrides the
// configuration file path when Options leaves it unset.
const configEnvVar = "RIENDA_CONFIG"

// Options configures the preparation of a session.
type Options struct {
	// AgentID is the identifier of the agent definition to run. It is
	// required to create a session and must be empty when SessionID resumes
	// one.
	AgentID string

	// SessionID resumes the session with this identifier instead of creating
	// a new one. The agent of the resumed session comes from its stored
	// header.
	SessionID string

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

// SetTag replaces the tag of the entry identified by id, an empty tag removing
// the one it carries.
func (s *Session) SetTag(id, tag string) error {
	if err := s.store.SetTag(id, tag); err != nil {
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

	store, definition, err := openSession(ctx, opts, projectDir, workdir, agentsDir)
	if err != nil {
		return nil, err
	}

	resolved, err := cfg.Resolve(definition.Model)
	if err != nil {
		closeStore(store)
		return nil, fmt.Errorf("harness: agent %q: %w", definition.ID, err)
	}
	client, err := provider.New(resolved.Protocol, resolved.ProviderConfig)
	if err != nil {
		closeStore(store)
		return nil, fmt.Errorf("harness: build provider client: %w", err)
	}

	registry, err := newTools()
	if err != nil {
		closeStore(store)
		return nil, err
	}

	model := engineModel(resolved)
	model.ContextWindow = resolveContextWindow(resolved)

	summarizer, err := newCompactor(cfg, resolved, client)
	if err != nil {
		closeStore(store)
		return nil, err
	}

	runner, err := engine.New(engine.Config{
		Client:    client,
		Store:     store,
		Agent:     definition,
		Model:     model,
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

	return &Session{store: store, engine: runner}, nil
}

// openSession returns the store of the session to run and the agent definition
// that owns it. It resumes the session identified by opts.SessionID, or creates
// a new session owned by opts.AgentID in dir.
func openSession(
	ctx context.Context,
	opts Options,
	dir, workdir, agentsDir string,
) (*session.Store, agent.Agent, error) {
	sessionID := strings.TrimSpace(opts.SessionID)
	agentID := strings.TrimSpace(opts.AgentID)
	switch {
	case sessionID != "" && agentID != "":
		return nil, agent.Agent{}, errors.New(
			"harness: an agent id and a session id are mutually exclusive",
		)
	case sessionID == "" && agentID == "":
		return nil, agent.Agent{}, errors.New("harness: an agent id is required")
	}

	if sessionID != "" {
		store, err := session.Open(dir, sessionID, id.NewIDGenerator())
		if err != nil {
			return nil, agent.Agent{}, fmt.Errorf("harness: open session %q: %w", sessionID, err)
		}
		owner := store.Info().Agent
		definition, err := agent.Load(agentsDir, owner)
		if err != nil {
			closeStore(store)
			return nil, agent.Agent{}, fmt.Errorf("harness: load agent %q: %w", owner, err)
		}
		return store, definition, nil
	}

	definition, err := agent.Load(agentsDir, agentID)
	if err != nil {
		return nil, agent.Agent{}, fmt.Errorf("harness: load agent %q: %w", agentID, err)
	}
	store, err := session.Create(ctx, dir, session.Header{
		Agent:   definition.ID,
		Model:   definition.Model,
		Workdir: workdir,
	}, id.NewIDGenerator())
	if err != nil {
		return nil, agent.Agent{}, fmt.Errorf("harness: create session: %w", err)
	}
	return store, definition, nil
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

// engineModel translates the resolved model settings into engine form.
func engineModel(resolved config.Resolved) engine.Model {
	return engine.Model{
		ID:          resolved.ModelID,
		MaxTokens:   resolved.MaxTokens,
		Temperature: resolved.Temperature,
		TopP:        resolved.TopP,
		Thinking: llm.ThinkingConfig{
			Level:     resolved.ThinkingLevel,
			MaxTokens: resolved.ThinkingMaxTokens,
		},
	}
}

// resolveContextWindow returns the context window of a resolved model: the
// value the configuration declares, the value the catalog knows, or the
// conservative fallback. The catalog is best effort, so a home directory that
// cannot be located falls back instead of failing the session.
func resolveContextWindow(resolved config.Resolved) int {
	facts, err := catalog.New(catalog.Options{})
	if err != nil {
		if resolved.ContextWindow > 0 {
			return resolved.ContextWindow
		}
		return catalog.FallbackWindow
	}
	return facts.Window(resolved.ModelID, resolved.ContextWindow)
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
