package files

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// project writes a project with ignore rules into a temporary directory and
// returns its root. The tree exercises the rules of the ignore files: a
// pattern matching at any depth, a directory, a re-inclusion, a nested ignore
// file, a hidden file and the version control directory.
func project(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()

		full := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}

	write(".gitignore", "*.log\nbuild/\n!keep/*.log\n")
	write("main.go", "package main\n")
	write("app.log", "ignored\n")
	write("build/out.bin", "ignored\n")
	write("keep/keep.log", "re-included\n")
	write(".hidden/secret.go", "package hidden\n")
	write(".git/config", "[core]\n")
	write("sub/.gitignore", "ignored.go\n")
	write("sub/package.go", "package sub\n")
	write("sub/ignored.go", "ignored\n")

	// A directory without files is not an entry of the project.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o750))
	return root
}

// entries returns the entries a project is expected to list, sorted by path.
func entries() []Entry {
	return []Entry{
		{Path: ".gitignore"},
		{Path: ".hidden", IsDir: true},
		{Path: ".hidden/secret.go"},
		{Path: "keep", IsDir: true},
		{Path: "keep/keep.log"},
		{Path: "main.go"},
		{Path: "sub", IsDir: true},
		{Path: "sub/.gitignore"},
		{Path: "sub/package.go"},
	}
}

// TestLister verifies the listing of a project.
func TestLister(t *testing.T) {
	t.Run("lists the files and the directories that hold them", func(t *testing.T) {
		listed, err := New(project(t)).Entries()

		require.NoError(t, err)
		require.Equal(t, entries(), listed)
	})

	t.Run("honors the ignore rules of every level", func(t *testing.T) {
		listed, err := New(project(t)).Entries()
		require.NoError(t, err)

		paths := make([]string, 0, len(listed))
		for _, entry := range listed {
			paths = append(paths, entry.Path)
		}

		// *.log matches at any depth; build/ takes the whole directory away and
		// the re-inclusion brings one log back; sub/.gitignore anchors its own
		// rule to the directory that declares it.
		require.NotContains(t, paths, "app.log")
		require.NotContains(t, paths, "build")
		require.NotContains(t, paths, "build/out.bin")
		require.NotContains(t, paths, "sub/ignored.go")
		require.Contains(t, paths, "keep/keep.log")
		require.Contains(t, paths, "sub/package.go")
	})

	t.Run("lists the hidden files but never the version control directory", func(t *testing.T) {
		listed, err := New(project(t)).Entries()
		require.NoError(t, err)

		paths := make([]string, 0, len(listed))
		for _, entry := range listed {
			paths = append(paths, entry.Path)
		}

		require.Contains(t, paths, ".hidden/secret.go", "a hidden file is listed")
		require.NotContains(t, paths, ".git")
		require.NotContains(t, paths, ".git/config")
	})

	t.Run("resolves a relative root", func(t *testing.T) {
		root := project(t)
		t.Chdir(root)

		listed, err := New(".").Entries()

		require.NoError(t, err)
		require.Equal(t, entries(), listed)
	})

	t.Run("reports a root that is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file.go")
		require.NoError(t, os.WriteFile(file, []byte("package main\n"), 0o600))

		_, err := New(file).Entries()

		require.ErrorContains(t, err, "not a directory")
	})

	t.Run("reports a root that does not exist", func(t *testing.T) {
		_, err := New(filepath.Join(t.TempDir(), "missing")).Entries()

		require.ErrorContains(t, err, "files: list")
	})
}
