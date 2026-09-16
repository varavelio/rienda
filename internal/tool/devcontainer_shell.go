package tool

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/varavelio/rienda/internal/llm"
)

// Defaults of the devcontainer shell tool.
const (
	defaultDevcontainerName   = "dc-shell"
	defaultDevcontainerBinary = "devcontainer"
)

// defaultDevcontainerDescription describes the devcontainer shell to the model.
const defaultDevcontainerDescription = "Runs a single shell command inside the project's dev container through the devcontainer CLI and returns its output. The dev container is located by searching the working directory and its parents for a .devcontainer/devcontainer.json or .devcontainer.json file. Every invocation starts a fresh process, so shell state does not persist between calls."

// defaultDevcontainerCommandDescription describes the command argument of the
// devcontainer shell to the model.
const defaultDevcontainerCommandDescription = "Shell command to run inside the dev container. The command runs in a fresh process inside the container, so shell state does not persist between calls."

// defaultDevcontainerWorkdirDescription describes the workdir argument of the
// devcontainer shell to the model.
const defaultDevcontainerWorkdirDescription = "Host directory used to locate the dev container workspace. Relative paths resolve against the session workspace. Defaults to the session workspace."

// DevcontainerShell is the built-in tool that runs one-shot shell commands
// inside the project's dev container through the devcontainer CLI.
//
// Every invocation starts a fresh process through devcontainer exec, so shell
// state does not persist between calls. Output is streamed to the sink while
// the command runs and capped for the model result.
type DevcontainerShell struct {
	core *shellTool
}

// DevcontainerShellOptions configures a DevcontainerShell tool. The zero value
// yields a tool named "dc-shell" that uses the devcontainer binary.
type DevcontainerShellOptions struct {
	// Name overrides the tool name.
	Name string

	// Description overrides the tool description shown to the model.
	Description string

	// Binary is the devcontainer CLI binary that runs the commands. It
	// defaults to "devcontainer".
	Binary string

	// Interpreter is the shell binary used inside the container. It defaults
	// to "sh".
	Interpreter string

	// Workdir is the default host working directory used to locate the dev
	// container. It must be absolute. When empty, invocations fall back to the
	// context working directory and then to the process working directory.
	Workdir string

	// Timeout is the default maximum duration of an invocation. It defaults to
	// two minutes.
	Timeout time.Duration

	// MaxTimeout caps the timeout_ms argument of every invocation. It defaults
	// to thirty minutes.
	MaxTimeout time.Duration

	// MaxOutputBytes caps the output kept for the model. Output streamed to
	// the sink is never capped. It defaults to 256 KiB.
	MaxOutputBytes int
}

// NewDevcontainerShell builds a DevcontainerShell tool from options.
func NewDevcontainerShell(options DevcontainerShellOptions) (*DevcontainerShell, error) {
	binary := cmp.Or(options.Binary, defaultDevcontainerBinary)
	interpreter := cmp.Or(options.Interpreter, defaultShellInterpreter)
	timeout := cmp.Or(options.Timeout, defaultShellTimeout)
	maxTimeout := cmp.Or(options.MaxTimeout, defaultShellMaxTimeout)

	core, err := newShellTool(shellConfig{
		name:        cmp.Or(options.Name, defaultDevcontainerName),
		description: cmp.Or(options.Description, defaultDevcontainerDescription),
		parameters: json.RawMessage(shellParametersJSON(
			defaultDevcontainerCommandDescription,
			defaultDevcontainerWorkdirDescription,
			timeout,
			maxTimeout,
		)),
		buildArgv: func(_ context.Context, workdir, command string) ([]string, error) {
			workspace, err := findDevcontainerWorkspace(workdir)
			if err != nil {
				return nil, err
			}
			return []string{
				binary,
				"exec",
				"--workspace-folder",
				workspace,
				interpreter,
				"-c",
				command,
			}, nil
		},
		defaultWorkdir: options.Workdir,
		timeout:        timeout,
		maxTimeout:     maxTimeout,
		maxOutput:      cmp.Or(options.MaxOutputBytes, defaultShellMaxOutput),
	})
	if err != nil {
		return nil, err
	}
	return &DevcontainerShell{core: core}, nil
}

// Definition returns the tool definition shown to the model.
func (d *DevcontainerShell) Definition() llm.Tool {
	return d.core.Definition()
}

// Execute runs one devcontainer shell command invocation.
func (d *DevcontainerShell) Execute(ctx context.Context, call Call, out Sink) (Result, error) {
	return d.core.Execute(ctx, call, out)
}

// findDevcontainerWorkspace locates the workspace folder of the dev container
// that contains workdir, searching workdir and its ancestors for a
// devcontainer configuration file.
func findDevcontainerWorkspace(workdir string) (string, error) {
	dir := workdir
	for {
		for _, config := range []string{
			filepath.Join(dir, ".devcontainer", "devcontainer.json"),
			filepath.Join(dir, ".devcontainer.json"),
		} {
			if info, err := os.Stat(config); err == nil && info.Mode().IsRegular() {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"tool: no devcontainer configuration found in %s or any parent directory",
				workdir,
			)
		}
		dir = parent
	}
}
