package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
)

// projectSection is the opening of the section the instructions package
// renders, which is enough for the engine test to locate it in the composition
// without reproducing its whole template.
const projectSection = `<project_instructions source="AGENTS.md">`

// skillsSection is the opening of the catalog the skill package renders for a
// workspace that declares a skill.
const skillsSection = "<available_skills>"

// writeProjectInstructions writes the preferred instruction file of a project
// with content into dir, which is the file the instructions package loads
// first.
func writeProjectInstructions(t *testing.T, dir, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o600))
}

// writeSkill writes a SKILL.md file of a skill into a workspace.
func writeSkill(t *testing.T, dir, name, contents string) {
	t.Helper()

	path := filepath.Join(dir, ".agents", "skills", name, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

// skillFile returns the contents of a SKILL.md file that declares the fields
// the catalog publishes.
func skillFile(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
}

// TestSystemPrompt verifies the assembly of the system prompt from the agent
// definition, the instructions of the project the session runs in and the
// skills of the workspace.
func TestSystemPrompt(t *testing.T) {
	t.Run("returns the agent prompt when the session runs in no directory", func(t *testing.T) {
		engine, _ := newTestEngine(
			t,
			Config{Agents: []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}}},
		)

		system, diagnostics, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Equal(t, "be nice", system)
		require.Empty(t, diagnostics)
	})

	t.Run("returns the agent prompt when the project declares nothing", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: t.TempDir(),
		})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Equal(t, "be nice", system)
	})

	t.Run("appends the project instructions to the agent prompt", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs for indentation.")
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.True(t, strings.HasPrefix(system, "be nice"+sectionSeparator))
		require.Contains(t, system, projectSection)
		require.Contains(t, system, "Use tabs for indentation.")
	})

	t.Run(
		"returns only the project instructions when the agent prompt is empty",
		func(t *testing.T) {
			dir := t.TempDir()
			writeProjectInstructions(t, dir, "Use tabs.")
			engine, _ := newTestEngine(t, Config{Workdir: dir})

			system, _, err := engine.systemPrompt(engine.agents["coder"])

			require.NoError(t, err)
			require.Contains(t, system, projectSection)
			require.NotContains(t, system, sectionSeparator, "one section carries no separator")
		},
	)

	t.Run("trims the agent prompt", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs.")
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "  \n be nice \n "}},
			Workdir: dir,
		})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.True(t, strings.HasPrefix(system, "be nice"+sectionSeparator))
	})

	t.Run("treats empty project instructions as absent", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "  \n")
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Equal(t, "be nice", system)
	})

	t.Run("keeps the project instructions current across runs", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "First rule.")
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		before, _, err := engine.systemPrompt(engine.agents["coder"])
		require.NoError(t, err)
		require.Contains(t, before, "First rule.")

		writeProjectInstructions(t, dir, "Second rule.")

		after, _, err := engine.systemPrompt(engine.agents["coder"])
		require.NoError(t, err)
		require.Contains(t, after, "Second rule.")
		require.NotContains(t, after, "First rule.")
	})

	t.Run("fails when an instruction file cannot be read", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "AGENTS.md"), 0o750))
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		_, _, err := engine.systemPrompt(engine.agents["coder"])

		require.ErrorContains(t, err, "read AGENTS.md")
	})
}

// TestSystemPromptSkills verifies the composition of the three sections of the
// system prompt.
func TestSystemPromptSkills(t *testing.T) {
	t.Run("appends the skills section after the project instructions", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectInstructions(t, dir, "Use tabs.")
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		system, diagnostics, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Empty(t, diagnostics)
		require.Equal(t, 2, strings.Count(system, sectionSeparator))
		require.Less(t, strings.Index(system, "be nice"), strings.Index(system, projectSection))
		require.Less(t, strings.Index(system, projectSection), strings.Index(system, skillsSection))
	})

	t.Run("returns only the skills section when nothing else carries content", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Contains(t, system, skillsSection)
		require.NotContains(t, system, sectionSeparator, "no leading separator is written")
	})

	t.Run("skips a missing section without doubling a separator", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{
			Agents:  []agent.Agent{{ID: "coder", SystemPrompt: "be nice"}},
			Workdir: dir,
		})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Contains(t, system, skillsSection)
		require.Equal(t, 1, strings.Count(system, sectionSeparator))
	})

	t.Run("returns an empty prompt when every section is empty", func(t *testing.T) {
		engine, _ := newTestEngine(t, Config{Workdir: t.TempDir()})

		system, _, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Empty(t, system)
	})

	t.Run("collects the diagnostics of a broken skill", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "broken", "---\nname: broken\n---\nbody\n")
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		_, diagnostics, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Equal(t, []string{
			"./.agents/skills/broken/SKILL.md: the description is missing or empty",
		}, diagnostics)
	})

	t.Run("reports no diagnostic for a workspace of usable skills", func(t *testing.T) {
		dir := t.TempDir()
		writeSkill(t, dir, "pdfs", skillFile("pdfs", "Handle PDFs."))
		engine, _ := newTestEngine(t, Config{Workdir: dir})

		system, diagnostics, err := engine.systemPrompt(engine.agents["coder"])

		require.NoError(t, err)
		require.Empty(t, diagnostics)
		require.Contains(t, system, "<name>pdfs</name>")
	})
}

// TestJoinSections verifies the separator between the sections that carry
// content.
func TestJoinSections(t *testing.T) {
	t.Run("joins every present section", func(t *testing.T) {
		require.Equal(t, "a"+sectionSeparator+"b"+sectionSeparator+"c", joinSections("a", "b", "c"))
	})

	t.Run("writes no separator around an absent section", func(t *testing.T) {
		require.Equal(t, "a"+sectionSeparator+"c", joinSections("a", "", "c"))
	})

	t.Run("writes no separator when only one section is present", func(t *testing.T) {
		require.Equal(t, "b", joinSections("", "b", ""))
	})

	t.Run("returns nothing when no section is present", func(t *testing.T) {
		require.Empty(t, joinSections("", "", ""))
	})
}
