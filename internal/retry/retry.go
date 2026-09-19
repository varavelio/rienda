package retry

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/nodivbyzero/try"

	"github.com/varavelio/rienda/internal/llm"
)

// Bounds of the shared retry policy.
const (
	// maxAttempts is the total number of attempts of one call, the first one
	// included.
	maxAttempts = 5

	// initialDelay is the wait before the second attempt. It doubles with
	// every attempt, up to maxDelay.
	initialDelay = 500 * time.Millisecond

	// maxDelay caps any single wait between attempts.
	maxDelay = 8 * time.Second
)

// clock is the time source of the waits between attempts. Tests replace it
// with a clock that returns at once, so a suite does not sleep through the
// backoff.
var clock try.Clock = systemClock{}

// systemClock measures the waits with the real time.
type systemClock struct{}

// After returns a channel that fires once d elapses.
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Now returns the current time.
func (systemClock) Now() time.Time { return time.Now() }

// Attempt describes a transient failure that is about to be retried. It is
// reported to the observer of Do before the wait that precedes the next
// attempt.
type Attempt struct {
	// Number is the 1-based number of the attempt that failed.
	Number int

	// Delay is the wait before the next attempt.
	Delay time.Duration

	// Err is the failure that triggered the retry.
	Err error
}

// Do runs fn and returns its result, repeating the call while it fails with a
// transient error and attempts remain. The waits grow exponentially with full
// jitter, so callers that fail at the same time do not retry in lockstep, and
// the cancellation of ctx interrupts the operation at any wait.
//
// The observer, when it is not nil, is notified before every wait with the
// failure and the delay that precedes the next attempt.
//
// fn may wrap an error with Permanent to stop the retries at once, for example
// when repeating the call would duplicate work the caller already did.
func Do[T any](
	ctx context.Context,
	fn func(context.Context) (T, error),
	observer func(Attempt),
) (T, error) {
	options := []try.Option{
		try.WithAttempts(maxAttempts),
		try.WithInitialDelay(initialDelay),
		try.WithMaxDelay(maxDelay),
		try.WithJitter(try.FullJitter),
		try.WithRetryIf(IsTransient),
		try.WithClock(clock),
	}
	if observer != nil {
		options = append(options, try.WithOnRetry(func(info try.RetryInfo) {
			observer(Attempt{Number: info.Attempt, Delay: info.Delay, Err: info.Err})
		}))
	}

	//nolint:wrapcheck // the failure already names the call it aborted.
	return try.Do(ctx, fn, options...)
}

// Permanent marks err as not worth repeating, so Do returns it after the
// attempt that produced it. The wrapping keeps the message and the cause of
// err reachable.
func Permanent(err error) error {
	//nolint:wrapcheck // the wrapping only marks err as permanent.
	return try.Permanent(err)
}

// IsTransient reports whether err is a failure that repeating the call may
// overcome: a rate limit, a failed or overloaded provider, an attempt that
// timed out or conflicted, or a transport error. The failures a provider
// attributes to the request, the credentials or the account are not transient,
// and neither is a canceled context.
func IsTransient(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	}

	if apiErr, ok := errors.AsType[*llm.Error](err); ok {
		return transientKind(apiErr.Kind) || transientStatus(apiErr.StatusCode)
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// transientKind reports whether a classified provider failure is worth
// repeating.
func transientKind(kind llm.ErrorKind) bool {
	switch kind {
	case llm.ErrorKindRateLimit, llm.ErrorKindOverloaded, llm.ErrorKindServer:
		return true
	default:
		return false
	}
}

// transientStatus reports whether an HTTP failure is worth repeating when the
// provider gave no recognized error code.
func transientStatus(status int) bool {
	switch {
	case status == http.StatusRequestTimeout,
		status == http.StatusConflict,
		status == http.StatusTooManyRequests:
		return true
	case status >= http.StatusInternalServerError:
		return true
	default:
		return false
	}
}
