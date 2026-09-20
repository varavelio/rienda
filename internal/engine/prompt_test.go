package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
)

// wantTemplate mirrors the literal section the engine appends to the system
// prompt, so the test catches any unintended change to the text sent to the
// model. The first placeholder is the source file name and the second one is
// its content.
const wantTemplate = `CRITICAL: The project instructions loaded from %[1]s inside <project_instructions> are mandatory guidelines for this workspace. You MUST strictly adhere to them for every task. The ONLY exception is if the user explicitly instructs you to bypass or override a specific rule in the active conversation.

<project_instructions source="%[1]s">
%[2]s
</project_instructions>`

// wantSection returns the section the engine appends for a source file and its
// content.
func wantSection(source, content string) string {
	return fmt.Sprintf(wantTemplate, source, content)
}

// writeInstructions writes an instruction file with the given name and content
// into dir.
func writeInstructions(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// writeProjectInstructions writes the preferred instruction file, the first
// candidate of the lookup, with content into dir.
func writeProjectInstructions(t *testing.T, dir, content string) {
	t.Helper()

	writeInstructions(t, dir, projectInstructionsFiles[0], content)
}

// requireCaseSensitive skips the test when the file system of dir folds the
// case of file names, because the lookup cannot tell apart the case variants
// of the instruction file names there.
func requireCaseSensitive(t *testing.T, dir string) {
	t.Helper()

	probe := filepath.Join(dir, "CaseProbe")
	require.NoError(t, os.WriteFile(probe, nil, 0o600))
	if _, err := os.Stat(filepath.Join(dir, "caseprobe")); err == nil {
		t.Skip("file system is case insensitive")
	}
}

// TestSystemPrompt verifies the assembly of the system prompt from the agent
// definition and the instructions of the project the session runs in.
func TestSystemPrompt(t *testing.T) {
	t.Run("returns the agent prompt when the session runs in no directory", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{Agent: agent.Agent{SystemPrompt: "be nice"}})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Equal(t, "be nice", system)
	})

	t.Run("returns the agent prompt when the project has no instructions", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "be nice"},
			Workdir: t.TempDir(),
		})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Equal(t, "be nice", system)
	})

	t.Run("appends the project instructions to the agent prompt", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs for indentation.")
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "be nice"},
			Workdir: dir,
		})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Equal(t,
			"be nice\n\n---\n\n"+wantSection("AGENTS.md", "Use tabs for indentation."),
			system,
		)
	})

	t.Run(
		"returns only the project instructions when the agent prompt is empty",
		func(t *testing.T) {
			dir := t.TempDir()
			writeProjectInstructions(t, dir, "Use tabs.")
			engine, _ := newTestEngine(t, Config{Workdir: dir})

			system, err := engine.systemPrompt()
			require.NoError(t, err)
			require.Equal(t, wantSection("AGENTS.md", "Use tabs."), system)
		},
	)

	t.Run("trims the agent prompt", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs.")
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "  \n be nice \n "},
			Workdir: dir,
		})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Equal(t, "be nice\n\n---\n\n"+wantSection("AGENTS.md", "Use tabs."), system)
	})

	t.Run("treats empty project instructions as absent", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "  \n")
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "be nice"},
			Workdir: dir,
		})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Equal(t, "be nice", system)
	})

	t.Run("keeps the project instructions current across turns", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "First rule.")
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "be nice"},
			Workdir: dir,
		})

		before, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Contains(t, before, "First rule.")

		writeProjectInstructions(t, dir, "Second rule.")

		after, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Contains(t, after, "Second rule.")
		require.NotContains(t, after, "First rule.")
	})

	t.Run("fails when the project instructions cannot be read", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, projectInstructionsFiles[0]), 0o750))
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "be nice"},
			Workdir: dir,
		})

		_, err := engine.systemPrompt()
		require.ErrorContains(t, err, "read project instructions")
	})
}

// TestProjectInstructionsLookup verifies that the engine loads the first
// instruction file of the lookup order that exists in the working directory.
func TestProjectInstructionsLookup(t *testing.T) {
	t.Run("loads the first candidate when every file exists", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range projectInstructionsFiles {
			writeInstructions(t, dir, name, "from "+name)
		}
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Contains(t, system, `source="`+projectInstructionsFiles[0]+`"`)
		require.Contains(t, system, "from "+projectInstructionsFiles[0])
	})

	t.Run("loads the first candidate that exists", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, "CLAUDE.md", "claude rules")
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Contains(t, system, `source="CLAUDE.md"`)
		require.Contains(t, system, "claude rules")
	})

	t.Run("reports the loaded file in the source attribute", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructions(t, dir, "claude.md", "claude rules")
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Contains(t, system, `source="claude.md"`)
	})

	t.Run("ignores case variants of a candidate already found", func(t *testing.T) {
		dir := t.TempDir()
		requireCaseSensitive(t, dir)
		writeInstructions(t, dir, "agents.md", "lowercase rules")
		writeInstructions(t, dir, "CLAUDE.md", "claude rules")
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		system, err := engine.systemPrompt()
		require.NoError(t, err)
		require.Contains(t, system, "lowercase rules")
		require.NotContains(t, system, "claude rules")
	})
}
