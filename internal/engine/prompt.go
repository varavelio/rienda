package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// projectInstructionsSeparator divides the system prompt of the agent from the
// project instructions appended to it.
const projectInstructionsSeparator = "\n\n---\n\n"

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

// systemPrompt builds the system instruction of the next turn: the system
// prompt of the agent followed by the instructions of the project the session
// runs in. The instructions are read from disk on every turn, so an edit to
// the project instruction file applies to the next request even when earlier
// turns sent different content.
func (e *Engine) systemPrompt() (string, error) {
	systemPrompt := strings.TrimSpace(e.agent.SystemPrompt)

	instructions, err := e.loadProjectInstructions()
	if err != nil {
		return "", err
	}
	if instructions.Content == "" {
		return systemPrompt, nil
	}

	section := strings.TrimSpace(fmt.Sprintf(
		projectInstructionsTemplate,
		instructions.Source,
		instructions.Content,
	))
	if systemPrompt == "" {
		return section, nil
	}
	return systemPrompt + projectInstructionsSeparator + section, nil
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
