// Package catalog resolves the context window of a model from the models.dev
// database.
//
// The window is needed to turn a context measurement into a percentage, and
// almost nobody declares it by hand, so it is resolved from three sources in
// strict order: the value declared in the configuration, the value published
// by models.dev and, when neither exists, a conservative fallback that errs low
// so a guardrail arrives early rather than late. Which level answered is never
// surfaced: callers report the window as the number it is.
//
// The database is cached in a single file under "~/.rienda/cache" and read
// without ever waiting on the network: a lookup answers from what the file
// holds, however old it is, and a background loop refreshes it in the
// meantime. The loop writes the new content to a temporary file that is
// renamed over the cache, so a failed fetch never damages what the previous one
// left. The endpoint is a parameter with a default, which is what lets the
// end-to-end suite point it at a local server instead of the real service.
//
// Everything here is best effort by design: Rienda works fully offline, with
// the cache it has or with the fallback, and a refresh failure is never a
// failure of a run.
package catalog
