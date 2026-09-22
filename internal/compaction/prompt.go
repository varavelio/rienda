package compaction

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// builtinPrompt holds the summarization prompt compiled into the binary. The
// file is the prompt: it is reviewed and diffed as prose instead of as a Go
// string.
//
//go:embed COMPACTION.md
var builtinPrompt string

// Prompt returns the summarization prompt. It reads the override file at path
// when it exists and holds more than whitespace, and the embedded prompt
// otherwise. The override is all or nothing: no concatenation, no templating,
// so a user who wants their own summary format gets exactly that.
//
// The file is read on every call, so an edit applies to the next compaction
// without restarting the process. An empty path skips the override entirely,
// which is what a caller without a home directory does.
func Prompt(path string) (string, error) {
	override, err := readOverride(path)
	switch {
	case err != nil:
		return "", err
	case strings.TrimSpace(override) != "":
		return override, nil
	default:
		return builtinPrompt, nil
	}
}

// readOverride returns the contents of the override file, or an empty string
// when it is absent.
func readOverride(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // the user selects the path.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("compaction: read prompt override %s: %w", path, err)
	}
	return string(data), nil
}
