package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// fakeClock skips the waits between attempts and records them, so a test
// asserts the backoff without sleeping through it.
type fakeClock struct {
	delays []time.Duration
}

// After records the wait and returns at once.
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.delays = append(c.delays, d)
	ready := make(chan time.Time, 1)
	ready <- time.Now()
	return ready
}

// Now returns the current time.
func (c *fakeClock) Now() time.Time { return time.Now() }

// connectionError is a stub of the net.Error interface, standing for a dropped
// connection.
type connectionError struct{}

// Error describes the failure.
func (connectionError) Error() string { return "connection reset by peer" }

// Timeout reports that the failure is not a timeout.
func (connectionError) Timeout() bool { return false }

// Temporary reports that the failure is not temporary.
func (connectionError) Temporary() bool { return false }

// withFakeClock replaces the time source of the waits for the duration of one
// test, keeping the suite fast.
func withFakeClock(t *testing.T) *fakeClock {
	t.Helper()

	fake := &fakeClock{}
	previous := clock
	clock = fake
	t.Cleanup(func() { clock = previous })
	return fake
}

// TestIsTransient verifies the classification of retryable failures.
func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no failure", err: nil, want: false},
		{name: "canceled context", err: context.Canceled, want: false},
		{name: "expired deadline", err: context.DeadlineExceeded, want: false},
		{
			name: "wrapped canceled context",
			err:  fmt.Errorf("call failed: %w", context.Canceled),
			want: false,
		},
		{
			name: "rate limit",
			err:  &llm.Error{Kind: llm.ErrorKindRateLimit},
			want: true,
		},
		{
			name: "overloaded provider",
			err:  &llm.Error{Kind: llm.ErrorKindOverloaded},
			want: true,
		},
		{
			name: "failing provider",
			err:  &llm.Error{Kind: llm.ErrorKindServer},
			want: true,
		},
		{
			name: "rejected request",
			err:  &llm.Error{Kind: llm.ErrorKindInvalidRequest},
			want: false,
		},
		{
			name: "rejected credentials",
			err:  &llm.Error{Kind: llm.ErrorKindAuthentication},
			want: false,
		},
		{
			name: "unclassified status 503",
			err:  &llm.Error{StatusCode: 503},
			want: true,
		},
		{
			name: "unclassified status 429",
			err:  &llm.Error{StatusCode: 429},
			want: true,
		},
		{
			name: "unclassified status 408",
			err:  &llm.Error{StatusCode: 408},
			want: true,
		},
		{
			name: "unclassified status 409",
			err:  &llm.Error{StatusCode: 409},
			want: true,
		},
		{
			name: "unclassified status 400",
			err:  &llm.Error{StatusCode: 400},
			want: false,
		},
		{name: "wrapped transport failure", err: connectionError{}, want: true},
		{
			name: "truncated body",
			err:  fmt.Errorf("read: %w", io.ErrUnexpectedEOF),
			want: true,
		},
		{name: "unknown failure", err: errors.New("boom"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, IsTransient(test.err))
		})
	}
}

// TestDo verifies the retry loop.
func TestDo(t *testing.T) {
	t.Run("returns the first success without waiting", func(t *testing.T) {
		fake := withFakeClock(t)
		calls := 0

		value, err := Do(t.Context(), func(context.Context) (string, error) {
			calls++
			return "ok", nil
		}, nil)

		require.NoError(t, err)
		require.Equal(t, "ok", value)
		require.Equal(t, 1, calls)
		require.Empty(t, fake.delays)
	})

	t.Run("repeats transient failures until success", func(t *testing.T) {
		fake := withFakeClock(t)
		var observed []Attempt
		calls := 0

		value, err := Do(t.Context(), func(context.Context) (int, error) {
			calls++
			if calls < 3 {
				return 0, &llm.Error{Provider: "test", Kind: llm.ErrorKindServer, Message: "down"}
			}
			return 42, nil
		}, func(attempt Attempt) { observed = append(observed, attempt) })

		require.NoError(t, err)
		require.Equal(t, 42, value)
		require.Equal(t, 3, calls)
		require.Len(t, fake.delays, 2)
		require.Len(t, observed, 2)
		require.Equal(t, 1, observed[0].Number)
		require.Equal(t, 2, observed[1].Number)
		require.Positive(t, observed[0].Delay)
		require.ErrorContains(t, observed[0].Err, "down")
	})

	t.Run("returns non-transient failures without repeating", func(t *testing.T) {
		fake := withFakeClock(t)
		calls := 0

		_, err := Do(t.Context(), func(context.Context) (int, error) {
			calls++
			return 0, &llm.Error{Kind: llm.ErrorKindInvalidRequest, Message: "bad"}
		}, nil)

		require.ErrorContains(t, err, "bad")
		require.Equal(t, 1, calls)
		require.Empty(t, fake.delays)
	})

	t.Run("stops when the failure is marked permanent", func(t *testing.T) {
		fake := withFakeClock(t)
		calls := 0

		_, err := Do(t.Context(), func(context.Context) (int, error) {
			calls++
			return 0, Permanent(
				&llm.Error{Kind: llm.ErrorKindServer, Message: "down"},
			)
		}, nil)

		require.ErrorContains(t, err, "down")
		require.Equal(t, 1, calls)
		require.Empty(t, fake.delays)
	})

	t.Run("gives up after the attempts are exhausted", func(t *testing.T) {
		fake := withFakeClock(t)
		calls := 0

		_, err := Do(t.Context(), func(context.Context) (int, error) {
			calls++
			return 0, &llm.Error{Kind: llm.ErrorKindServer, Message: "down"}
		}, nil)

		require.ErrorContains(t, err, "down")
		require.Equal(t, maxAttempts, calls)
		require.Len(t, fake.delays, maxAttempts-1)
	})

	t.Run("stops on cancellation before the first attempt", func(t *testing.T) {
		withFakeClock(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := 0

		_, err := Do(ctx, func(context.Context) (int, error) {
			calls++
			return 0, nil
		}, nil)

		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, calls)
	})

	t.Run("stops when the call cancels the context", func(t *testing.T) {
		fake := withFakeClock(t)
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0

		_, err := Do(ctx, func(context.Context) (int, error) {
			calls++
			cancel()
			return 0, &llm.Error{Kind: llm.ErrorKindServer, Message: "down"}
		}, nil)

		require.ErrorContains(t, err, "down")
		require.Equal(t, 1, calls)
		require.Empty(t, fake.delays)
	})
}
