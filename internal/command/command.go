package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"
)

// waitDelay bounds how long a finished invocation may keep its output streams
// open, for example through a background process, before the runner stops
// waiting for it.
const waitDelay = 5 * time.Second

// Request describes one command invocation.
type Request struct {
	// Argv is the program and its arguments. It is required.
	Argv []string

	// Dir is the working directory. It is required.
	Dir string

	// Timeout bounds the invocation. Zero means none.
	Timeout time.Duration

	// MaxOutput caps the bytes kept per stream.
	MaxOutput int

	// Stream receives produced output while the command runs. It may be nil.
	Stream Stream

	// Env adds variables on top of the inherited environment.
	Env map[string]string
}

// Stream receives command output while it runs.
type Stream interface {
	// Emit reports a chunk of output produced on name ("stdout"/"stderr").
	Emit(name string, data []byte)
}

// Outcome is the result of one command invocation.
type Outcome struct {
	// ExitCode is the exit status, -1 when a signal terminated the process.
	ExitCode int

	// Stdout is the capped standard output.
	Stdout string

	// Stderr is the capped standard error.
	Stderr string

	// TimedOut reports the deadline fired.
	TimedOut bool

	// Background reports the process left output streams open.
	Background bool

	// Truncated reports at least one stream hit the cap.
	Truncated bool
}

// Run executes one command and returns its outcome.
func Run(ctx context.Context, req Request) (Outcome, error) {
	if len(req.Argv) == 0 {
		return Outcome{}, errors.New("command: argv is required")
	}
	if req.Dir == "" {
		return Outcome{}, errors.New("command: dir is required")
	}
	maxOut := req.MaxOutput
	if maxOut <= 0 {
		maxOut = 256 << 10
	}
	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	binary, extra := req.Argv[0], req.Argv[1:]
	cmd := exec.CommandContext(runCtx, binary, extra...) //nolint:gosec // caller supplies argv.
	cmd.Dir = req.Dir
	cmd.WaitDelay = waitDelay
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	if len(req.Env) > 0 {
		env := make([]string, 0, len(req.Env))
		for k, v := range req.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = append(cmd.Environ(), env...)
	}
	stdout := newLimitedBuffer(maxOut)
	stderr := newLimitedBuffer(maxOut)
	var mu sync.Mutex
	emit := func(name string, p []byte) {
		if req.Stream == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		req.Stream.Emit(name, p)
	}
	cmd.Stdout = io.MultiWriter(stdout, writerFunc(func(p []byte) (int, error) {
		emit("stdout", p)
		return len(p), nil
	}))
	cmd.Stderr = io.MultiWriter(stderr, writerFunc(func(p []byte) (int, error) {
		emit("stderr", p)
		return len(p), nil
	}))
	if err := cmd.Start(); err != nil {
		return Outcome{}, fmt.Errorf("command: start command: %w", err)
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return Outcome{}, fmt.Errorf("command: command canceled: %w", err)
	}
	out := Outcome{
		Stdout:    string(stdout.bytes()),
		Stderr:    string(stderr.bytes()),
		TimedOut:  runCtx.Err() != nil,
		Truncated: stdout.truncated() || stderr.truncated(),
	}
	if state := cmd.ProcessState; state != nil {
		out.ExitCode = state.ExitCode()
	}
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
	case out.TimedOut:
		out.ExitCode = 0
	case errors.As(waitErr, &exitErr):
		out.ExitCode = exitErr.ExitCode()
	case errors.Is(waitErr, exec.ErrWaitDelay):
		out.Background = true
	default:
		return Outcome{}, fmt.Errorf("command: wait for command: %w", waitErr)
	}
	out.Stdout, _ = truncateUTF8(out.Stdout, maxOut)
	out.Stderr, _ = truncateUTF8(out.Stderr, maxOut)
	_ = bytes.MinRead
	return out, nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// truncateUTF8 cuts s to at most maxBytes without splitting a rune.
func truncateUTF8(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// limitedBuffer accumulates output up to a byte budget.
type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	cut       bool
}

func newLimitedBuffer(maxBytes int) *limitedBuffer { return &limitedBuffer{remaining: maxBytes} }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.remaining {
		p = p[:b.remaining]
		b.cut = true
	}
	b.remaining -= len(p)
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *limitedBuffer) bytes() []byte   { return b.buffer.Bytes() }
func (b *limitedBuffer) truncated() bool { return b.cut }
