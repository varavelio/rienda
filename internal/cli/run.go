package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/varavelio/rienda/internal/harness"
)

// Run executes the run command: it prepares one session, sends one prompt to
// the agent and streams the result. Assistant text goes to stdout, tool
// activity and notices go to stderr.
func Run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { runUsage(stderr) }

	var agentID, prompt, configPath, workdir string
	flags.StringVar(&agentID, "agent", "", "agent definition to run (required)")
	flags.StringVar(&agentID, "a", "", "shorthand for --agent")
	flags.StringVar(&prompt, "prompt", "", "prompt to send to the agent (required)")
	flags.StringVar(&prompt, "p", "", "shorthand for --prompt")
	flags.StringVar(&configPath, "config", "", "path of the configuration file")
	flags.StringVar(&workdir, "workdir", "", "directory the session runs in")
	flags.StringVar(&workdir, "C", "", "shorthand for --workdir")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("run: parse flags: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(agentID) == "" {
		return errors.New("an agent is required: use --agent")
	}
	if strings.TrimSpace(prompt) == "" {
		return errors.New("a prompt is required: use --prompt")
	}

	// Cancellation reaches the engine, which closes the run cleanly and
	// leaves the session ready to resume.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	session, err := harness.Prepare(ctx, harness.Options{
		AgentID:    agentID,
		Workdir:    workdir,
		ConfigPath: configPath,
	})
	if err != nil {
		return fmt.Errorf("run: prepare session: %w", err)
	}
	defer func() { _ = session.Close() }()

	_, _ = fmt.Fprintf(stderr, "session: %s\n", session.ID())
	return render(session.Run(ctx, prompt), stdout, stderr)
}

// runUsage prints the flags of the run command.
func runUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  rienda run -a <agent> -p <prompt> [flags]

Flags:
  -a, --agent     Agent definition to run (required)
  -p, --prompt    Prompt to send to the agent (required)
      --config    Path of the configuration file
  -C, --workdir   Directory the session runs in
`)
}
