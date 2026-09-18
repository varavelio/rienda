package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// formulaHashes returns a complete artifact set for formula generation.
func formulaHashes() map[string]string {
	return map[string]string{
		"rienda_darwin_arm64.tar.gz": "aaa",
		"rienda_darwin_amd64.tar.gz": "bbb",
		"rienda_linux_arm64.tar.gz":  "ddd",
		"rienda_linux_amd64.tar.gz":  "eee",
		"rienda_windows_amd64.zip":   "fff",
	}
}

// requireWorldReadable fails unless every user can read path, which is the
// requirement the release workflow has on the generated formulas: they are
// generated inside a container running as root and committed from the host with
// the unprivileged user of the runner.
func requireWorldReadable(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)

	other := info.Mode().Perm() & 0o007
	if info.IsDir() {
		// A directory also needs the traverse bit to expose its entries.
		require.Equal(t, os.FileMode(0o005), other, "%s must be traversable by every user", path)
		return
	}
	require.Equal(t, os.FileMode(0o004), other, "%s must be readable by every user", path)
}

func TestGenerateFormula(t *testing.T) {
	t.Run("renders the complete formula", func(t *testing.T) {
		content, err := generateFormula("rienda.rb", "0.1.0", formulaHashes())
		require.NoError(t, err)

		require.Contains(t, content, "class Rienda < Formula")
		require.Contains(t, content, `desc "Simple, declarative, multi-agent LLM harness"`)
		require.Contains(t, content, `homepage "https://github.com/varavelio/rienda"`)
		require.Contains(t, content, `version "0.1.0"`)
		require.Contains(
			t,
			content,
			`url "https://github.com/varavelio/rienda/releases/download/v0.1.0/rienda_darwin_arm64.tar.gz"`,
		)
		require.Contains(t, content, `sha256 "aaa"`)
		require.Contains(
			t,
			content,
			`url "https://github.com/varavelio/rienda/releases/download/v0.1.0/rienda_darwin_amd64.tar.gz"`,
		)
		require.Contains(t, content, `sha256 "bbb"`)
		require.Contains(
			t,
			content,
			`url "https://github.com/varavelio/rienda/releases/download/v0.1.0/rienda_linux_arm64.tar.gz"`,
		)
		require.Contains(t, content, `sha256 "ddd"`)
		require.Contains(
			t,
			content,
			`url "https://github.com/varavelio/rienda/releases/download/v0.1.0/rienda_linux_amd64.tar.gz"`,
		)
		require.Contains(t, content, `sha256 "eee"`)
		require.Contains(t, content, "on_macos do")
		require.Contains(t, content, "on_linux do")
		require.Contains(t, content, `bin.install "rienda"`)
		require.Contains(t, content, `system "#{bin}/rienda", "--help"`)
	})

	t.Run("fails when a required checksum is missing", func(t *testing.T) {
		hashes := formulaHashes()
		delete(hashes, "rienda_linux_amd64.tar.gz")

		_, err := generateFormula("rienda.rb", "0.1.0", hashes)
		require.ErrorContains(t, err, "checksum for rienda_linux_amd64.tar.gz is missing")
	})
}

func TestFormulaClassName(t *testing.T) {
	t.Run("derives valid Ruby class names", func(t *testing.T) {
		require.Equal(t, "Rienda", formulaClassName("rienda.rb"))
		require.Equal(t, "RiendaNext", formulaClassName("rienda-next.rb"))
		require.Equal(t, "RiendaAT010", formulaClassName("rienda@0.1.0.rb"))
	})
}

func TestNormalizeVersion(t *testing.T) {
	t.Run("strips the leading v", func(t *testing.T) {
		require.Equal(t, "1.2.3", normalizeVersion("v1.2.3"))
		require.Equal(t, "1.2.3", normalizeVersion("  V1.2.3 "))
		require.Equal(t, "1.2.3", normalizeVersion("1.2.3"))
	})
}

func TestURLs(t *testing.T) {
	t.Run("builds the GitHub release URLs", func(t *testing.T) {
		require.Equal(
			t,
			"https://github.com/varavelio/rienda/releases/download/v0.1.0",
			releaseBaseURL("0.1.0"),
		)
		require.Equal(
			t,
			"https://github.com/varavelio/rienda/releases/download/v0.1.0/manifest.json",
			manifestURL("0.1.0"),
		)
	})
}

func TestParseManifest(t *testing.T) {
	t.Run("maps artifact names to checksums", func(t *testing.T) {
		content := `{"artifacts":[{"name":"rienda_linux_amd64.tar.gz","sha256":"abc"}]}`
		hashes, err := parseManifest([]byte(content))
		require.NoError(t, err)
		require.Equal(t, map[string]string{"rienda_linux_amd64.tar.gz": "abc"}, hashes)
	})

	t.Run("rejects invalid manifests", func(t *testing.T) {
		_, err := parseManifest([]byte("not json"))
		require.ErrorContains(t, err, "parse manifest")

		_, err = parseManifest([]byte(`{"artifacts":[{"name":"","sha256":""}]}`))
		require.ErrorContains(t, err, "name and sha256 are required")
	})
}

func TestFetchManifest(t *testing.T) {
	t.Run("downloads and parses the manifest", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(
				[]byte(`{"artifacts":[{"name":"rienda_linux_amd64.tar.gz","sha256":"abc"}]}`),
			)
		}))
		t.Cleanup(server.Close)

		hashes, err := fetchManifest(context.Background(), server.URL)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"rienda_linux_amd64.tar.gz": "abc"}, hashes)
	})

	t.Run("fails on HTTP errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(server.Close)

		_, err := fetchManifest(context.Background(), server.URL)
		require.ErrorContains(t, err, "404")
	})
}

// stubChecksums returns a fetch function serving fixed checksums.
func stubChecksums(
	hashes map[string]string,
) func(context.Context, string) (map[string]string, error) {
	return func(context.Context, string) (map[string]string, error) {
		return hashes, nil
	}
}

//
//nolint:gosec // every path lives in the temporary tap root of the test.
func TestRun(t *testing.T) {
	t.Run("writes the moving and pinned formulas for stable versions", func(t *testing.T) {
		outputRoot := t.TempDir()
		os.Args = []string{"brew", "v0.1.0", outputRoot}
		require.NoError(t, run(context.Background(), stubChecksums(formulaHashes())))

		formulaDir := filepath.Join(outputRoot, "Formula", "rienda")
		entries, err := os.ReadDir(formulaDir)
		require.NoError(t, err)
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		require.ElementsMatch(t, []string{"rienda.rb", "rienda@0.1.0.rb"}, names)

		content, err := os.ReadFile(filepath.Join(formulaDir, "rienda.rb"))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(content), "# This file was generated"))

		// The workflow commits the formulas from outside the container that
		// generates them, so the directory and the files must be readable by
		// every user.
		requireWorldReadable(t, formulaDir)
		requireWorldReadable(t, filepath.Join(formulaDir, "rienda.rb"))
	})

	t.Run("writes only the next formula for pre-releases", func(t *testing.T) {
		outputRoot := t.TempDir()
		os.Args = []string{"brew", "v0.1.0-alpha.6", outputRoot}
		require.NoError(t, run(context.Background(), stubChecksums(formulaHashes())))

		formulaDir := filepath.Join(outputRoot, "Formula", "rienda")
		content, err := os.ReadFile(filepath.Join(formulaDir, "rienda-next.rb"))
		require.NoError(t, err)
		require.Contains(t, string(content), "class RiendaNext < Formula")
		require.Contains(t, string(content), `version "0.1.0-alpha.6"`)

		_, err = os.Stat(filepath.Join(formulaDir, "rienda@0.1.0-alpha.6.rb"))
		require.True(t, os.IsNotExist(err))
	})

	t.Run("rejects invalid argument counts", func(t *testing.T) {
		os.Args = []string{"brew"}
		require.ErrorContains(t, run(context.Background(), stubChecksums(nil)), "usage:")
	})
}
