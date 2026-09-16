package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/config"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/id"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/provider"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/tool"
)

// configEnvVar names the environment variable that overrides the
// configuration file path when Options leaves it unset.
const configEnvVar = "RIENDA_CONFIG"

// Options configures the preparation of a session.
type Options struct {
	// AgentID is the identifier of the agent definition to run. It is
	// required.
	AgentID string

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

// Prepare resolves the options and opens a new session.
func Prepare(ctx context.Context, opts Options) (*Session, error) {
	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		return nil, errors.New("harness: an agent id is required")
	}

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

	agentsDir := opts.AgentsDir
	if agentsDir == "" {
		agentsDir, err = agent.DefaultDir()
		if err != nil {
			return nil, fmt.Errorf("harness: locate agent directory: %w", err)
		}
	}
	definition, err := agent.Load(agentsDir, agentID)
	if err != nil {
		return nil, fmt.Errorf("harness: load agent %q: %w", agentID, err)
	}

	resolved, err := cfg.Resolve(definition.Model)
	if err != nil {
		return nil, fmt.Errorf("harness: agent %q: %w", agentID, err)
	}
	client, err := provider.New(resolved.Protocol, resolved.ProviderConfig)
	if err != nil {
		return nil, fmt.Errorf("harness: build provider client: %w", err)
	}

	registry, err := newTools()
	if err != nil {
		return nil, err
	}

	sessionsDir := opts.SessionsDir
	if sessionsDir == "" {
		sessionsDir, err = session.DefaultDir()
		if err != nil {
			return nil, fmt.Errorf("harness: locate sessions directory: %w", err)
		}
	}
	projectDir, err := projectDir(sessionsDir, workdir)
	if err != nil {
		return nil, err
	}
	store, err := session.Create(ctx, projectDir, session.Header{
		Agent:   definition.ID,
		Model:   definition.Model,
		Workdir: workdir,
	}, id.NewIDGenerator())
	if err != nil {
		return nil, fmt.Errorf("harness: create session: %w", err)
	}

	runner, err := engine.New(engine.Config{
		Client:   client,
		Store:    store,
		Agent:    definition,
		Model:    engineModel(resolved),
		Registry: registry,
		Workdir:  workdir,
	})
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("harness: build engine: %w", err)
	}

	return &Session{store: store, engine: runner}, nil
}

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.store.ID()
}

// Run starts a run of the session and returns the channel carrying its
// events. A non-empty prompt starts a new turn; an empty prompt continues the
// conversation from its active leaf.
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
		ID:        resolved.ModelID,
		MaxTokens: resolved.MaxTokens,
		Reasoning: llm.ReasoningConfig{
			Effort:       resolved.ReasoningEffort,
			BudgetTokens: resolved.ReasoningBudgetTokens,
		},
	}
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
