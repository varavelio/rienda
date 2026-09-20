package tui

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// errHelp reports that the caller asked for the usage of the interface.
var errHelp = errors.New("tui: help requested")

// options holds the parsed command line options of the interactive interface.
type options struct {
	// AgentID preselects the agent to run, skipping the start list and the
	// agent picker.
	AgentID string

	// Workdir is the directory sessions run in.
	Workdir string

	// ConfigPath overrides the path of the configuration file.
	ConfigPath string
}

// parseOptions parses the arguments of the interactive interface. It returns
// errHelp when the caller asked for help.
func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("rienda", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	var parsed options
	flags.StringVar(&parsed.AgentID, "agent", "", "agent to run")
	flags.StringVar(&parsed.AgentID, "a", "", "shorthand for --agent")
	flags.StringVar(&parsed.Workdir, "workdir", "", "directory sessions run in")
	flags.StringVar(&parsed.Workdir, "C", "", "shorthand for --workdir")
	flags.StringVar(&parsed.ConfigPath, "config", "", "path of the configuration file")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, errHelp
		}
		return options{}, fmt.Errorf("tui: parse flags: %w", err)
	}
	if flags.NArg() > 0 {
		return options{}, fmt.Errorf("tui: unexpected argument %q", flags.Arg(0))
	}

	parsed.AgentID = strings.TrimSpace(parsed.AgentID)
	parsed.Workdir = strings.TrimSpace(parsed.Workdir)
	parsed.ConfigPath = strings.TrimSpace(parsed.ConfigPath)
	return parsed, nil
}

// usage prints the flags of the interactive interface.
func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  rienda [flags]

Flags:
  -a, --agent     Agent to run, skipping the start list and the agent picker
  -C, --workdir   Directory sessions run in
      --config    Path of the configuration file
`)
}
