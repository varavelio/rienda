package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeSkill writes a SKILL.md file for a skill into a workspace.
func writeSkill(t *testing.T, workdir, dir, contents string) {
	t.Helper()

	writeFile(t, filepath.Join(workdir, ".agents", "skills", dir, skillFile), contents)
}

// writeFile writes a file, creating the directories that hold it.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

// validSkill returns the contents of a SKILL.md file that declares both fields
// the catalog publishes.
func validSkill(name, description string) string {
	return frontmatterFile(
		nameKey + ": " + name + "\n" + descriptionKey + ": " + description + "\n",
	)
}

// TestDiscover verifies the discovery of the skills of a workspace.
func TestDiscover(t *testing.T) {
	t.Run("yields nothing without a workspace", func(t *testing.T) {
		require.Equal(t, Result{}, Discover(""))
	})

	t.Run("yields nothing when the workspace declares no skills directory", func(t *testing.T) {
		require.Equal(t, Result{}, Discover(t.TempDir()))
	})

	t.Run("yields nothing when the skills directory is empty", func(t *testing.T) {
		workdir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(workdir, skillsDir), 0o750))

		require.Equal(t, Result{}, Discover(workdir))
	})

	t.Run("reports a skills directory that cannot be read", func(t *testing.T) {
		workdir := t.TempDir()
		writeFile(t, filepath.Join(workdir, skillsDir), "not a directory")

		result := Discover(workdir)

		require.Empty(t, result.Section)
		require.Len(t, result.Diagnostics, 1)
		require.Contains(t, result.Diagnostics[0], skillsLocation)
	})

	t.Run("publishes a skill of the workspace", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "pdfs", validSkill("pdfs", "Handle PDFs."))

		result := Discover(workdir)

		require.Empty(t, result.Diagnostics)
		require.Contains(t, result.Section, "<name>pdfs</name>")
		require.Contains(t, result.Section, "<description>Handle PDFs.</description>")
		require.Contains(t, result.Section, "<location>./.agents/skills/pdfs/SKILL.md</location>")
	})

	t.Run("ignores every entry that is not a skill", func(t *testing.T) {
		workdir := t.TempDir()
		base := filepath.Join(workdir, skillsDir)
		writeFile(t, filepath.Join(base, "README.md"), "notes")
		writeFile(t, filepath.Join(base, "notes", "other.md"), "notes")
		writeSkill(t, workdir, ".hidden", validSkill("hidden", "x"))
		writeFile(t, filepath.Join(base, "nested", "inner", skillFile), validSkill("inner", "x"))
		require.NoError(t, os.MkdirAll(filepath.Join(base, "empty-dir"), 0o750))

		result := Discover(workdir)

		require.Empty(t, result.Section)
		require.Empty(t, result.Diagnostics)
	})

	t.Run("ignores a SKILL.md that is a directory", func(t *testing.T) {
		workdir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(workdir, skillsDir, "pdfs", skillFile), 0o750))

		require.Equal(t, Result{}, Discover(workdir))
	})

	t.Run("orders the catalog by directory name", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "zeta", validSkill("zeta", "last"))
		writeSkill(t, workdir, "alpha", validSkill("alpha", "first"))

		result := Discover(workdir)

		require.Less(t,
			strings.Index(result.Section, "<name>alpha</name>"),
			strings.Index(result.Section, "<name>zeta</name>"),
		)
	})

	t.Run("keeps the later directory of a duplicate name", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "alpha", validSkill("shared", "from alpha"))
		writeSkill(t, workdir, "beta", validSkill("shared", "from beta"))

		result := Discover(workdir)

		require.Contains(t, result.Section, "<name>shared</name>")
		require.Contains(t, result.Section, "<description>from beta</description>")
		require.NotContains(t, result.Section, "from alpha")
		require.Len(t, result.Diagnostics, 3)
		require.Equal(
			t,
			"./.agents/skills/alpha/SKILL.md: shadowed by ./.agents/skills/beta/SKILL.md",
			result.Diagnostics[0],
			"the shadowed skill is reported",
		)
	})

	t.Run("skips an unusable skill and offers the rest", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "broken", frontmatterFile(nameKey+": broken\n"))
		writeSkill(t, workdir, "good", validSkill("good", "Handle things."))

		result := Discover(workdir)

		require.Contains(t, result.Section, "<name>good</name>")
		require.NotContains(t, result.Section, "broken")
		require.Equal(t, []string{
			"./.agents/skills/broken/SKILL.md: the description is missing or empty",
		}, result.Diagnostics)
	})

	t.Run("reports one diagnostic per skill in a deterministic order", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "second", "not a frontmatter at all\n")
		writeSkill(t, workdir, "first", frontmatterFile(nameKey+": first\n"))
		writeSkill(t, workdir, "third", validSkill("third", "x"))

		result := Discover(workdir)

		require.Equal(t, []string{
			"./.agents/skills/first/SKILL.md: the description is missing or empty",
			"./.agents/skills/second/SKILL.md: the frontmatter is missing its opening ---",
		}, result.Diagnostics)
	})

	t.Run("joins every problem of a skill into one diagnostic", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "PDF_Handler", validSkill("PDF_Handler", "Handle PDFs."))

		result := Discover(workdir)

		require.Contains(t, result.Section, "<name>PDF_Handler</name>")
		require.Equal(t, []string{
			"./.agents/skills/PDF_Handler/SKILL.md: " +
				"the name uses characters outside lowercase letters, digits and hyphens",
		}, result.Diagnostics)
	})

	t.Run("reports a skill directory that cannot be read", func(t *testing.T) {
		workdir := t.TempDir()
		base := filepath.Join(workdir, skillsDir)
		require.NoError(t, os.MkdirAll(base, 0o750))
		require.NoError(
			t,
			os.Symlink(filepath.Join(base, "ghost"), filepath.Join(base, "dangling")),
		)

		result := Discover(workdir)

		require.Empty(t, result.Section)
		require.Len(t, result.Diagnostics, 1)
		require.Contains(t, result.Diagnostics[0], "./.agents/skills/dangling/SKILL.md")
	})

	t.Run("follows a symlinked skill directory", func(t *testing.T) {
		workdir := t.TempDir()
		base := filepath.Join(workdir, skillsDir)
		require.NoError(t, os.MkdirAll(base, 0o750))
		outside := filepath.Join(t.TempDir(), "shared")
		writeFile(t, filepath.Join(outside, skillFile), validSkill("linked", "Shared skill."))
		require.NoError(t, os.Symlink(outside, filepath.Join(base, "linked")))

		result := Discover(workdir)

		require.Empty(t, result.Diagnostics)
		require.Contains(t, result.Section, "<name>linked</name>")
		require.Contains(t, result.Section, "<location>./.agents/skills/linked/SKILL.md</location>")
	})

	t.Run("reads the skills again on every discovery", func(t *testing.T) {
		workdir := t.TempDir()
		writeSkill(t, workdir, "first", validSkill("first", "First skill."))
		require.Contains(t, Discover(workdir).Section, "<name>first</name>")

		writeSkill(t, workdir, "second", validSkill("second", "Second skill."))
		require.Contains(t, Discover(workdir).Section, "<name>second</name>")

		require.NoError(t, os.RemoveAll(filepath.Join(workdir, skillsDir, "first")))
		result := Discover(workdir)
		require.NotContains(t, result.Section, "<name>first</name>")
		require.Contains(t, result.Section, "<name>second</name>")
	})
}
