package skill

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Constants of the SKILL.md format.
const (
	// frontmatterDelimiter opens and closes the YAML frontmatter of a
	// SKILL.md file.
	frontmatterDelimiter = "---"

	// nameKey and descriptionKey are the only frontmatter fields the catalog
	// publishes. Every other field of the specification, and every unknown
	// one, is ignored.
	nameKey        = "name"
	descriptionKey = "description"

	// maxNameRunes bounds the name of a skill. The specification recommends
	// 64 characters, so a longer name is reported and still offered.
	maxNameRunes = 64

	// maxDescriptionRunes bounds the description of a skill. The
	// specification recommends 1024 characters, so a longer description is
	// reported and still offered.
	maxDescriptionRunes = 1024

	// maxFrontmatterBytes bounds how much of a SKILL.md file is read while
	// looking for the closing delimiter. The file is read through a limit
	// reader of this size, so the bound holds for a file that never closes
	// its frontmatter and for a single line longer than the bound. A
	// well-formed file stops at its closing delimiter long before reaching
	// it.
	maxFrontmatterBytes = 64 << 10
)

// Problems reported for a SKILL.md file whose frontmatter cannot be used. They
// become the text of the diagnostic that reports the skill, never the failure
// of a run.
var (
	// errNoFrontmatter reports a file that does not open a frontmatter block.
	errNoFrontmatter = errors.New("the frontmatter is missing its opening ---")

	// errUnterminated reports a file whose frontmatter never closes.
	errUnterminated = errors.New("the frontmatter is missing its closing ---")

	// errShape reports a frontmatter that parses as something other than a
	// mapping, which no recovery can repair.
	errShape = errors.New("the frontmatter must be a YAML mapping")

	// errSyntax marks a frontmatter strict YAML cannot decode, which is the
	// one case the recovery pass exists for.
	errSyntax = errors.New("the frontmatter is not valid YAML")
)

// frontmatter holds the fields of a SKILL.md file the catalog publishes.
type frontmatter struct {
	// name is the declared name of the skill, its identity.
	name string

	// description explains what the skill does and when to use it.
	description string
}

// unusable reports whether the frontmatter declares no usable name or
// description, which is what makes a skill skipped instead of offered.
func (f frontmatter) unusable() bool {
	return f.name == "" || f.description == ""
}

// with returns the frontmatter with one of the fields the catalog publishes
// set, and reports whether the key named one. Every other key leaves the
// frontmatter untouched, which is what makes an unknown field harmless.
func (f frontmatter) with(key, value string) (frontmatter, bool) {
	switch key {
	case nameKey:
		f.name = value
	case descriptionKey:
		f.description = value
	default:
		return f, false
	}
	return f, true
}

// readFrontmatter returns the YAML frontmatter of a SKILL.md file, reading no
// further than the closing delimiter and never more than maxFrontmatterBytes.
// It fails when the file does not open a frontmatter block or does not close it
// before the bound, so the Markdown body of the file is never read and its size
// can never reach the caller.
func readFrontmatter(file io.Reader) ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(file, maxFrontmatterBytes))

	opening, _, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	// A byte order mark is a prefix of the file and not part of its first
	// line, so it is dropped before the delimiter is recognized.
	if strings.TrimSpace(strings.TrimPrefix(opening, "\uFEFF")) != frontmatterDelimiter {
		return nil, errNoFrontmatter
	}

	var block strings.Builder
	for {
		line, end, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(line) == frontmatterDelimiter {
			return []byte(block.String()), nil
		}
		if end {
			return nil, errUnterminated
		}
		block.WriteString(line)
		block.WriteByte('\n')
	}
}

// readLine returns the next line of a reader without its line ending, so a
// Windows file and a Unix one produce the same text. A read that reaches the
// end of the file sets end, which is how the caller tells the last line of a
// file that does not end with a newline from the absence of one.
func readLine(reader *bufio.Reader) (line string, end bool, err error) {
	text, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, fmt.Errorf("read frontmatter: %w", err)
	}

	text = strings.TrimSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\r")
	return text, errors.Is(err, io.EOF), nil
}

// parseFrontmatter reads the fields the catalog publishes out of a frontmatter
// and returns the problems that leave it unusable. An empty list means the
// frontmatter declares both fields.
//
// The parse is lenient on purpose, unlike the strict decoding this project
// applies to agent definitions: a skill is written for every client of the
// format, so an unknown field is ignored, a value that breaks strict YAML is
// recovered when it can be, and only a missing name or description is a
// problem.
func parseFrontmatter(data []byte) (frontmatter, []string) {
	parsed, err := decodeFrontmatter(data)
	if errors.Is(err, errSyntax) {
		if recovered, found := recoverFrontmatter(data); found {
			parsed, err = recovered, nil
		}
	}

	if err != nil {
		return frontmatter{}, []string{err.Error()}
	}

	problems := make([]string, 0, 2)
	if parsed.name == "" {
		problems = append(problems, "the name is missing or empty")
	}
	if parsed.description == "" {
		problems = append(problems, "the description is missing or empty")
	}
	return parsed, problems
}

// decodeFrontmatter decodes a frontmatter into the fields the catalog
// publishes. It fails when the text is not valid YAML or does not hold a
// mapping.
//
// The fields are read out of a node tree rather than decoded into a struct
// because that is what makes the parse lenient: an unknown field is simply
// never looked at, and a value is accepted only when it is a scalar string, so
// a name declared as a number or a boolean counts as absent instead of being
// coerced into a string.
func decodeFrontmatter(data []byte) (frontmatter, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return frontmatter{}, fmt.Errorf("%w: %w", errSyntax, err)
	}

	root := &document
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return frontmatter{}, errShape
	}

	var parsed frontmatter
	for index := 0; index+1 < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		// A field declared twice keeps the last occurrence, and an occurrence
		// that is not a scalar string clears the value the earlier one set.
		text, _ := scalarString(value)
		if updated, ok := parsed.with(key.Value, text); ok {
			parsed = updated
		}
	}
	return parsed, nil
}

// scalarString returns the trimmed value of a node that holds a scalar string,
// and reports whether it held one. A number, a boolean, a list and a map are
// not strings, which is what makes a name declared as a number count as absent.
func scalarString(node *yaml.Node) (string, bool) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", false
	}
	return strings.TrimSpace(node.Value), true
}

// recoverFrontmatter reads the fields of a frontmatter that strict YAML
// rejects, line by line, taking everything after the first colon of a name or
// description line as a literal value. It recovers the skills of other clients,
// whose descriptions often hold a colon that breaks YAML, and it reports
// whether it recovered any field at all.
//
// Only a line that starts at the first column is read, so the key of a nested
// mapping never passes for a field of the skill.
func recoverFrontmatter(data []byte) (frontmatter, bool) {
	var parsed frontmatter
	found := false

	for line := range strings.Lines(string(data)) {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		if updated, ok := parsed.with(strings.TrimSpace(key), value); ok {
			parsed, found = updated, true
		}
	}
	return parsed, found
}

// recommendations returns the ways a usable frontmatter departs from the
// recommendations of the format, in a fixed order so the same skill produces
// the same diagnostic on every run. The dir is the name of the directory that
// holds the SKILL.md file, which the format recommends the name to match.
func recommendations(f frontmatter, dir string) []string {
	problems := make([]string, 0, 5)

	if len([]rune(f.name)) > maxNameRunes {
		problems = append(
			problems,
			fmt.Sprintf("the name is longer than %d characters", maxNameRunes),
		)
	}
	if strings.ContainsFunc(f.name, func(r rune) bool { return !nameRune(r) }) {
		problems = append(
			problems,
			"the name uses characters outside lowercase letters, digits and hyphens",
		)
	}
	if strings.HasPrefix(f.name, "-") || strings.HasSuffix(f.name, "-") ||
		strings.Contains(f.name, "--") {
		problems = append(
			problems,
			"the name starts or ends with a hyphen or holds consecutive hyphens",
		)
	}
	if f.name != dir {
		problems = append(problems, fmt.Sprintf("the name does not match the directory %q", dir))
	}
	if len([]rune(f.description)) > maxDescriptionRunes {
		problems = append(
			problems,
			fmt.Sprintf("the description is longer than %d characters", maxDescriptionRunes),
		)
	}
	return problems
}

// nameRune reports whether a rune is allowed in the name of a skill by the
// specification: a lowercase letter, a digit or a hyphen.
func nameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}
