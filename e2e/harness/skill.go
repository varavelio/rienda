//go:build e2e

package harness

import (
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Constants of the skill directory layout.
const (
	// skillFileName is the name of the file that declares a skill.
	skillFileName = "SKILL.md"
	// skillsDirName is the directory inside the workspace that holds the
	// skills of a project.
	skillsDirName = "skills"
	// workspaceAgentsDirName is the directory inside the workspace that holds
	// the project-scoped resources of the Agent Skills convention.
	workspaceAgentsDirName = ".agents"
)

// Skill describes one skill written into the workspace of an instance, in the
// exact format the binary reads. The package reproduces the format
// independently of the implementation, so the suite validates the contract
// between the two.
type Skill struct {
	// Dir is the name of the directory that holds the SKILL.md file. It is the
	// value that orders the catalog and resolves a duplicate.
	Dir string

	// Name is the name the frontmatter declares.
	Name string

	// Description explains what the skill does and when to use it.
	Description string

	// Body is the Markdown body of the file, which the binary never reads
	// while discovering the skill.
	Body string

	// Raw replaces the generated file with the given contents, so tests can
	// exercise skills the binary must reject or repair. The Dir still names
	// the directory.
	Raw string
}

// write stores the skill into the workspace of an instance.
func (s Skill) write(t *testing.T, workdir string) {
	t.Helper()
	if s.Dir == "" {
		t.Fatal("harness: every skill declaration needs a dir")
	}

	path := filepath.Join(workdir, workspaceAgentsDirName, skillsDirName, s.Dir, skillFileName)
	if s.Raw != "" {
		writeFile(t, path, s.Raw)
		return
	}

	frontmatter, err := yaml.Marshal(skillFrontmatter{
		Name:        s.Name,
		Description: s.Description,
	})
	if err != nil {
		t.Fatalf("harness: encode skill %q: %v", s.Dir, err)
	}

	contents := frontmatterDelimiter + "\n" + string(frontmatter) +
		frontmatterDelimiter + "\n" + s.Body
	writeFile(t, path, contents)
}

// skillFrontmatter mirrors the YAML frontmatter of a SKILL.md file.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}
