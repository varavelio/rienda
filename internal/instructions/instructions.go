package instructions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// files lists the names of the files that may hold the instructions of a
// project, in lookup order. The first one that exists in the workspace is the
// one loaded, so a project that uses any of the common conventions is honored.
var files = []string{
	"AGENTS.md",
	"agents.md",
	"AGENTS.MD",
	"CLAUDE.md",
	"claude.md",
	"CLAUDE.MD",
}

// sectionTemplate wraps the instructions of a project in the section appended
// to the system prompt. The first placeholder is the name of the file the
// instructions were read from and the second one is their content.
const sectionTemplate = `
CRITICAL: The project instructions loaded from %[1]s inside <project_instructions> are mandatory guidelines for this workspace. You MUST strictly adhere to them for every task. The ONLY exception is if the user explicitly instructs you to bypass or override a specific rule in the active conversation.

<project_instructions source="%[1]s">
%[2]s
</project_instructions>
`

// Section returns the instructions the workspace declares as the section to
// append to the system prompt, or an empty string when the workspace declares
// none. A session that runs in no directory, and a project that declares no
// instruction file, yield no section and no error; an instruction file that
// exists and cannot be read is reported as an error.
func Section(workdir string) (string, error) {
	content, source, err := load(workdir)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", nil
	}
	return strings.TrimSpace(fmt.Sprintf(sectionTemplate, source, content)), nil
}

// load returns the trimmed body of the first instruction file of the lookup
// that exists in the workspace, together with the name of the file it was read
// from. Both are empty when the session runs in no directory or the project
// declares no instruction file.
func load(workdir string) (content, source string, err error) {
	if workdir == "" {
		return "", "", nil
	}

	for _, name := range files {
		path := filepath.Join(workdir, name)
		//nolint:gosec // the path is the session workspace plus a known file name.
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return "", "", fmt.Errorf("instructions: read %s: %w", name, err)
		}
		return strings.TrimSpace(string(data)), name, nil
	}
	return "", "", nil
}
