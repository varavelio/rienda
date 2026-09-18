//go:build e2e

package harness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"
)

// Process is one rienda invocation running in the background. The tests that
// act while a run is in flight, like the ones that interrupt it, start a
// process instead of waiting for a result.
type Process struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
	result *Result
}

// Start launches the compiled binary with the environment of the harness and
// returns the running process without waiting for it to exit.
func (h *Harness) Start(t *testing.T, args ...string) *Process {
	t.Helper()

	//nolint:gosec // the binary under test and its arguments are test-controlled.
	cmd := exec.CommandContext(t.Context(), BinaryPath(t), args...)
	cmd.Dir = h.workdir
	cmd.Env = mergeEnv(os.Environ(), h.environment(nil))

	process := &Process{cmd: cmd}
	cmd.Stdout, cmd.Stderr = &process.stdout, &process.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start rienda %v: %v", args, err)
	}
	return process
}

// Interrupt delivers an interrupt signal to the process, exactly like the
// terminal does when the user presses ctrl+c.
func (p *Process) Interrupt(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt rienda: %v", err)
	}
}

// Wait blocks until the process exits and returns its outcome. Calling it more
// than once returns the same result without waiting again.
func (p *Process) Wait(t *testing.T) *Result {
	t.Helper()
	if p.result != nil {
		return p.result
	}

	err := p.cmd.Wait()
	p.result = &Result{Stdout: p.stdout.String(), Stderr: p.stderr.String()}
	if err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("wait for rienda: %v", err)
		}
		p.result.Code = exitErr.ExitCode()
	}
	return p.result
}
