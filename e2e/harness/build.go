//go:build e2e

package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// binaryEnvVar names the environment variable that overrides the path of the
// compiled binary, so a developer iterating locally can reuse the binary built
// by the build task instead of compiling one per run.
const binaryEnvVar = "RIENDA_E2E_BINARY"

// binaryPath is the compiled rienda executable shared by the whole suite. It
// is empty until Main builds it.
var binaryPath string

// Main compiles the rienda executable once, runs the suite against it, and
// returns the exit code the test binary must use. It is the entry point of
// TestMain, so the compilation is paid a single time per run.
func Main(m *testing.M) int {
	if override := strings.TrimSpace(os.Getenv(binaryEnvVar)); override != "" {
		binaryPath = override
		return m.Run()
	}

	dir, err := os.MkdirTemp("", "rienda-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: create temporary directory:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "rienda")
	//nolint:gosec // the tool and its arguments are fixed by the suite.
	build := exec.CommandContext(context.Background(), "go", "build", "-o", path, "./cmd/rienda/.")
	build.Dir = moduleRoot()
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build binary: %v\n%s", err, output)
		return 1
	}

	binaryPath = path
	return m.Run()
}

// moduleRoot returns the root of the module, derived from the location of this
// source file so the suite compiles the project whatever the working directory
// of the test binary is.
func moduleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// BinaryPath returns the path of the compiled rienda executable of the suite,
// failing the test when Main did not compile it.
func BinaryPath(t *testing.T) string {
	t.Helper()
	if binaryPath == "" {
		t.Fatal("harness: the suite did not compile the rienda binary, run it through harness.Main")
	}
	return binaryPath
}

// Result is the outcome of one completed rienda invocation.
type Result struct {
	// Stdout holds the complete standard output of the process.
	Stdout string
	// Stderr holds the complete standard error of the process.
	Stderr string
	// Code is the exit code of the process.
	Code int
}

// RequireSuccess fails the test unless the invocation exited successfully,
// reporting its captured output together with the failure.
func (r *Result) RequireSuccess(t *testing.T) *Result {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf(
			"exit code = %d, want 0\nstandard output:\n%s\nstandard error:\n%s",
			r.Code,
			r.Stdout,
			r.Stderr,
		)
	}
	return r
}

// SessionID returns the identifier of the session the invocation reported on
// standard error, failing the test when it reported none.
func (r *Result) SessionID(t *testing.T) string {
	t.Helper()
	for line := range strings.SplitSeq(r.Stderr, "\n") {
		if id, found := strings.CutPrefix(line, "session: "); found {
			return strings.TrimSpace(id)
		}
	}
	t.Fatalf("the invocation reported no session id on standard error:\n%s", r.Stderr)
	return ""
}

// Run executes the compiled binary with the given environment additions and
// command-line arguments and waits for it to exit. The additions override the
// inherited environment, so a test can point HOME at its own directory.
func Run(t *testing.T, env []string, args ...string) *Result {
	t.Helper()
	return execute(t, "", os.Environ(), env, args...)
}

// execute runs the binary with the given working directory and environment and
// waits for it to exit, failing the test when the process cannot start.
func execute(t *testing.T, dir string, base, additions []string, args ...string) *Result {
	t.Helper()

	//nolint:gosec // the binary under test and its arguments are test-controlled.
	cmd := exec.CommandContext(t.Context(), BinaryPath(t), args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(base, additions)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	result := &Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("run rienda %v: %v", args, err)
		}
		result.Code = exitErr.ExitCode()
	}
	return result
}

// mergeEnv returns base with the additions applied. An addition replaces every
// inherited entry with the same variable name, so the child process never sees
// a duplicated variable.
func mergeEnv(base, additions []string) []string {
	overridden := make(map[string]bool, len(additions))
	for _, entry := range additions {
		overridden[envName(entry)] = true
	}

	merged := make([]string, 0, len(base)+len(additions))
	for _, entry := range base {
		if !overridden[envName(entry)] {
			merged = append(merged, entry)
		}
	}
	return append(merged, additions...)
}

// envName returns the variable name of an environment entry.
func envName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	return name
}
