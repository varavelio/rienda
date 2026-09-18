//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/e2e/harness"
)

// TestRunSendsTheAgentDefinition verifies that the body of the definition
// becomes the system instruction and that its tools are declared to the model.
func TestRunSendsTheAgentDefinition(t *testing.T) {
	definition := coderAgent()
	definition.SystemPrompt = "# Rules\n\nAnswer in a single line."

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{definition},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
	result.RequireSuccess(t)

	chat := app.Provider().LastRequest(t).Chat(t)
	require.Equal(t, definition.SystemPrompt, chat.Messages[0].Text())

	require.Equal(t, []string{"shell"}, chat.ToolNames())
	require.Equal(t, "function", chat.Tools[0].Type)
	require.NotEmpty(t, chat.Tools[0].Function.Description)
	require.Contains(t, string(chat.Tools[0].Function.Parameters), `"command"`)
}

// TestRunDeclaresNoToolsWithoutThem verifies that an agent that declares no
// tool sends no tool declaration and leaves the tool settings untouched.
func TestRunDeclaresNoToolsWithoutThem(t *testing.T) {
	definition := coderAgent()
	definition.Tools = nil

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{definition},
	})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")
	result.RequireSuccess(t)

	chat := app.Provider().LastRequest(t).Chat(t)
	require.Empty(t, chat.Tools)
	require.Empty(t, chat.ToolChoice)
	require.Nil(t, chat.ParallelToolCalls)
}

// TestRunRejectsUnknownToolDefinitions verifies that an agent declaring a tool
// the harness does not provide fails before any request is sent.
func TestRunRejectsUnknownToolDefinitions(t *testing.T) {
	definition := coderAgent()
	definition.Tools = []string{"shell", "ghost"}

	app := harness.New(t, harness.Options{Agents: []harness.Agent{definition}})

	result := app.Run(t, "run", "-a", "coder", "-p", "hi")

	require.Equal(t, 1, result.Code)
	require.Contains(t, result.Stderr, `unknown tool "ghost"`)
	require.Contains(t, result.Stderr, "shell")
	require.Empty(t, app.Provider().Requests())
}

// TestRunAppliesTheDefinitionOfEveryAgent verifies that a run uses the
// definition of the agent it names.
func TestRunAppliesTheDefinitionOfEveryAgent(t *testing.T) {
	reviewer := coderAgent()
	reviewer.ID = "reviewer"
	reviewer.SystemPrompt = "You review code."
	reviewer.Tools = nil

	app := harness.New(t, harness.Options{
		Script: []harness.Turn{harness.Text("hello")},
		Agents: []harness.Agent{coderAgent(), reviewer},
	})

	result := app.Run(t, "run", "-a", "reviewer", "-p", "review this")
	result.RequireSuccess(t)

	chat := app.Provider().LastRequest(t).Chat(t)
	require.Equal(t, "You review code.", chat.Messages[0].Text())
	require.Empty(t, chat.Tools)
}

// TestRunReportsInvalidDefinitions verifies that every way of writing an
// unusable agent definition fails the run with a message that names the file
// and the problem.
func TestRunReportsInvalidDefinitions(t *testing.T) {
	cases := map[string]struct {
		definition harness.Agent
		message    string
	}{
		"without a description": {
			definition: harness.Agent{ID: "coder", Model: harness.FakeModelRef, SystemPrompt: "hi"},
			message:    "description is required",
		},
		"without a model": {
			definition: harness.Agent{ID: "coder", Description: "A test agent"},
			message:    "model is required",
		},
		"with a model reference that names no provider": {
			definition: modelFor("ghost"),
			message:    "must have the form provider/model",
		},
		"with an unknown generation setting": {
			definition: harness.Agent{ID: "coder", Raw: "---\n" +
				"description: A test agent\n" +
				"model: " + harness.FakeModelRef + "\n" +
				"temperature: 0.5\n" +
				"---\nYou answer briefly.\n"},
			message: "field temperature not found",
		},
		"without frontmatter": {
			definition: harness.Agent{ID: "coder", Raw: "You answer briefly.\n"},
			message:    "definition must start with a --- frontmatter block",
		},
		"with unterminated frontmatter": {
			definition: harness.Agent{ID: "coder", Raw: "---\ndescription: A test agent\n"},
			message:    "frontmatter is missing its closing ---",
		},
		"with an unknown frontmatter field": {
			definition: harness.Agent{ID: "coder", Raw: "---\n" +
				"description: A test agent\n" +
				"model: " + harness.FakeModelRef + "\n" +
				"unknown: value\n" +
				"---\nYou answer briefly.\n"},
			message: "field unknown not found",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			app := harness.New(t, harness.Options{Agents: []harness.Agent{testCase.definition}})

			result := app.Run(t, "run", "-a", "coder", "-p", "hi")

			require.Equal(t, 1, result.Code)
			require.Empty(t, result.Stdout)
			require.Contains(t, result.Stderr, app.AgentPath("coder"))
			require.Contains(t, result.Stderr, testCase.message)
			require.Empty(t, app.Provider().Requests())
		})
	}
}
