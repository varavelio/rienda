package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// projectInstructionsFile names the file that holds the instructions of the
// project a session runs in.
const projectInstructionsFile = "AGENTS.md"

// projectInstructionsSeparator divides the system prompt of the agent from the
// project instructions appended to it.
const projectInstructionsSeparator = "\n\n---\n\n"

// projectInstructionsTemplate injects AGENTS.md instructions by keeping the system
// directive outside and using a clean, single-level XML container for the file content.
const projectInstructionsTemplate = `
CRITICAL: The project instructions loaded from AGENTS.md inside <project_instructions> are mandatory guidelines for this workspace. You MUST strictly adhere to them for every task. The ONLY exception is if the user explicitly instructs you to bypass or override a specific rule in the active conversation.

<project_instructions source="AGENTS.md">
%s
</project_instructions>
`

// systemPrompt builds the system instruction of the next turn: the system
// prompt of the agent followed by the instructions of the project the session
// runs in. The instructions are read from disk on every turn, so an edit to
// AGENTS.md applies to the next request even when earlier turns sent different
// content.
func (e *Engine) systemPrompt() (string, error) {
	systemPrompt := strings.TrimSpace(e.agent.SystemPrompt)

	instructions, err := e.projectInstructions()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(instructions) == "" {
		return systemPrompt, nil
	}

	section := fmt.Sprintf(projectInstructionsTemplate, instructions)
	section = strings.TrimSpace(section)

	if systemPrompt == "" {
		return section, nil
	}

	return systemPrompt + projectInstructionsSeparator + section, nil
}

// projectInstructions returns the instructions of the project the session runs
// in, read from AGENTS.md in the working directory. It returns an empty string
// when the session runs in no directory or the file does not exist.
func (e *Engine) projectInstructions() (string, error) {
	if e.workdir == "" {
		return "", nil
	}

	path := filepath.Join(e.workdir, projectInstructionsFile)
	//nolint:gosec // the path is the session working directory plus a fixed file name.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("engine: read project instructions: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}
