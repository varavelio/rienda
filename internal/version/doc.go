// Package version exposes Rienda's build metadata and the identifiers derived
// from it, such as the version string and the user agent used to identify the
// application when talking to external services.
//
// The Version, Commit, and Date variables are meant to be injected at build
// time through -ldflags, so released binaries report their real provenance.
// Version is cleaned of its tag prefix and surrounding whitespace when the
// package is initialized, and development builds that lack build metadata fall
// back to safe placeholders instead of reporting empty values.
package version
