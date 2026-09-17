package main

import (
	"fmt"
	"io"
	"os"

	"github.com/varavelio/rienda/internal/cli"
)

// main is the program entrypoint. Its only job is to delegate to run and
// translate the returned error into a non-zero exit code, keeping all real
// logic in a testable function.
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "rienda: "+err.Error())
		os.Exit(1)
	}
}

// run selects the mode of the program and delegates to it. Every mode lives
// in its own package; the entry point only dispatches.
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return nil
	}

	switch args[0] {
	case "run":
		//nolint:wrapcheck // the command produces user-facing messages.
		return cli.Run(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// usage prints the command overview.
func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Rienda runs AI agents defined as Markdown files.

Usage:
  rienda run -a <agent> -p <prompt> [flags]

Commands:
  run    Run an agent once and print its answer

Environment:
  RIENDA_CONFIG   Path of the configuration file when --config is not set

Run 'rienda run -h' for the flags of the run command.
`)
}
