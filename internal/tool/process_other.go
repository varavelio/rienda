//go:build !unix

package tool

import (
	"fmt"
	"os"
	"os/exec"
)

// configureProcessGroup is a no-op on platforms without POSIX process groups.
func configureProcessGroup(_ *exec.Cmd) {}

// killProcessGroup terminates command.
func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	if err := command.Process.Kill(); err != nil {
		return fmt.Errorf("tool: kill command: %w", err)
	}
	return nil
}
