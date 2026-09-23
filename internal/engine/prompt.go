package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/varavelio/rienda/internal/agent"
)

// projectInstructionsFiles lists the names of the files that may hold the
// instructions of a project, in lookup order. The first one that exists in the
// working directory is the one loaded, so a project that uses any of the
// common conventions is honored.
var projectInstructionsFiles = []string{
	"AGENTS.md",
	"agents.md",
	"AGENTS.MD",
	"CLAUDE.md",
	"claude.md",
	"CLAUDE.MD",
}

// sectionSeparator divides two sections of the system prompt, whatever they
// are: the system prompt of the agent, the instructions of the project and the
// skills of the workspace.
const sectionSeparator = "\n\n---\n\n"

// projectInstructionsTemplate wraps the project instructions in the section
// appended to the system prompt. The first placeholder is the name of the file
// the instructions were read from and the second one is its content.
const projectInstructionsTemplate = `
CRITICAL: The project instructions loaded from %[1]s inside <project_instructions> are mandatory guidelines for this workspace. You MUST strictly adhere to them for every task. The ONLY exception is if the user explicitly instructs you to bypass or override a specific rule in the active conversation.

<project_instructions source="%[1]s">
%[2]s
</project_instructions>
`

// projectInstructions holds the instructions of the project a session runs in
// together with the file they were read from.
type projectInstructions struct {
	// Source is the name of the file the instructions were read from, empty
	// when the project declares none.
	Source string

	// Content is the trimmed body of the instructions, empty when the project
	// declares none.
	Content string
}

// section returns the instructions of the project as the section appended to
// the system prompt, or an empty string when the project declares none.
func (p projectInstructions) section() string {
	if p.Content == "" {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf(projectInstructionsTemplate, p.Source, p.Content))
}

// systemPrompt builds the system instruction of the next turn from the
// sections that carry content: the system prompt of the agent the branch runs,
// the instructions of the project the session runs in and the skills the
// workspace declares, in that order. The instructions and the skills are read
// from disk on every turn, so an edit to the project instruction file or to a
// skill applies to the next request even when earlier turns sent different
// content.
func (e *Engine) systemPrompt(definition agent.Agent, skills string) (string, error) {
	instructions, err := e.loadProjectInstructions()
	if err != nil {
		return "", err
	}
	return joinSections(
		strings.TrimSpace(definition.SystemPrompt),
		instructions.section(),
		strings.TrimSpace(skills),
	), nil
}

// joinSections joins the sections that carry content with the section
// separator, so a missing section never leaves a leading, a trailing or a
// doubled separator, and no section at all yields no prompt.
func joinSections(sections ...string) string {
	present := make([]string, 0, len(sections))
	for _, current := range sections {
		if current != "" {
			present = append(present, current)
		}
	}
	return strings.Join(present, sectionSeparator)
}

// loadProjectInstructions returns the instructions of the project the session
// runs in, read from the first file of projectInstructionsFiles that exists in
// the working directory. It returns zero instructions when the session runs in
// no directory or the project declares no instruction file.
func (e *Engine) loadProjectInstructions() (projectInstructions, error) {
	if e.workdir == "" {
		return projectInstructions{}, nil
	}

	for _, name := range projectInstructionsFiles {
		path := filepath.Join(e.workdir, name)
		//nolint:gosec // the path is the session working directory plus a known file name.
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return projectInstructions{}, fmt.Errorf("engine: read project instructions: %w", err)
		}
		return projectInstructions{
			Source:  name,
			Content: strings.TrimSpace(string(data)),
		}, nil
	}
	return projectInstructions{}, nil
}
