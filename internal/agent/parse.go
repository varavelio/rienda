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
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
	Hooks       []string `yaml:"hooks"`
	Config      any      `yaml:"config"`
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

	config, err := normalizeConfig(meta.Config)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %q: %w", id, err)
	}
	if err := meta.normalize(); err != nil {
		return Agent{}, fmt.Errorf("agent %q: %w", id, err)
	}

	return Agent{
		ID:           id,
		Description:  meta.Description,
		Tools:        meta.Tools,
		Hooks:        meta.Hooks,
		Config:       config,
		SystemPrompt: strings.TrimSpace(string(body)),
	}, nil
}

// normalizeConfig validates the free config block of an agent definition: an
// absent block stays absent, and a present one must be a map of maps.
func normalizeConfig(raw any) (map[string]map[string]any, error) {
	if raw == nil {
		//nolint:nilnil // an absent block stays absent by contract.
		return nil, nil
	}
	outer, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("config must be an object of objects")
	}
	config := make(map[string]map[string]any, len(outer))
	for name, values := range outer {
		inner, ok := values.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config %q must be an object", name)
		}
		config[name] = inner
	}
	return config, nil
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

	for i, tool := range m.Tools {
		m.Tools[i] = strings.TrimSpace(tool)
		if m.Tools[i] == "" {
			return errors.New("tools must not contain empty names")
		}
	}

	for i, hook := range m.Hooks {
		m.Hooks[i] = strings.TrimSpace(hook)
		if m.Hooks[i] == "" {
			return errors.New("hooks must not contain empty names")
		}
	}
	return nil
}
