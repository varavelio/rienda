package tool

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/varavelio/rienda/internal/command"
	"github.com/varavelio/rienda/internal/workdir"

	"github.com/varavelio/rienda/internal/llm"
)

// Defaults shared by the built-in shell tools.
const (
	defaultShellName        = "shell"
	defaultShellInterpreter = "sh"
	defaultShellTimeout     = 2 * time.Minute
	defaultShellMaxTimeout  = 30 * time.Minute
	defaultShellMaxOutput   = 256 << 10
)

// defaultShellDescription describes the local shell tool to the model.
const defaultShellDescription = "Runs a single shell command on the host and returns its output. Every invocation starts a fresh process, so the working directory, exported variables and any other shell state do not persist between calls."

// defaultShellCommandDescription describes the command argument to the model.
const defaultShellCommandDescription = "Shell command to run. The command runs in a fresh process, so shell state does not persist between calls."

// defaultShellWorkdirDescription describes the workdir argument to the model.
const defaultShellWorkdirDescription = "Directory the command runs in. Relative paths resolve against the session workspace. Defaults to the session workspace."

// shellArguments is the decoded argument object of the built-in shell tools.
type shellArguments struct {
	Command   string `json:"command"`
	Workdir   string `json:"workdir"`
	TimeoutMS int    `json:"timeout_ms"`
}

// argvBuilder turns a command into the process argv that runs it. It receives
// the resolved working directory because some tools, like the devcontainer
// shell, need it to locate their execution target.
type argvBuilder func(ctx context.Context, workdir, command string) ([]string, error)

// shellConfig holds the validated settings of a shell tool instance.
type shellConfig struct {
	name           string
	description    string
	parameters     json.RawMessage
	buildArgv      argvBuilder
	defaultWorkdir string
	allowWorkdir   bool
	timeout        time.Duration
	maxTimeout     time.Duration
	maxOutput      int
}

// shellTool is the one-shot command runner shared by the built-in shell tools.
type shellTool struct {
	name           string
	description    string
	parameters     json.RawMessage
	buildArgv      argvBuilder
	defaultWorkdir string
	allowWorkdir   bool
	timeout        time.Duration
	maxTimeout     time.Duration
	maxOutput      int

	// sinkMu serializes sink calls because stdout and stderr are forwarded
	// from separate goroutines.
	sinkMu sync.Mutex
}

// newShellTool validates cfg and builds the shared command runner.
func newShellTool(cfg shellConfig) (*shellTool, error) {
	switch {
	case !ValidName(cfg.name):
		return nil, fmt.Errorf("tool: invalid tool name %q", cfg.name)
	case strings.TrimSpace(cfg.description) == "":
		return nil, fmt.Errorf("tool: tool %q requires a description", cfg.name)
	case len(cfg.parameters) == 0 || !json.Valid(cfg.parameters):
		return nil, fmt.Errorf("tool: tool %q requires a valid JSON Schema in Parameters", cfg.name)
	case cfg.buildArgv == nil:
		return nil, fmt.Errorf("tool: tool %q requires a command builder", cfg.name)
	case cfg.timeout <= 0:
		return nil, fmt.Errorf("tool: tool %q timeout must be positive", cfg.name)
	case cfg.maxTimeout < cfg.timeout:
		return nil, fmt.Errorf(
			"tool: tool %q max timeout must not be smaller than its timeout",
			cfg.name,
		)
	case cfg.maxOutput <= 0:
		return nil, fmt.Errorf("tool: tool %q max output must be positive", cfg.name)
	case cfg.defaultWorkdir != "" && !filepath.IsAbs(cfg.defaultWorkdir):
		return nil, fmt.Errorf("tool: tool %q default workdir must be an absolute path", cfg.name)
	}

	return &shellTool{
		name:           cfg.name,
		description:    cfg.description,
		parameters:     cfg.parameters,
		buildArgv:      cfg.buildArgv,
		defaultWorkdir: cfg.defaultWorkdir,
		allowWorkdir:   cfg.allowWorkdir,
		timeout:        cfg.timeout,
		maxTimeout:     cfg.maxTimeout,
		maxOutput:      cfg.maxOutput,
	}, nil
}

// Definition returns the tool definition shown to the model.
func (t *shellTool) Definition() llm.Tool {
	return llm.Tool{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

// Execute runs one command invocation.
func (t *shellTool) Execute(ctx context.Context, call Call, out Sink) (Result, error) {
	args, err := t.parseArguments(call.Arguments)
	if err != nil {
		return Result{}, err
	}

	workdir, err := t.resolveWorkdir(ctx, args.Workdir)
	if err != nil {
		return Result{}, err
	}

	argv, err := t.buildArgv(ctx, workdir, args.Command)
	if err != nil {
		return Result{}, err
	}

	timeout := t.effectiveTimeout(args.TimeoutMS)
	stdout := newLimitedBuffer(t.maxOutput)
	stderr := newLimitedBuffer(t.maxOutput)
	stream := &forwardStream{tool: t, sink: out, stdout: stdout, stderr: stderr}
	outcome, err := command.Run(ctx, command.Request{
		Argv: argv, Dir: workdir, Timeout: timeout, MaxOutput: t.maxOutput, Stream: stream,
	})
	if err != nil {
		return Result{}, fmt.Errorf("tool: %w", err)
	}
	return t.commandResult(ctx, outcome, timeout)
}

// forwardStream forwards command output to the sink and keeps a capped copy.
type forwardStream struct {
	tool           *shellTool
	sink           Sink
	stdout, stderr *limitedBuffer
}

// Emit reports one chunk of output.
func (s *forwardStream) Emit(name string, data []byte) {
	var buf *limitedBuffer
	var stream Stream
	if name == "stderr" {
		buf, stream = s.stderr, StreamStderr
	} else {
		buf, stream = s.stdout, StreamStdout
	}
	_, _ = buf.Write(data)
	if s.sink != nil {
		s.tool.sinkMu.Lock()
		s.sink.Emit(stream, data)
		s.tool.sinkMu.Unlock()
	}
}

// parseArguments decodes and validates the shared shell arguments. Tools that
// do not accept a working directory reject the argument instead of ignoring
// it, so the model learns the interface.
func (t *shellTool) parseArguments(raw json.RawMessage) (shellArguments, error) {
	var args shellArguments
	if err := decodeArguments(raw, &args); err != nil {
		return shellArguments{}, err
	}

	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return shellArguments{}, errors.New(`tool: "command" is required`)
	}
	if !t.allowWorkdir && args.Workdir != "" {
		return shellArguments{}, fmt.Errorf(
			"tool: %s does not accept a workdir argument",
			t.name,
		)
	}
	if args.TimeoutMS < 0 {
		return shellArguments{}, errors.New(`tool: "timeout_ms" must not be negative`)
	}
	return args, nil
}

// resolveWorkdir returns the directory an invocation runs in. An explicit
// request wins over the context directory, which wins over the configured
// directory and the process working directory.
func (t *shellTool) resolveWorkdir(ctx context.Context, requested string) (string, error) {
	base, err := workdir.Base(ctx, t.defaultWorkdir)
	if err != nil {
		if _, ok := errors.AsType[*workdir.NotDirError](err); ok {
			return "", fmt.Errorf(
				"tool: working directory %s is not a directory",
				requestedOrBase(requested, base),
			)
		}
		return "", fmt.Errorf(
			"tool: working directory %s: %w",
			requestedOrBase(requested, base),
			err,
		)
	}
	resolved := workdir.Resolve(base, requested)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("tool: working directory %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("tool: working directory %s is not a directory", resolved)
	}
	return resolved, nil
}

func requestedOrBase(requested, base string) string {
	if requested != "" {
		return requested
	}
	return base
}

// effectiveTimeout resolves the timeout of an invocation, capping requests
// above the maximum and falling back to the default.
func (t *shellTool) effectiveTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return t.timeout
	}
	requested := time.Duration(timeoutMS) * time.Millisecond
	if requested <= 0 || requested > t.maxTimeout {
		return t.maxTimeout
	}
	return requested
}

// commandResult renders the outcome of a finished command invocation.
func (t *shellTool) commandResult(
	ctx context.Context,
	outcome command.Outcome,
	timeout time.Duration,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("tool: command canceled: %w", err)
	}
	co := commandOutcome{
		stdout:     newLimitedBuffer(t.maxOutput),
		stderr:     newLimitedBuffer(t.maxOutput),
		exitCode:   outcome.ExitCode,
		timedOut:   outcome.TimedOut,
		timeout:    timeout,
		maxOutput:  t.maxOutput,
		background: outcome.Background,
	}
	_, _ = co.stdout.Write([]byte(outcome.Stdout))
	_, _ = co.stderr.Write([]byte(outcome.Stderr))
	if outcome.Truncated {
		co.stdout.cut = true
	}
	return renderResult(co), nil
}

// commandOutcome carries everything needed to render a command result.
type commandOutcome struct {
	stdout     *limitedBuffer
	stderr     *limitedBuffer
	exitCode   int
	timedOut   bool
	timeout    time.Duration
	background bool
	maxOutput  int
}

// renderResult turns a command outcome into the tool result read by the model.
func renderResult(outcome commandOutcome) Result {
	var sections []string
	if stdout := strings.TrimRight(string(outcome.stdout.bytes()), "\r\n"); stdout != "" {
		if outcome.stdout.truncated() {
			stdout += "\n[stdout truncated]"
		}
		sections = append(sections, stdout)
	}
	if stderr := strings.TrimRight(string(outcome.stderr.bytes()), "\r\n"); stderr != "" {
		if outcome.stderr.truncated() {
			stderr += "\n[stderr truncated]"
		}
		sections = append(sections, "stderr:\n"+stderr)
	}

	switch {
	case outcome.exitCode > 0:
		sections = append(sections, "command exited with code "+strconv.Itoa(outcome.exitCode))
	case outcome.exitCode < 0:
		sections = append(sections, "command terminated by a signal")
	}
	if outcome.timedOut {
		sections = append(sections, "command timed out after "+outcome.timeout.String())
	}
	if outcome.background {
		sections = append(
			sections,
			"command left background output open, its output may be incomplete",
		)
	}
	if len(sections) == 0 {
		sections = append(sections, "(no output)")
	}

	text, truncated := truncateUTF8(strings.Join(sections, "\n"), outcome.maxOutput)
	if truncated {
		text += "\n[output truncated]"
	}
	if outcome.exitCode != 0 || outcome.timedOut || outcome.background {
		return ErrorResult(text)
	}
	return TextResult(text)
}

// limitedBuffer accumulates output up to a byte budget.
type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	cut       bool
}

// newLimitedBuffer returns a buffer that keeps at most maxBytes.
func newLimitedBuffer(maxBytes int) *limitedBuffer {
	return &limitedBuffer{remaining: maxBytes}
}

// Write appends p while the budget lasts and reports every byte as consumed,
// so writers like io.Copy keep producing output after the budget is exhausted.
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

// bytes returns the accumulated output.
func (b *limitedBuffer) bytes() []byte {
	return b.buffer.Bytes()
}

// truncated reports whether output was discarded.
func (b *limitedBuffer) truncated() bool {
	return b.cut
}

// decodeArguments decodes a tool argument object, rejecting unknown fields and
// trailing data so model mistakes surface as clear errors.
func decodeArguments(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return errors.New("tool: arguments are required")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("tool: invalid arguments: %w", err)
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("tool: invalid arguments: unexpected trailing data")
	}
	return nil
}

// truncateUTF8 cuts s to at most maxBytes without splitting a rune and reports
// whether it cut anything.
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

// shellParametersJSON builds the argument schema shared by the built-in shell
// tools. The workdir property is included only when its description is not
// empty, because some tools do not accept a working directory.
func shellParametersJSON(
	command, workdir string,
	defaultTimeout, maxTimeout time.Duration,
) string {
	timeout := "Maximum time in milliseconds the command may run. Defaults to " +
		strconv.FormatInt(defaultTimeout.Milliseconds(), 10) +
		" and requests above " + strconv.FormatInt(maxTimeout.Milliseconds(), 10) +
		" are capped."

	workdirProperty := ""
	if workdir != "" {
		workdirProperty = fmt.Sprintf(`,
    "workdir": {
      "type": "string",
      "description": %q
    }`, workdir)
	}

	return fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": %q
    }%s,
    "timeout_ms": {
      "type": "integer",
      "description": %q
    }
  },
  "required": ["command"],
  "additionalProperties": false
}`, command, workdirProperty, timeout)
}

// Shell is the built-in tool that runs one-shot shell commands on the host.
//
// Every invocation starts a fresh process, so the working directory, exported
// variables and any other shell state do not persist between calls. Output is
// streamed to the sink while the command runs and capped for the model result.
type Shell struct {
	core *shellTool
}

// ShellOptions configures a Shell tool. The zero value yields a tool named
// "shell" that runs commands with sh.
type ShellOptions struct {
	// Name overrides the tool name.
	Name string

	// Description overrides the tool description shown to the model.
	Description string

	// Interpreter is the shell binary that runs the commands. It defaults to
	// "sh".
	Interpreter string

	// Workdir is the default working directory of every invocation. It must be
	// absolute. When empty, invocations fall back to the context working
	// directory and then to the process working directory.
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

// NewShell builds a Shell tool from options.
func NewShell(options ShellOptions) (*Shell, error) {
	interpreter := cmp.Or(options.Interpreter, defaultShellInterpreter)
	timeout := cmp.Or(options.Timeout, defaultShellTimeout)
	maxTimeout := cmp.Or(options.MaxTimeout, defaultShellMaxTimeout)

	core, err := newShellTool(shellConfig{
		name:        cmp.Or(options.Name, defaultShellName),
		description: cmp.Or(options.Description, defaultShellDescription),
		parameters: json.RawMessage(shellParametersJSON(
			defaultShellCommandDescription,
			defaultShellWorkdirDescription,
			timeout,
			maxTimeout,
		)),
		buildArgv: func(_ context.Context, _, command string) ([]string, error) {
			return []string{interpreter, "-c", command}, nil
		},
		defaultWorkdir: options.Workdir,
		allowWorkdir:   true,
		timeout:        timeout,
		maxTimeout:     maxTimeout,
		maxOutput:      cmp.Or(options.MaxOutputBytes, defaultShellMaxOutput),
	})
	if err != nil {
		return nil, err
	}
	return &Shell{core: core}, nil
}

// Definition returns the tool definition shown to the model.
func (s *Shell) Definition() llm.Tool {
	return s.core.Definition()
}

// Execute runs one shell command invocation.
func (s *Shell) Execute(ctx context.Context, call Call, out Sink) (Result, error) {
	return s.core.Execute(ctx, call, out)
}
