package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// wantTemplate mirrors the literal section appended to the system prompt, so
// the test catches any unintended change to the text sent to the model. The
// first placeholder is the source file name and the second one is its content.
const wantTemplate = `CRITICAL: The project instructions loaded from %[1]s inside <project_instructions> are mandatory guidelines for this workspace. You MUST strictly adhere to them for every task. The ONLY exception is if the user explicitly instructs you to bypass or override a specific rule in the active conversation.

<project_instructions source="%[1]s">
%[2]s
</project_instructions>`

// wantSection returns the section rendered for a source file and its content.
func wantSection(source, content string) string {
	return fmt.Sprintf(wantTemplate, source, content)
}

// writeInstructions writes an instruction file with the given name and content
// into dir.
func writeInstructions(t *testing.T, dir, name, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

// requireCaseSensitive skips the test when the file system of dir folds the
// case of file names, because the lookup cannot tell apart the case variants of
// the instruction file names there.
func requireCaseSensitive(t *testing.T, dir string) {
	t.Helper()

	probe := filepath.Join(dir, "CaseProbe")
	require.NoError(t, os.WriteFile(probe, nil, 0o600))
	if _, err := os.Stat(filepath.Join(dir, "caseprobe")); err == nil {
		t.Skip("file system is case insensitive")
	}
}

// TestSection verifies the section a workspace of instructions yields.
func TestSection(t *testing.T) {
	t.Run("returns nothing without a workspace", func(t *testing.T) {
		section, err := Section("")

		require.NoError(t, err)
		require.Empty(t, section)
	})

	t.Run("returns nothing when the project declares no instructions", func(t *testing.T) {
		section, err := Section(t.TempDir())

		require.NoError(t, err)
		require.Empty(t, section)
	})

	t.Run("returns the instructions of the preferred file", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, files[0], "Use tabs.")

		section, err := Section(dir)

		require.NoError(t, err)
		require.Equal(t, wantSection(files[0], "Use tabs."), section)
	})

	t.Run("trims the instructions", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, files[0], "  \n Use tabs. \n ")

		section, err := Section(dir)

		require.NoError(t, err)
		require.Equal(t, wantSection(files[0], "Use tabs."), section)
	})

	t.Run("treats empty instructions as absent", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, files[0], "  \n")

		section, err := Section(dir)

		require.NoError(t, err)
		require.Empty(t, section)
	})

	t.Run("is read again on every call", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, files[0], "First rule.")
		before, err := Section(dir)
		require.NoError(t, err)
		require.Contains(t, before, "First rule.")

		writeInstructions(t, dir, files[0], "Second rule.")

		after, err := Section(dir)
		require.NoError(t, err)
		require.Contains(t, after, "Second rule.")
		require.NotContains(t, after, "First rule.")
	})

	t.Run("fails when an instruction file cannot be read", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, files[0]), 0o750))

		_, err := Section(dir)

		require.ErrorContains(t, err, "read AGENTS.md")
	})
}

// TestLookup verifies that the first instruction file of the lookup order that
// exists in the workspace is the one loaded.
func TestLookup(t *testing.T) {
	t.Run("loads the first candidate when every file exists", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range files {
			writeInstructions(t, dir, name, "from "+name)
		}

		section, err := Section(dir)

		require.NoError(t, err)
		require.Contains(t, section, `source="`+files[0]+`"`)
		require.Contains(t, section, "from "+files[0])
	})

	t.Run("loads the first candidate that exists", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, "CLAUDE.md", "claude rules")

		section, err := Section(dir)

		require.NoError(t, err)
		require.Contains(t, section, `source="CLAUDE.md"`)
		require.Contains(t, section, "claude rules")
	})

	t.Run("ignores case variants of a candidate already found", func(t *testing.T) {
		dir := t.TempDir()
		requireCaseSensitive(t, dir)
		writeInstructions(t, dir, "agents.md", "lowercase rules")
		writeInstructions(t, dir, "CLAUDE.md", "claude rules")

		section, err := Section(dir)

		require.NoError(t, err)
		require.Contains(t, section, "lowercase rules")
		require.NotContains(t, section, "claude rules")
	})
}
