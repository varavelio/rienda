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

// projectInstructionsPreamble introduces the project instructions and states
// that the model must follow them.
const projectInstructionsPreamble = "The content below holds the instructions for working on " +
	"the current project. You MUST follow them."

// systemPrompt builds the system instruction of the next turn: the system
// prompt of the agent followed by the instructions of the project the session
// runs in. The instructions are read from disk on every turn, so an edit to
// AGENTS.md applies to the next request even when earlier turns sent different
// content.
func (e *Engine) systemPrompt() (string, error) {
	instructions, err := e.projectInstructions()
	if err != nil {
		return "", err
	}
	if instructions == "" {
		return e.agent.SystemPrompt, nil
	}

	section := projectInstructionsPreamble + "\n\n" + instructions
	if strings.TrimSpace(e.agent.SystemPrompt) == "" {
		return section, nil
	}
	return e.agent.SystemPrompt + projectInstructionsSeparator + section, nil
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
