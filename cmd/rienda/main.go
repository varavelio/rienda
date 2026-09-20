package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/varavelio/rienda/internal/cli"
	"github.com/varavelio/rienda/internal/tui"
	"github.com/varavelio/rienda/internal/version"
)

// main is the program entrypoint. Its only job is to delegate to run and
// translate the returned error into a non-zero exit code, keeping all real
// logic in a testable function.
func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "rienda: "+err.Error())
		os.Exit(1)
	}
}

// run selects the mode of the program and delegates to it. Every mode lives
// in its own package; the entry point only dispatches.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		//nolint:wrapcheck // the interface produces user-facing messages.
		return tui.Run(args, stdin, stdout)
	}

	switch args[0] {
	case "run":
		//nolint:wrapcheck // the command produces user-facing messages.
		return cli.Run(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	case "version", "-v", "--version":
		_, _ = fmt.Fprintln(stdout, version.Detailed())
		return nil
	}

	if !strings.HasPrefix(args[0], "-") {
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}

	//nolint:wrapcheck // the interface produces user-facing messages.
	return tui.Run(args, stdin, stdout)
}

// usage prints the program overview, headed by the build version.
func usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `Rienda %s

Runs AI agents defined as Markdown files.

Usage:
  rienda [flags]                      Open the interactive interface
  rienda run -a <agent> -p <prompt>   Run an agent once and print its answer

Commands:
  run    Run an agent once and print its answer

Global flags:
  -h, --help      Print this overview
  -v, --version   Print the version and the build metadata

Flags of the interactive interface:
  -a, --agent     Agent to run, skipping the start list and the agent picker
  -C, --workdir   Directory sessions run in
      --config    Path of the configuration file

Environment:
  RIENDA_CONFIG   Path of the configuration file when --config is not set
`, version.Number())
}
