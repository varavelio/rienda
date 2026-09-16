package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// frontmatterDelimiter separates the YAML frontmatter from the Markdown body.
const frontmatterDelimiter = "---"

// frontmatter mirrors the YAML frontmatter of an agent definition.
type frontmatter struct {
	Description           string   `yaml:"description"`
	Model                 string   `yaml:"model"`
	Tools                 []string `yaml:"tools"`
	Temperature           *float64 `yaml:"temperature"`
	TopP                  *float64 `yaml:"top_p"`
	MaxTokens             int      `yaml:"max_tokens"`
	Stop                  []string `yaml:"stop"`
	ReasoningEffort       string   `yaml:"reasoning_effort"`
	ReasoningBudgetTokens int      `yaml:"reasoning_budget_tokens"`
}

// Parse validates and converts the raw contents of an agent definition into an
// Agent. The id is the agent identifier, normally the file name without its
// extension.
func Parse(id string, data []byte) (Agent, error) {
	if !ValidID(id) {
		return Agent{}, fmt.Errorf("agent: invalid agent id %q", id)
	}

	header, body, err := splitFrontmatter(data)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %q: %w", id, err)
	}

	var meta frontmatter
	decoder := yaml.NewDecoder(bytes.NewReader(header))
	decoder.KnownFields(true)
	if err := decoder.Decode(&meta); err != nil {
		if errors.Is(err, io.EOF) {
			return Agent{}, fmt.Errorf("agent %q: frontmatter is empty", id)
		}
		return Agent{}, fmt.Errorf("agent %q: invalid frontmatter: %w", id, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Agent{}, fmt.Errorf("agent %q: frontmatter must hold a single YAML document", id)
	}

	if err := meta.normalize(); err != nil {
		return Agent{}, fmt.Errorf("agent %q: %w", id, err)
	}

	return Agent{
		ID:                    id,
		Description:           meta.Description,
		Model:                 meta.Model,
		Tools:                 meta.Tools,
		Temperature:           meta.Temperature,
		TopP:                  meta.TopP,
		MaxTokens:             meta.MaxTokens,
		Stop:                  meta.Stop,
		ReasoningEffort:       meta.ReasoningEffort,
		ReasoningBudgetTokens: meta.ReasoningBudgetTokens,
		SystemPrompt:          strings.TrimSpace(string(body)),
	}, nil
}

// splitFrontmatter separates the YAML frontmatter of an agent definition from
// its Markdown body. It tolerates a leading byte order mark and Windows line
// endings.
func splitFrontmatter(data []byte) (header, body []byte, err error) {
	text := strings.TrimPrefix(string(data), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return nil, nil, errors.New("definition must start with a --- frontmatter block")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != frontmatterDelimiter {
			continue
		}
		header = []byte(strings.Join(lines[1:i], "\n"))
		body = []byte(strings.Join(lines[i+1:], "\n"))
		return header, body, nil
	}
	return nil, nil, errors.New("frontmatter is missing its closing ---")
}

// normalize trims and validates the frontmatter fields in place.
func (m *frontmatter) normalize() error {
	m.Description = strings.TrimSpace(m.Description)
	if m.Description == "" {
		return errors.New("description is required")
	}

	providerName, modelName, found := strings.Cut(m.Model, "/")
	providerName, modelName = strings.TrimSpace(providerName), strings.TrimSpace(modelName)
	if !found || providerName == "" || modelName == "" {
		return fmt.Errorf("model %q must have the form provider/model", m.Model)
	}
	m.Model = providerName + "/" + modelName

	for i, tool := range m.Tools {
		m.Tools[i] = strings.TrimSpace(tool)
		if m.Tools[i] == "" {
			return errors.New("tools must not contain empty names")
		}
	}

	if m.Temperature != nil && (*m.Temperature < 0 || *m.Temperature > 2) {
		return fmt.Errorf("temperature %v must be between 0 and 2", *m.Temperature)
	}
	if m.TopP != nil && (*m.TopP < 0 || *m.TopP > 1) {
		return fmt.Errorf("top_p %v must be between 0 and 1", *m.TopP)
	}
	if m.MaxTokens < 0 {
		return fmt.Errorf("max_tokens %d must not be negative", m.MaxTokens)
	}

	for i, stop := range m.Stop {
		m.Stop[i] = strings.TrimSpace(stop)
		if m.Stop[i] == "" {
			return errors.New("stop must not contain empty sequences")
		}
	}

	m.ReasoningEffort = strings.ToLower(strings.TrimSpace(m.ReasoningEffort))
	if m.ReasoningBudgetTokens < 0 {
		return fmt.Errorf(
			"reasoning_budget_tokens %d must not be negative",
			m.ReasoningBudgetTokens,
		)
	}
	return nil
}
