package slash

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

// frontmatter mirrors the YAML frontmatter of a command definition.
type frontmatter struct {
	Description string `yaml:"description"`
}

// Parse validates and converts the raw contents of a command definition into a
// Command. The id is the command identifier, normally the file name without
// its extension.
func Parse(id string, data []byte) (Command, error) {
	if !ValidID(id) {
		return Command{}, fmt.Errorf("slash: invalid command id %q", id)
	}

	header, body, err := splitFrontmatter(data)
	if err != nil {
		return Command{}, fmt.Errorf("slash %q: %w", id, err)
	}

	var meta frontmatter
	decoder := yaml.NewDecoder(bytes.NewReader(header))
	decoder.KnownFields(true)
	if err := decoder.Decode(&meta); err != nil {
		if errors.Is(err, io.EOF) {
			return Command{}, fmt.Errorf("slash %q: frontmatter is empty", id)
		}
		return Command{}, fmt.Errorf("slash %q: invalid frontmatter: %w", id, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Command{}, fmt.Errorf("slash %q: frontmatter must hold a single YAML document", id)
	}

	meta.Description = strings.TrimSpace(meta.Description)
	if meta.Description == "" {
		return Command{}, fmt.Errorf("slash %q: description is required", id)
	}

	template := strings.TrimSpace(string(body))
	if template == "" {
		return Command{}, fmt.Errorf("slash %q: template is empty", id)
	}

	return Command{
		ID:          id,
		Description: meta.Description,
		Template:    template,
	}, nil
}

// splitFrontmatter separates the YAML frontmatter of a command definition from
// its Markdown body. It tolerates a leading byte order mark and Windows line
// endings.
func splitFrontmatter(data []byte) (header, body []byte, err error) {
	text := strings.TrimPrefix(string(data), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
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
