package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/harness"
)

// Run starts the interactive interface. It parses the options, lists the
// agent definitions available to it and drives the session of the chosen
// agent until the user quits.
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

	app := newModel(modelConfig{
		agents:   definitions,
		selected: selected,
		newSession: func(agentID string) (Session, error) {
			return harness.Prepare(context.Background(), harness.Options{
				AgentID:    agentID,
				AgentsDir:  agentsDir,
				Workdir:    opts.Workdir,
				ConfigPath: opts.ConfigPath,
			})
		},
		newRunContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	})
	defer app.Close()

	program := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
	)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: run interface: %w", err)
	}
	return app.fatal
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
