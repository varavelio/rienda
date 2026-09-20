package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/filecomplete"
	"github.com/varavelio/rienda/internal/files"
	"github.com/varavelio/rienda/internal/harness"
	"github.com/varavelio/rienda/internal/session"
)

// Run starts the interactive interface. It parses the options, offers a new
// session and the previous ones of the workspace to continue from a single
// list, plus the agent definitions a new session needs, and drives the chosen
// session until the user quits. The stored sessions are read again whenever
// the list opens, so a session created while the interface runs shows up.
func Run(args []string, stdin io.Reader, stdout io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		if errors.Is(err, errHelp) {
			usage(stdout)
			return nil
		}
		return err
	}

	agentsDir, err := agent.DefaultDir()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	definitions, err := loadAgents(agentsDir)
	if err != nil {
		return err
	}
	selected, err := selectAgent(definitions, opts.AgentID)
	if err != nil {
		return err
	}

	prepare := func(sessionID, agentID string) (Session, error) {
		return harness.Prepare(context.Background(), harness.Options{
			AgentID:    agentID,
			SessionID:  sessionID,
			AgentsDir:  agentsDir,
			Workdir:    opts.Workdir,
			ConfigPath: opts.ConfigPath,
		})
	}

	app := newModel(modelConfig{
		agents:    definitions,
		selected:  selected,
		requested: opts.AgentID != "",
		sessions:  listSessions(opts, definitions),
		scanSessions: func() []session.Info {
			return listSessions(opts, definitions)
		},
		newSession: func(agentID string) (Session, error) {
			return prepare("", agentID)
		},
		resumeSession: func(sessionID string) (Session, error) {
			return prepare(sessionID, "")
		},
		newRunContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
		scanFiles: newFileScanner(opts),
	})
	defer app.Close()

	program := tea.NewProgram(app, tea.WithInput(stdin), tea.WithOutput(stdout))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: run interface: %w", err)
	}
	return app.fatal
}

// listSessions returns the sessions the interface offers for the workspace of
// the options: the ones whose agent definition is still available and that
// hold a conversation, most recently updated first. Sessions that cannot be
// read are skipped, so a single corrupt session file never blocks the
// interface.
func listSessions(opts options, definitions []agent.Agent) []session.Info {
	infos, _ := harness.Sessions(harness.Options{
		Workdir:    opts.Workdir,
		ConfigPath: opts.ConfigPath,
	})
	return resumable(infos, definitions)
}

// newFileScanner returns the read that completes the mentions of the prompt,
// or nil when the project it would list cannot be located. The interface then
// runs without file completion instead of refusing to open.
func newFileScanner(opts options) fileScanner {
	project := opts.Workdir
	if project == "" {
		workdir, err := os.Getwd()
		if err != nil {
			return nil
		}
		project = workdir
	}

	listing := files.New(project)
	return func() ([]filecomplete.Suggestion, error) {
		return filecomplete.List(listing)
	}
}

// loadAgents returns the agent definitions available to the interface. It
// fails when there is nothing to run, so the interface never opens empty.
func loadAgents(dir string) ([]agent.Agent, error) {
	definitions, err := agent.LoadAll(dir)
	if err != nil {
		return nil, fmt.Errorf("tui: load agents: %w", err)
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf(
			"tui: no agent definitions found in %s, create one to use the interface",
			dir,
		)
	}
	return definitions, nil
}

// resumable keeps the sessions the interface can open: those whose agent
// definition is still available and that hold a conversation.
func resumable(infos []session.Info, definitions []agent.Agent) []session.Info {
	available := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		available[definition.ID] = true
	}

	kept := make([]session.Info, 0, len(infos))
	for _, info := range infos {
		if available[info.Agent] && info.Title != "" {
			kept = append(kept, info)
		}
	}
	return kept
}

// selectAgent returns the index of the agent to run, or -1 when the user must
// pick one from the list.
func selectAgent(definitions []agent.Agent, requested string) (int, error) {
	if requested == "" {
		if len(definitions) == 1 {
			return 0, nil
		}
		return -1, nil
	}

	for i, definition := range definitions {
		if definition.ID == requested {
			return i, nil
		}
	}

	available := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		available = append(available, definition.ID)
	}
	return 0, fmt.Errorf(
		"tui: unknown agent %q (available: %s)",
		requested,
		strings.Join(available, ", "),
	)
}
