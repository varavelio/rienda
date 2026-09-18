package version

import (
	"strings"
	"sync"
)

const (
	name = "rienda"

	// Repository is the canonical Rienda repository URL.
	Repository = "https://github.com/varavelio/rienda"
)

var (
	// Version is the Rienda version set at build time using ldflags.
	Version = "dev"

	// Commit is the git commit hash set at build time using ldflags.
	Commit = "unknown"

	// Date is the build date set at build time using ldflags.
	Date = "unknown"
)

// normalizeOnce guards the one-time cleanup of the Version build metadata.
var normalizeOnce sync.Once

// init cleans the Version build metadata as soon as the package is imported, so
// the value injected with ldflags is exposed without a tag prefix and without
// surrounding whitespace.
func init() {
	normalizeVersion()
}

// normalizeVersion applies the one-time cleanup to Version, leaving the
// variable already normalized for every reader.
func normalizeVersion() {
	normalizeOnce.Do(func() {
		Version = cleanVersion(Version)
	})
}

// cleanVersion removes surrounding whitespace and an optional leading "v" or
// "V" tag prefix, so a version such as "v1.2.3" becomes "1.2.3".
func cleanVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	value = strings.TrimPrefix(value, "V")

	return value
}

// String returns the concise Rienda version string.
func String() string {
	return name + " " + Number()
}

// UserAgent returns the HTTP User-Agent header value that identifies Rienda and
// its version, formatted as the standard "product/version" token.
func UserAgent() string {
	return name + "/" + Number()
}

// Detailed returns Rienda version metadata useful for diagnostics.
func Detailed() string {
	parts := []string{String()}
	if known(Commit) {
		parts = append(parts, "commit "+CommitHash())
	}
	if known(Date) {
		parts = append(parts, "built "+BuildDate())
	}

	return strings.Join(parts, " ")
}

// Number returns the normalized Rienda version number.
func Number() string {
	return normalized(cleanVersion(Version), "dev")
}

// CommitHash returns the normalized git commit hash.
func CommitHash() string {
	return normalized(Commit, "unknown")
}

// BuildDate returns the normalized build date.
func BuildDate() string {
	return normalized(Date, "unknown")
}

// normalized returns fallback when value is empty after trimming.
func normalized(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}

	return value
}

// known reports whether a build metadata value should be displayed.
func known(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "unknown"
}
