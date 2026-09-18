package version

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumber(t *testing.T) {
	t.Run("returns the version injected at build time", func(t *testing.T) {
		withVersion(t, "0.1.0", "unknown", "unknown", func() {
			require.Equal(t, "0.1.0", Number())
		})
	})

	t.Run("strips the tag prefix", func(t *testing.T) {
		withVersion(t, "v0.1.0", "unknown", "unknown", func() {
			require.Equal(t, "0.1.0", Number())
		})

		withVersion(t, "V0.1.0", "unknown", "unknown", func() {
			require.Equal(t, "0.1.0", Number())
		})
	})

	t.Run("strips surrounding whitespace", func(t *testing.T) {
		withVersion(t, " v0.1.0 ", "unknown", "unknown", func() {
			require.Equal(t, "0.1.0", Number())
		})
	})

	t.Run("falls back to the development version", func(t *testing.T) {
		withVersion(t, "v", "unknown", "unknown", func() {
			require.Equal(t, "dev", Number())
		})
	})
}

func TestNormalizeVersion(t *testing.T) {
	withVersion(t, " v1.2.3 ", "unknown", "unknown", func() {
		normalizeOnce = sync.Once{}
		normalizeVersion()
		require.Equal(t, "1.2.3", Version)

		Version = "v9.9.9"
		normalizeVersion()
		require.Equal(t, "v9.9.9", Version)
	})
}

func TestString(t *testing.T) {
	withVersion(t, "0.1.0", "unknown", "unknown", func() {
		require.Equal(t, "rienda 0.1.0", String())
	})

	withVersion(t, "", "unknown", "unknown", func() {
		require.Equal(t, "rienda dev", String())
	})
}

func TestUserAgent(t *testing.T) {
	t.Run("uses the product token format", func(t *testing.T) {
		withVersion(t, "0.1.0", "unknown", "unknown", func() {
			require.Equal(t, "rienda/0.1.0", UserAgent())
		})
	})

	t.Run("omits the tag prefix", func(t *testing.T) {
		withVersion(t, "v0.1.0", "unknown", "unknown", func() {
			require.Equal(t, "rienda/0.1.0", UserAgent())
		})
	})

	t.Run("falls back to the development version", func(t *testing.T) {
		withVersion(t, "  ", "unknown", "unknown", func() {
			require.Equal(t, "rienda/dev", UserAgent())
		})
	})
}

func TestDetailed(t *testing.T) {
	withVersion(t, "0.1.0", "abc123", "2026-06-27", func() {
		require.Equal(t, "rienda 0.1.0 commit abc123 built 2026-06-27", Detailed())
	})

	withVersion(t, "dev", "unknown", "unknown", func() {
		require.Equal(t, "rienda dev", Detailed())
	})
}

// withVersion temporarily replaces build metadata during a test.
func withVersion(t *testing.T, version, commit, date string, test func()) {
	t.Helper()
	previousVersion := Version
	previousCommit := Commit
	previousDate := Date
	Version = version
	Commit = commit
	Date = date
	defer func() {
		Version = previousVersion
		Commit = previousCommit
		Date = previousDate
	}()

	test()
}
