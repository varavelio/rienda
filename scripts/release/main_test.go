package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArchiveName(t *testing.T) {
	t.Run("names every release target", func(t *testing.T) {
		for _, buildTarget := range releaseTargets {
			expected := projectName + "_" + buildTarget.OS + "_" + buildTarget.Arch + "." + archiveFormat(
				buildTarget.OS,
			)
			require.Equal(t, expected, archiveName(buildTarget))
		}
	})
}

func TestArchiveFormat(t *testing.T) {
	t.Run("uses zip only for windows", func(t *testing.T) {
		require.Equal(t, "zip", archiveFormat("windows"))
		require.Equal(t, "tar.gz", archiveFormat("linux"))
		require.Equal(t, "tar.gz", archiveFormat("darwin"))
	})
}

func TestBinaryName(t *testing.T) {
	t.Run("adds the exe suffix only for windows", func(t *testing.T) {
		require.Equal(t, "rienda.exe", binaryName("windows"))
		require.Equal(t, "rienda", binaryName("linux"))
		require.Equal(t, "rienda", binaryName("darwin"))
	})
}

func TestReleaseTargets(t *testing.T) {
	t.Run("covers the three operating systems on both architectures", func(t *testing.T) {
		expected := []target{
			{OS: "linux", Arch: "amd64"},
			{OS: "linux", Arch: "arm64"},
			{OS: "darwin", Arch: "amd64"},
			{OS: "darwin", Arch: "arm64"},
			{OS: "windows", Arch: "amd64"},
			{OS: "windows", Arch: "arm64"},
		}

		require.Equal(t, expected, releaseTargets)
	})
}

func TestLDFlags(t *testing.T) {
	t.Run("points every metadata variable at the version package", func(t *testing.T) {
		metadata := buildMetadata{Version: "0.1.0", Commit: "abc123", Date: "2026-06-27T00:00:00Z"}

		require.Equal(
			t,
			`-s -w -X github.com/varavelio/rienda/internal/version.Version=0.1.0 `+
				`-X github.com/varavelio/rienda/internal/version.Commit=abc123 `+
				`-X github.com/varavelio/rienda/internal/version.Date=2026-06-27T00:00:00Z`,
			metadata.ldflags(),
		)
	})
}

func TestNormalizeVersion(t *testing.T) {
	t.Run("strips tag prefixes and whitespace", func(t *testing.T) {
		require.Equal(t, "1.2.3", normalizeVersion("v1.2.3"))
		require.Equal(t, "1.2.3", normalizeVersion("  V1.2.3  "))
		require.Equal(t, "1.2.3", normalizeVersion("refs/tags/v1.2.3"))
		require.Equal(t, "1.2.3", normalizeVersion("1.2.3"))
		require.Equal(t, "0.1.0-alpha.6", normalizeVersion("v0.1.0-alpha.6"))
		require.Empty(t, normalizeVersion("  "))
	})
}

func TestTagLike(t *testing.T) {
	t.Run("accepts only values that start with v", func(t *testing.T) {
		require.Equal(t, "v1.2.3", tagLike("v1.2.3"))
		require.Equal(t, "v1.2.3", tagLike(" v1.2.3 "))
		require.Empty(t, tagLike("1.2.3"))
		require.Empty(t, tagLike("main"))
	})
}

func TestFirstNonEmpty(t *testing.T) {
	t.Run("returns the first trimmed non-empty value", func(t *testing.T) {
		require.Equal(t, "a", firstNonEmpty("", " a ", "b"))
		require.Equal(t, "b", firstNonEmpty("", "", "b"))
		require.Empty(t, firstNonEmpty("", " "))
	})
}

func TestDetectVersion(t *testing.T) {
	t.Run("prefers the explicit environment version", func(t *testing.T) {
		t.Setenv("RIENDA_VERSION", " v0.2.1 ")
		require.Equal(t, "0.2.1", detectVersion(context.Background(), "."))
	})

	t.Run("accepts a tag-like reference name", func(t *testing.T) {
		t.Setenv("RIENDA_VERSION", "")
		t.Setenv("GITHUB_REF_NAME", "v0.3.0")
		require.Equal(t, "0.3.0", detectVersion(context.Background(), "."))
	})

	t.Run("falls back to a dev version outside git", func(t *testing.T) {
		t.Setenv("RIENDA_VERSION", "")
		t.Setenv("GITHUB_REF_NAME", "main")
		require.NotEmpty(t, detectVersion(context.Background(), "."))
	})
}

func TestDetectBuildMetadata(t *testing.T) {
	t.Run("prefers the explicit environment metadata", func(t *testing.T) {
		t.Setenv("RIENDA_VERSION", "v0.4.0")
		t.Setenv("RIENDA_COMMIT", "abc123")
		t.Setenv("RIENDA_DATE", "2026-06-27T10:00:00Z")

		metadata := detectBuildMetadata(context.Background(), ".")

		require.Equal(t, "0.4.0", metadata.Version)
		require.Equal(t, "abc123", metadata.Commit)
		require.Equal(t, "2026-06-27T10:00:00Z", metadata.Date)
	})

	t.Run("falls back to the environment of the release workflow", func(t *testing.T) {
		root, err := findProjectRoot()
		require.NoError(t, err)

		t.Setenv("RIENDA_VERSION", "")
		t.Setenv("RIENDA_COMMIT", "")
		t.Setenv("GITHUB_REF_NAME", "")
		t.Setenv("GITHUB_SHA", "")
		t.Setenv("RIENDA_DATE", "")

		metadata := detectBuildMetadata(context.Background(), root)

		require.NotEmpty(t, metadata.Version)
		require.NotEmpty(t, metadata.Commit)
		require.NotEmpty(t, metadata.Date)
	})

	t.Run("reads the commit from the repository when git is available", func(t *testing.T) {
		root, err := findProjectRoot()
		require.NoError(t, err)

		t.Setenv("RIENDA_COMMIT", "")
		t.Setenv("GITHUB_SHA", "")

		metadata := detectBuildMetadata(context.Background(), root)

		require.NotEqual(t, "unknown", metadata.Commit)
	})
}

func TestPrepareDistDir(t *testing.T) {
	t.Run("replaces the directory with an empty readable one", func(t *testing.T) {
		distDir := filepath.Join(t.TempDir(), "dist")
		require.NoError(t, os.MkdirAll(filepath.Join(distDir, "stale"), 0o700))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(distDir, "stale", "old.txt"), []byte("old"), 0o600),
		)
		require.NoError(t, prepareDistDir(distDir))

		info, err := os.Stat(distDir)
		require.NoError(t, err)
		require.True(t, info.IsDir())
		require.Equal(t, os.FileMode(artifactDirMode), info.Mode().Perm())

		entries, err := os.ReadDir(distDir)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}

// requireWorldReadable fails unless every user can read path, which is the
// requirement the release workflow has on the generated artifacts: they are
// built inside a container running as root and uploaded from the host with the
// unprivileged user of the runner.
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

func TestArtifactPermissions(t *testing.T) {
	t.Run("makes every artifact readable by the release workflow", func(t *testing.T) {
		distDir := t.TempDir()
		require.NoError(t, prepareDistDir(distDir))
		require.NoError(t, writeManifest(distDir, "0.1.0", nil))
		require.NoError(t, writeChecksums(distDir))

		binary := filepath.Join(t.TempDir(), binaryName("linux"))
		require.NoError(t, os.WriteFile(binary, []byte("binary"), 0o600))
		files := map[string]string{binary: binaryName("linux")}
		require.NoError(t, createTarGz(filepath.Join(distDir, "rienda_linux_amd64.tar.gz"), files))
		require.NoError(t, createZip(filepath.Join(distDir, "rienda_windows_amd64.zip"), files))

		requireWorldReadable(t, distDir)
		requireWorldReadable(t, filepath.Join(distDir, manifestFileName))
		requireWorldReadable(t, filepath.Join(distDir, checksumFileName))
		requireWorldReadable(t, filepath.Join(distDir, "rienda_linux_amd64.tar.gz"))
		requireWorldReadable(t, filepath.Join(distDir, "rienda_windows_amd64.zip"))
	})
}

func TestFindProjectRoot(t *testing.T) {
	t.Run("locates the repository root from any depth", func(t *testing.T) {
		root, err := findProjectRoot()
		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(root, "go.mod"))
		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(root, "Taskfile.yml"))
		require.NoError(t, err)
	})

	t.Run("fails outside a project", func(t *testing.T) {
		t.Chdir(t.TempDir())

		_, err := findProjectRoot()
		require.Error(t, err)
	})
}

func TestFileSHA256(t *testing.T) {
	t.Run("hashes the exact file bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "content.txt")
		require.NoError(t, os.WriteFile(path, []byte("rienda\n"), 0o600))

		checksum, err := fileSHA256(path)
		require.NoError(t, err)
		require.Equal(
			t,
			"b24e3e93eafee4b90fb43ca4aaab1d8c4b22d52e88b391080bed4bcfdd34b8e2",
			checksum,
		)
	})

	t.Run("fails when the file does not exist", func(t *testing.T) {
		_, err := fileSHA256(filepath.Join(t.TempDir(), "missing.txt"))
		require.ErrorContains(t, err, "open")
	})
}

//
//nolint:gosec // every path lives in the temporary dist directory of the test.
func TestWriteChecksums(t *testing.T) {
	t.Run("writes every file except the checksums file itself", func(t *testing.T) {
		distDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(distDir, "b.txt"), []byte("b"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(distDir, "a.txt"), []byte("a"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(distDir, "nested"), 0o750))
		require.NoError(t, writeChecksums(distDir))

		content, err := os.ReadFile(filepath.Join(distDir, checksumFileName))
		require.NoError(t, err)

		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		require.Len(t, lines, 2)

		hashA := "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
		hashB := "3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d"
		require.Equal(t, hashA+"  a.txt", lines[0])
		require.Equal(t, hashB+"  b.txt", lines[1])
	})
}

//
//nolint:gosec // every path lives in the temporary dist directory of the test.
func TestWriteManifest(t *testing.T) {
	t.Run("writes the complete release inventory", func(t *testing.T) {
		distDir := t.TempDir()
		artifacts := []releaseArtifact{
			{
				Arch:   "amd64",
				Format: "tar.gz",
				Name:   "rienda_linux_amd64.tar.gz",
				OS:     "linux",
				SHA256: "abc",
			},
		}
		require.NoError(t, writeManifest(distDir, "0.1.0", artifacts))

		content, err := os.ReadFile(filepath.Join(distDir, manifestFileName))
		require.NoError(t, err)

		var manifest releaseManifest
		require.NoError(t, json.Unmarshal(content, &manifest))
		require.Equal(t, "rienda", manifest.Project)
		require.Equal(t, "varavelio/rienda", manifest.Repo)
		require.Equal(t, "0.1.0", manifest.Version)
		require.Equal(t, artifacts, manifest.Artifacts)
		require.True(t, strings.HasSuffix(string(content), "\n"))
	})
}

//
//nolint:gosec // every path lives in the temporary directories of the test.
func TestCreateArchives(t *testing.T) {
	setupFiles := func(t *testing.T) map[string]string {
		t.Helper()
		sourceDir := t.TempDir()
		binary := filepath.Join(sourceDir, binaryName("linux"))
		license := filepath.Join(sourceDir, "LICENSE")
		require.NoError(t, os.WriteFile(binary, []byte("binary"), 0o600))
		require.NoError(t, os.WriteFile(license, []byte("license"), 0o600))

		return map[string]string{binary: binaryName("linux"), license: "LICENSE"}
	}

	t.Run("packs the given files into tar.gz", func(t *testing.T) {
		archivePath := filepath.Join(t.TempDir(), archiveName(target{OS: "linux", Arch: "amd64"}))
		files := setupFiles(t)
		require.NoError(t, createTarGz(archivePath, files))

		archiveFile, err := os.Open(archivePath)
		require.NoError(t, err)
		defer func() {
			_ = archiveFile.Close()
		}()

		gzipReader, err := gzip.NewReader(archiveFile)
		require.NoError(t, err)
		tarReader := tar.NewReader(gzipReader)

		names := []string{}
		for {
			header, err := tarReader.Next()
			if err != nil {
				break
			}
			names = append(names, header.Name)
		}
		require.ElementsMatch(t, []string{"rienda", "LICENSE"}, names)
	})

	t.Run("packs the given files into zip", func(t *testing.T) {
		archivePath := filepath.Join(t.TempDir(), "archive.zip")
		files := setupFiles(t)
		require.NoError(t, createZip(archivePath, files))

		archive, err := zip.OpenReader(archivePath)
		require.NoError(t, err)
		defer func() {
			_ = archive.Close()
		}()

		names := []string{}
		for _, file := range archive.File {
			names = append(names, file.Name)
		}
		require.ElementsMatch(t, []string{"rienda", "LICENSE"}, names)
	})
}

func TestArchiveFiles(t *testing.T) {
	t.Run("keeps only files that exist in the repository", func(t *testing.T) {
		root := t.TempDir()
		binary := filepath.Join(root, "rienda")
		require.NoError(t, os.WriteFile(binary, []byte("binary"), 0o600))

		files := archiveFiles(root, binary, "linux")
		require.Equal(t, map[string]string{binary: "rienda"}, files)
	})

	t.Run("adds the license of the repository when it exists", func(t *testing.T) {
		root, err := findProjectRoot()
		require.NoError(t, err)
		binary := filepath.Join(t.TempDir(), "rienda")
		require.NoError(t, os.WriteFile(binary, []byte("binary"), 0o600))

		files := archiveFiles(root, binary, "linux")
		require.Equal(
			t,
			map[string]string{binary: "rienda", filepath.Join(root, "LICENSE"): "LICENSE"},
			files,
		)
	})
}
