// Package retry repeats provider calls that fail with a transient error.
//
// Rienda applies one fixed retry policy to every model call, whichever the
// mode of the program: five attempts with exponential backoff and full jitter,
// capped at eight seconds between waits. Only the failures a repeated call may
// overcome are retried (see IsTransient); the ones the provider attributes to
// the request, the credentials or the account are returned at once.
//
// The policy is a sane default, not a setting: no caller configures it, so
// every mode behaves the same way.
//
// A caller that already delivered the output of a failed call must wrap its
// error with Permanent before returning it, because repeating the call would
// repeat what the user already saw.
package retry
