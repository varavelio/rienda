//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// skillsApp returns an instance whose workspace declares the given skills and
// whose fake provider serves the given turns.
func skillsApp(t *testing.T, skills []harness.Skill, turns ...harness.Turn) *harness.Harness {
	t.Helper()
	return harness.New(t, harness.Options{
		Script: turns,
		Agents: []harness.Agent{coderAgent()},
		Skills: skills,
	})
}

// TestSkillsPublishesTheCatalog verifies that the skills of the workspace reach
// the model as a catalog in the system prompt, with their relative location.
func TestSkillsPublishesTheCatalog(t *testing.T) {
	app := skillsApp(t,
		[]harness.Skill{{
			Dir:         "pdf-processing",
			Name:        "pdf-processing",
			Description: "Extract PDF text, fill forms, merge files.",
			Body:        "Read the PDF, then extract its text.",
		}},
		harness.Text("hello"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.Equal(t, "hello\n", result.Stdout)
	require.NotContains(t, result.Stderr, "skill ",
		"a workspace with valid skills reports nothing")

	system := app.Provider().LastRequest(t).Chat(t).Messages[0].Text()
	require.Contains(t, system, "You answer briefly.")
	require.Contains(t, system, "<available_skills>")
	require.Contains(t, system, "<name>pdf-processing</name>")
	require.Contains(
		t,
		system,
		"<description>Extract PDF text, fill forms, merge files.</description>",
	)
	require.Contains(t, system, "<location>./.agents/skills/pdf-processing/SKILL.md</location>")
	require.Contains(t, system, "read the SKILL.md at the location it declares")
}

// TestSkillsReportsABrokenSkillOncePerRun verifies that a skill the binary
// cannot use is reported on standard error exactly once per run, while the
// valid skills are still published and standard output stays the answer.
func TestSkillsReportsABrokenSkillOncePerRun(t *testing.T) {
	app := skillsApp(t,
		[]harness.Skill{
			{
				Dir:         "good",
				Name:        "good",
				Description: "Handle things.",
				Body:        "Do the thing.",
			},
			{Dir: "broken", Raw: "---\nname: broken\n---\nbody\n"},
		},
		harness.ToolCall("shell", map[string]any{"command": "echo rienda-e2e"}),
		harness.ToolCall("shell", map[string]any{"command": "echo again"}),
		harness.Text("done"),
	)

	result := app.Run(t, "run", "-a", "coder", "-p", "go")

	result.RequireSuccess(t)
	require.Equal(t, "done\n", result.Stdout, "standard output carries only the answer")
	require.Contains(t, result.Stderr, "skill ./.agents/skills/broken/SKILL.md")
	require.Contains(t, result.Stderr, "the description is missing or empty")
	require.Equal(
		t,
		1,
		strings.Count(result.Stderr, "./.agents/skills/broken/SKILL.md"),
		"a run of several turns and tool batches reports the diagnostic exactly once",
	)

	system := app.Provider().LastRequest(t).Chat(t).Messages[0].Text()
	require.Contains(t, system, "<name>good</name>", "the valid skill is still published")
	require.NotContains(t, system, "<name>broken</name>")

	sessions := app.Sessions(t)
	require.Len(t, sessions, 1)
	for _, entry := range sessions[0].Entries {
		require.NotContains(t, entry.Text(), "the description is missing or empty",
			"a diagnostic never reaches the session file")
	}
}

// TestSkillsWithoutWorkspaceSkills verifies that a workspace that declares no
// skill leaves the system prompt exactly as it was.
func TestSkillsWithoutWorkspaceSkills(t *testing.T) {
	app := newApp(t, harness.Text("hello"))

	result := app.Run(t, "run", "-a", "coder", "-p", "say hello")

	result.RequireSuccess(t)
	require.NotContains(t, result.Stderr, "skill ")

	request := app.Provider().LastRequest(t).Chat(t)
	require.Equal(t, "You answer briefly.", request.Messages[0].Text())
	require.NotContains(t, request.Messages[0].Text(), "available_skills")
}

// TestSkillsReportsABrokenSkillOnEveryPrompt verifies that the binary keeps no
// memory of a diagnostic: a second prompt without a fix reports it again.
func TestSkillsReportsABrokenSkillOnEveryPrompt(t *testing.T) {
	app := skillsApp(t,
		[]harness.Skill{{Dir: "broken", Raw: "---\nname: broken\n---\nbody\n"}},
		harness.Text("first"),
		harness.Text("second"),
	)

	first := app.Run(t, "run", "-a", "coder", "-p", "first prompt")
	first.RequireSuccess(t)
	require.Contains(t, first.Stderr, "the description is missing or empty")

	second := app.Run(t, "run", "-s", first.SessionID(t), "-p", "second prompt")
	second.RequireSuccess(t)
	require.Contains(t, second.Stderr, "the description is missing or empty",
		"nothing is remembered between runs")
}

// TestSkillsSurviveACompaction verifies that the catalog is never summarized:
// the summarization request carries neither the catalog nor the project
// instructions, and the turn that follows carries the catalog again.
func TestSkillsSurviveACompaction(t *testing.T) {
	app := harness.New(t, harness.Options{
		Script: []harness.Turn{
			compactionTurn("first answer "),
			harness.Text("the checkpoint"),
			compactionTurn("second answer "),
		},
		Agents: []harness.Agent{coderAgent()},
		Skills: []harness.Skill{{
			Dir:         "pdfs",
			Name:        "pdfs",
			Description: "Handle PDFs.",
			Body:        "Read the PDF.",
		}},
		Catalog: map[string]int{"gpt-test": 300},
		Config:  compactionConfig(),
	})
	require.NoError(t, os.WriteFile(
		filepath.Join(app.Workdir(), "AGENTS.md"),
		[]byte("Use tabs for indentation.\n"),
		0o600,
	))

	first := app.Run(t, "run", "-a", "coder", "-p", "first prompt")
	first.RequireSuccess(t)

	second := app.Run(t, "run", "-s", first.SessionID(t), "-p", "second prompt")
	second.RequireSuccess(t)
	require.Contains(t, second.Stderr, "compaction: the conversation was compacted")

	requests := app.Provider().Requests()
	require.Len(t, requests, 3)

	firstTurn := requests[0].Chat(t)
	require.Contains(t, firstTurn.Messages[0].Text(), "Use tabs for indentation.",
		"an ordinary turn carries the project instructions")
	require.Contains(t, firstTurn.Messages[0].Text(), "available_skills")

	summary := requests[1].Chat(t)
	require.False(t, summary.Stream, "the summary is not streamed")
	require.NotContains(t, summary.Messages[0].Text(), "available_skills")
	require.NotContains(
		t,
		joinedMessages(summary),
		"<location>./.agents/skills/pdfs/SKILL.md</location>",
		"a compaction request never carries the catalog",
	)

	continued := requests[2].Chat(t)
	require.Contains(t, continued.Messages[0].Text(), "<available_skills>",
		"the turn that follows the compaction publishes the catalog again")
	require.Contains(t, continued.Messages[0].Text(), "<name>pdfs</name>")
}

// TestSkillsPickUpWorkspaceChangesBetweenPrompts verifies that a skill created,
// edited or removed while a session is open is reflected by the next prompt:
// the workspace is read again when each run opens, so the system prompt of the
// new run carries what is on disk right now.
func TestSkillsPickUpWorkspaceChangesBetweenPrompts(t *testing.T) {
	app := skillsApp(t,
		[]harness.Skill{{Dir: "one", Name: "one", Description: "First description."}},
		harness.Text("first"),
		harness.Text("second"),
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(app.Workdir(), "AGENTS.md"), []byte("First rule.\n"), 0o600,
	))

	first := app.Run(t, "run", "-a", "coder", "-p", "first prompt")
	first.RequireSuccess(t)
	firstSystem := app.Provider().Requests()[0].Chat(t).Messages[0].Text()
	require.Contains(t, firstSystem, "First description.")
	require.Contains(t, firstSystem, "First rule.")

	// The user edits the skill, adds another one and edits the project
	// instructions, all while the session is open.
	require.NoError(t, os.WriteFile(
		filepath.Join(app.Workdir(), ".agents", "skills", "one", "SKILL.md"),
		[]byte("---\nname: one\ndescription: Second description.\n---\nbody\n"), 0o600,
	))
	require.NoError(t, os.MkdirAll(
		filepath.Join(app.Workdir(), ".agents", "skills", "two"), 0o750,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(app.Workdir(), ".agents", "skills", "two", "SKILL.md"),
		[]byte("---\nname: two\ndescription: Brand new.\n---\nbody\n"), 0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(app.Workdir(), "AGENTS.md"), []byte("Second rule.\n"), 0o600,
	))

	second := app.Run(t, "run", "-s", first.SessionID(t), "-p", "second prompt")
	second.RequireSuccess(t)

	secondSystem := app.Provider().Requests()[1].Chat(t).Messages[0].Text()
	require.Contains(t, secondSystem, "Second description.",
		"the edited description of a skill reaches the next prompt")
	require.Contains(t, secondSystem, "Brand new.",
		"a skill added while the session is open reaches the next prompt")
	require.Contains(t, secondSystem, "Second rule.",
		"the edited project instructions reach the next prompt")
	require.NotContains(t, secondSystem, "First description.")
	require.NotContains(t, secondSystem, "First rule.")
}
