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

	"github.com/varavelio/rienda/internal/catalog"
	"github.com/varavelio/rienda/internal/harness"
)

// Run executes the run command: it prepares one session, either a new one
// owned by an agent or a stored one it continues, sends one prompt to the
// agent and streams the result. An agent or a model named together with a
// stored session selects what that session runs from now on, which is how a
// non-interactive run changes the agent or the model of a conversation. Assistant text goes to stdout, tool activity
// and notices go to stderr.
func Run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { runUsage(stderr) }

	var agentID, modelRef, sessionID, prompt, configPath, workdir string
	flags.StringVar(&agentID, "agent", "", "agent definition to run")
	flags.StringVar(&agentID, "a", "", "shorthand for --agent")
	flags.StringVar(&modelRef, "model", "", "model reference to run")
	flags.StringVar(&modelRef, "m", "", "shorthand for --model")
	flags.StringVar(&sessionID, "session", "", "session to continue")
	flags.StringVar(&sessionID, "s", "", "shorthand for --session")
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
	if strings.TrimSpace(agentID) == "" && strings.TrimSpace(sessionID) == "" {
		return errors.New("an agent or a session is required: use --agent or --session")
	}
	if strings.TrimSpace(prompt) == "" {
		return errors.New("a prompt is required: use --prompt")
	}

	// Cancellation reaches the engine, which closes the run cleanly and
	// leaves the session ready to resume.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The catalog keeps the model facts the context measurement resolves its
	// window from current while the run lasts, and stops with the context.
	if facts, err := catalog.New(catalog.Options{}); err == nil {
		go facts.Run(ctx)
	}

	session, err := harness.Prepare(ctx, harness.Options{
		AgentID:    agentID,
		ModelRef:   modelRef,
		SessionID:  sessionID,
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
  rienda run -s <session> -p <prompt> [flags]

Flags:
  -a, --agent     Agent definition to run
  -m, --model     Model reference to run, in provider/model form
  -s, --session   Session to continue, named by the identifier it reported
                  With -a or -m it switches what that session runs
  -p, --prompt    Prompt to send to the agent (required)
      --config    Path of the configuration file
  -C, --workdir   Directory the session runs in
`)
}
