package command

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

type recStream struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (r *recStream) Emit(name string, p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		r.data = map[string][]byte{}
	}
	r.data[name] = append(r.data[name], p...)
}

func TestCommand(t *testing.T) {
	t.Run("runs and streams", func(t *testing.T) {
		s := &recStream{}
		out, err := Run(
			t.Context(),
			Request{Argv: []string{"sh", "-c", "echo hi"}, Dir: t.TempDir(), Stream: s},
		)
		require.NoError(t, err)
		require.Equal(t, 0, out.ExitCode)
		require.Contains(t, out.Stdout, "hi")
		require.Contains(t, string(s.data["stdout"]), "hi")
	})
	t.Run("non-zero exit", func(t *testing.T) {
		out, err := Run(
			t.Context(),
			Request{Argv: []string{"sh", "-c", "exit 3"}, Dir: t.TempDir()},
		)
		require.NoError(t, err)
		require.Equal(t, 3, out.ExitCode)
	})
	t.Run("times out and kills group", func(t *testing.T) {
		start := time.Now()
		out, err := Run(
			t.Context(),
			Request{
				Argv:    []string{"sh", "-c", "sleep 30"},
				Dir:     t.TempDir(),
				Timeout: 150 * time.Millisecond,
			},
		)
		require.NoError(t, err)
		require.True(t, out.TimedOut)
		require.Less(t, time.Since(start), 5*time.Second)
	})
	t.Run("kills background group", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("process groups are POSIX only")
		}
		start := time.Now()
		_, err := Run(
			t.Context(),
			Request{
				Argv:    []string{"sh", "-c", "sleep 30 & wait"},
				Dir:     t.TempDir(),
				Timeout: 150 * time.Millisecond,
			},
		)
		require.NoError(t, err)
		require.Less(t, time.Since(start), 5*time.Second)
	})
	t.Run("caps output", func(t *testing.T) {
		out, err := Run(
			t.Context(),
			Request{
				Argv:      []string{"sh", "-c", "yes aaaa | head -n 5000"},
				Dir:       t.TempDir(),
				MaxOutput: 64,
			},
		)
		require.NoError(t, err)
		require.True(t, out.Truncated)
		require.LessOrEqual(t, len(out.Stdout), 64)
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		_, err := Run(ctx, Request{Argv: []string{"sh", "-c", "sleep 5"}, Dir: t.TempDir()})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("signal exit -1", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("signals differ on windows")
		}
		out, err := Run(
			t.Context(),
			Request{Argv: []string{"sh", "-c", "kill -TERM $$"}, Dir: t.TempDir()},
		)
		require.NoError(t, err)
		require.Equal(t, -1, out.ExitCode)
	})
	t.Run("requires argv and dir", func(t *testing.T) {
		_, err := Run(t.Context(), Request{Dir: t.TempDir()})
		require.Error(t, err)
		_, err = Run(t.Context(), Request{Argv: []string{"echo"}})
		require.Error(t, err)
	})
}

// TestTruncateUTF8 verifies that output truncation never splits a rune.
func TestTruncateUTF8(t *testing.T) {
	t.Run("keeps a string within the budget unchanged", func(t *testing.T) {
		out, cut := truncateUTF8("hello", 10)
		require.Equal(t, "hello", out)
		require.False(t, cut)
	})

	t.Run("cuts to the budget and reports it", func(t *testing.T) {
		out, cut := truncateUTF8("hello", 3)
		require.Equal(t, "hel", out)
		require.True(t, cut)
	})

	t.Run("never splits a multibyte rune", func(t *testing.T) {
		// "á" is two bytes; a budget that lands inside it must back off to a
		// rune boundary.
		out, cut := truncateUTF8("aá", 2)
		require.Equal(t, "a", out)
		require.True(t, cut)
		require.True(t, utf8.ValidString(out))
	})
}
