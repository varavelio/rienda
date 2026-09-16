//go:build unix

package tool

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup places command in its own process group so that
// terminating it also terminates its descendants.
func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup terminates command and every process in its group.
func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}

	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if err != nil {
		return fmt.Errorf("tool: kill process group: %w", err)
	}
	return nil
}
