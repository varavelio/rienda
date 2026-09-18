//go:build e2e

package harness

import (
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Constants of the agent definition file format.
const (
	// agentExtension is the file extension of agent definitions.
	agentExtension = ".md"
	// frontmatterDelimiter separates the YAML frontmatter from the Markdown
	// body of a definition.
	frontmatterDelimiter = "---"
	// configFileName is the file name of the configuration file.
	configFileName = "config.yaml"
	// agentsDirName is the directory inside the rienda directory that holds
	// the agent definitions.
	agentsDirName = "agents"
)

// Agent describes one agent definition written into the global agents
// directory of an instance, in the exact Markdown format the binary reads.
type Agent struct {
	// ID is the file name of the definition without its extension, the value
	// runs name with --agent.
	ID string

	// Description explains what the agent does. The binary requires it.
	Description string

	// Model is the model reference the agent runs, in provider/model form.
	Model string

	// Tools lists the names of the tools available to the agent.
	Tools []string

	// Temperature overrides the sampling temperature of the model.
	Temperature *float64

	// TopP overrides the nucleus sampling of the model.
	TopP *float64

	// MaxTokens overrides the response token limit of the model.
	MaxTokens int

	// ReasoningEffort selects the reasoning effort level of the model.
	ReasoningEffort string

	// ReasoningBudgetTokens reserves a token budget for reasoning.
	ReasoningBudgetTokens int

	// SystemPrompt is the Markdown body of the definition.
	SystemPrompt string

	// Raw replaces the generated definition with the given contents, so tests
	// can exercise definitions the binary must reject. The ID still names the
	// file.
	Raw string
}

// FileName returns the name of the definition file inside the agents
// directory.
func (a Agent) FileName() string {
	return a.ID + agentExtension
}

// write stores the definition into the agents directory of an instance.
func (a Agent) write(t *testing.T, dir string) {
	t.Helper()
	if a.ID == "" {
		t.Fatal("harness: every agent declaration needs an id")
	}

	path := filepath.Join(dir, a.FileName())
	if a.Raw != "" {
		writeFile(t, path, a.Raw)
		return
	}

	frontmatter, err := yaml.Marshal(agentFrontmatter{
		Description:           a.Description,
		Model:                 a.Model,
		Tools:                 a.Tools,
		Temperature:           a.Temperature,
		TopP:                  a.TopP,
		MaxTokens:             a.MaxTokens,
		ReasoningEffort:       a.ReasoningEffort,
		ReasoningBudgetTokens: a.ReasoningBudgetTokens,
	})
	if err != nil {
		t.Fatalf("harness: encode agent %q: %v", a.ID, err)
	}

	contents := frontmatterDelimiter + "\n" + string(frontmatter) +
		frontmatterDelimiter + "\n" + a.SystemPrompt + "\n"
	writeFile(t, path, contents)
}

// agentFrontmatter mirrors the YAML frontmatter of an agent definition.
type agentFrontmatter struct {
	Description           string   `yaml:"description"`
	Model                 string   `yaml:"model"`
	Tools                 []string `yaml:"tools,omitempty"`
	Temperature           *float64 `yaml:"temperature,omitempty"`
	TopP                  *float64 `yaml:"top_p,omitempty"`
	MaxTokens             int      `yaml:"max_tokens,omitempty"`
	ReasoningEffort       string   `yaml:"reasoning_effort,omitempty"`
	ReasoningBudgetTokens int      `yaml:"reasoning_budget_tokens,omitempty"`
}
