package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// recordedChunk is a single chunk captured by recordingSink.
type recordedChunk struct {
	stream Stream
	data   string
}

// recordingSink captures the chunks emitted by a tool.
type recordingSink struct {
	mu     sync.Mutex
	chunks []recordedChunk
}

// Emit records a chunk.
func (s *recordingSink) Emit(stream Stream, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, recordedChunk{stream: stream, data: string(data)})
}

// text returns every chunk concatenated in order.
func (s *recordingSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var builder strings.Builder
	for _, chunk := range s.chunks {
		builder.WriteString(chunk.data)
	}
	return builder.String()
}

// newTestShell builds a shell tool with a temporary default workdir.
func newTestShell(t *testing.T, options ShellOptions) *Shell {
	t.Helper()

	if options.Workdir == "" {
		options.Workdir = t.TempDir()
	}
	shell, err := NewShell(options)
	require.NoError(t, err)
	return shell
}

// runShell executes one shell invocation and returns its result and output sink.
func runShell(
	t *testing.T,
	shell *Shell,
	ctx context.Context,
	arguments string,
) (Result, *recordingSink) {
	t.Helper()

	sink := &recordingSink{}
	result, err := shell.Execute(ctx, Call{
		ID:        "call_1",
		Name:      shell.Definition().Name,
		Arguments: json.RawMessage(arguments),
	}, sink)
	require.NoError(t, err)
	return result, sink
}

// resultText returns the text carried by a result.
func resultText(t *testing.T, result Result) string {
	t.Helper()

	require.Len(t, result.Blocks, 1)
	require.Equal(t, llm.BlockText, result.Blocks[0].Type)
	return result.Blocks[0].Text
}

// physicalPath resolves symlinks, since pwd -P reports the physical path.
func physicalPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}

// pwdArguments builds a pwd invocation in the given directory.
func pwdArguments(t *testing.T, workdir string) string {
	t.Helper()

	return fmt.Sprintf(`{"command":"pwd -P","workdir":%q}`, workdir)
}

// TestTruncateUTF8 verifies byte-bounded truncation that never splits a rune.
func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
		wantCut  bool
	}{
		{name: "shorter than the limit", input: "hello", maxBytes: 10, want: "hello"},
		{name: "exactly the limit", input: "hello", maxBytes: 5, want: "hello"},
		{name: "cuts ascii", input: "hello", maxBytes: 2, want: "he", wantCut: true},
		{name: "keeps whole runes", input: "你好", maxBytes: 4, want: "你", wantCut: true},
		{name: "cuts at a rune boundary", input: "你好", maxBytes: 3, want: "你", wantCut: true},
		{name: "cuts to nothing", input: "你", maxBytes: 1, want: "", wantCut: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, cut := truncateUTF8(test.input, test.maxBytes)

			require.Equal(t, test.want, got)
			require.Equal(t, test.wantCut, cut)
		})
	}
}

// TestNewShell verifies option handling and validation.
func TestNewShell(t *testing.T) {
	t.Run("applies defaults", func(t *testing.T) {
		shell, err := NewShell(ShellOptions{})
		require.NoError(t, err)

		definition := shell.Definition()
		require.Equal(t, "shell", definition.Name)
		require.NotEmpty(t, definition.Description)
		require.True(t, json.Valid(definition.Parameters))
		require.Contains(t, string(definition.Parameters), `"command"`)
	})

	t.Run("rejects invalid options", func(t *testing.T) {
		tests := []struct {
			name    string
			options ShellOptions
			wantErr string
		}{
			{
				name:    "invalid name",
				options: ShellOptions{Name: "bad name"},
				wantErr: "invalid tool name",
			},
			{
				name:    "blank description",
				options: ShellOptions{Description: "  "},
				wantErr: "description",
			},
			{
				name:    "relative workdir",
				options: ShellOptions{Workdir: "relative"},
				wantErr: "absolute",
			},
			{
				name:    "max timeout below timeout",
				options: ShellOptions{Timeout: time.Minute, MaxTimeout: time.Second},
				wantErr: "max timeout",
			},
			{
				name:    "negative max output",
				options: ShellOptions{MaxOutputBytes: -1},
				wantErr: "max output",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, err := NewShell(test.options)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})
}

// TestShellExecute verifies command execution behavior.
func TestShellExecute(t *testing.T) {
	t.Run("runs a command and returns its output", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{})

		result, sink := runShell(t, shell, t.Context(), `{"command":"echo hello"}`)

		require.False(t, result.IsError)
		require.Equal(t, "hello", resultText(t, result))
		require.Contains(t, sink.text(), "hello")
	})

	t.Run("labels stderr output", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{})

		result, _ := runShell(t, shell, t.Context(), `{"command":"echo out; echo err 1>&2"}`)

		require.False(t, result.IsError)
		require.Equal(t, "out\nstderr:\nerr", resultText(t, result))
	})

	t.Run("reports commands without output", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{})

		result, _ := runShell(t, shell, t.Context(), `{"command":"true"}`)

		require.False(t, result.IsError)
		require.Equal(t, "(no output)", resultText(t, result))
	})

	t.Run("marks a non-zero exit as an error", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{})

		result, _ := runShell(t, shell, t.Context(), `{"command":"echo failing; exit 3"}`)

		require.True(t, result.IsError)
		require.Equal(t, "failing\ncommand exited with code 3", resultText(t, result))
	})

	t.Run("runs in the requested workdir", func(t *testing.T) {
		workdir := t.TempDir()
		shell := newTestShell(t, ShellOptions{})

		result, _ := runShell(t, shell, t.Context(), pwdArguments(t, workdir))

		require.Equal(t, physicalPath(t, workdir), resultText(t, result))
	})

	t.Run("falls back to the context workdir", func(t *testing.T) {
		workdir := t.TempDir()
		shell := newTestShell(t, ShellOptions{})
		ctx := WithWorkdir(t.Context(), workdir)

		result, _ := runShell(t, shell, ctx, `{"command":"pwd -P"}`)

		require.Equal(t, physicalPath(t, workdir), resultText(t, result))
	})

	t.Run("resolves relative workdirs against the context workdir", func(t *testing.T) {
		parent := t.TempDir()
		nested := filepath.Join(parent, "nested")
		require.NoError(t, os.Mkdir(nested, 0o750))
		shell := newTestShell(t, ShellOptions{})
		ctx := WithWorkdir(t.Context(), parent)

		result, _ := runShell(t, shell, ctx, `{"command":"pwd -P","workdir":"nested"}`)

		require.Equal(t, physicalPath(t, nested), resultText(t, result))
	})

	t.Run("times out long commands", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{Timeout: 150 * time.Millisecond})

		start := time.Now()
		result, _ := runShell(t, shell, t.Context(), `{"command":"sleep 5"}`)
		elapsed := time.Since(start)

		require.True(t, result.IsError)
		require.Contains(t, resultText(t, result), "command timed out after 150ms")
		require.Less(t, elapsed, 3*time.Second)
	})

	t.Run("caps requested timeouts", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{
			Timeout:    100 * time.Millisecond,
			MaxTimeout: 150 * time.Millisecond,
		})

		result, _ := runShell(t, shell, t.Context(), `{"command":"sleep 5","timeout_ms":60000}`)

		require.True(t, result.IsError)
		require.Contains(t, resultText(t, result), "command timed out after 150ms")
	})

	t.Run("kills background processes on timeout", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("process groups are POSIX only")
		}
		shell := newTestShell(t, ShellOptions{Timeout: 150 * time.Millisecond})

		start := time.Now()
		result, _ := runShell(t, shell, t.Context(), `{"command":"sleep 30 & wait"}`)
		elapsed := time.Since(start)

		require.True(t, result.IsError)
		require.Less(t, elapsed, 3*time.Second)
	})

	t.Run("truncates large output", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{MaxOutputBytes: 64})

		result, sink := runShell(t, shell, t.Context(), `{"command":"yes aaaa | head -n 100"}`)

		require.False(t, result.IsError)
		text := resultText(t, result)
		require.Contains(t, text, "[output truncated]")
		require.Less(t, len(text), 200)
		require.Greater(t, len(sink.text()), len(text))
	})

	t.Run("reports cancellation", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{Timeout: time.Minute})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(50*time.Millisecond, cancel)

		sink := &recordingSink{}
		_, err := shell.Execute(
			ctx,
			Call{Arguments: json.RawMessage(`{"command":"sleep 5"}`)},
			sink,
		)

		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("rejects invalid arguments", func(t *testing.T) {
		tests := []struct {
			name      string
			arguments string
			wantErr   string
		}{
			{name: "missing arguments", arguments: ``, wantErr: "arguments are required"},
			{
				name:      "unknown field",
				arguments: `{"command":"echo hi","bogus":true}`,
				wantErr:   "invalid arguments",
			},
			{
				name:      "blank command",
				arguments: `{"command":"   "}`,
				wantErr:   `"command" is required`,
			},
			{
				name:      "negative timeout",
				arguments: `{"command":"echo hi","timeout_ms":-1}`,
				wantErr:   "must not be negative",
			},
			{
				name:      "trailing data",
				arguments: `{"command":"echo hi"} {}`,
				wantErr:   "unexpected trailing data",
			},
			{name: "wrong type", arguments: `{"command":123}`, wantErr: "invalid arguments"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				shell := newTestShell(t, ShellOptions{})

				sink := &recordingSink{}
				_, err := shell.Execute(
					t.Context(),
					Call{Arguments: json.RawMessage(test.arguments)},
					sink,
				)

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})

	t.Run("rejects invalid workdirs", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{})

		t.Run("missing directory", func(t *testing.T) {
			missing := filepath.Join(t.TempDir(), "missing")

			sink := &recordingSink{}
			_, err := shell.Execute(
				t.Context(),
				Call{Arguments: json.RawMessage(pwdArguments(t, missing))},
				sink,
			)

			require.ErrorContains(t, err, "working directory")
		})

		t.Run("path that is a file", func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "file.txt")
			require.NoError(t, os.WriteFile(file, []byte("data"), 0o600))

			sink := &recordingSink{}
			_, err := shell.Execute(
				t.Context(),
				Call{Arguments: json.RawMessage(pwdArguments(t, file))},
				sink,
			)

			require.ErrorContains(t, err, "not a directory")
		})
	})

	t.Run("fails when the interpreter is missing", func(t *testing.T) {
		shell := newTestShell(t, ShellOptions{
			Interpreter: filepath.Join(t.TempDir(), "missing-shell"),
		})

		sink := &recordingSink{}
		_, err := shell.Execute(
			t.Context(),
			Call{Arguments: json.RawMessage(`{"command":"echo hi"}`)},
			sink,
		)

		require.ErrorContains(t, err, "start command")
	})
}

// TestShellToolEffectiveTimeout verifies timeout resolution and capping.
func TestShellToolEffectiveTimeout(t *testing.T) {
	shell := newTestShell(t, ShellOptions{Timeout: time.Minute, MaxTimeout: 5 * time.Minute})
	core := shell.core

	t.Run("falls back to the default", func(t *testing.T) {
		require.Equal(t, time.Minute, core.effectiveTimeout(0))
		require.Equal(t, time.Minute, core.effectiveTimeout(-5))
	})

	t.Run("honors requests below the cap", func(t *testing.T) {
		require.Equal(t, 30*time.Second, core.effectiveTimeout(30_000))
	})

	t.Run("caps requests above the maximum", func(t *testing.T) {
		require.Equal(t, 5*time.Minute, core.effectiveTimeout(600_000))
		require.Equal(t, 5*time.Minute, core.effectiveTimeout(math.MaxInt))
	})
}
