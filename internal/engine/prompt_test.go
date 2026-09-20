package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
)

// writeProjectInstructions writes an AGENTS.md file with content into dir.
func writeProjectInstructions(t *testing.T, dir, content string) {
	t.Helper()

	path := filepath.Join(dir, projectInstructionsFile)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// TestSystemPrompt verifies the assembly of the system prompt from the agent
// definition and the instructions of the project the session runs in.
func TestSystemPrompt(t *testing.T) {
	// preamble is the literal introduction of the project instructions, so the
	// test catches any unintended change to the text sent to the model.
	const preamble = "The content below holds the instructions for working on " +
		"the current project. You MUST follow them."

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
			"be nice\n\n---\n\n"+preamble+"\n\nUse tabs for indentation.",
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
			require.Equal(t, preamble+"\n\nUse tabs.", system)
		},
	)

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
		require.NoError(t, os.Mkdir(filepath.Join(dir, projectInstructionsFile), 0o750))
		engine, _ := newTestEngine(t, Config{
			Agent:   agent.Agent{SystemPrompt: "be nice"},
			Workdir: dir,
		})

		_, err := engine.systemPrompt()
		require.ErrorContains(t, err, "read project instructions")
	})
}
