package slash

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Extension is the file extension of command definition files.
const Extension = ".md"

// Command is a single custom command definition loaded from disk.
type Command struct {
	// ID is the file name of the definition without its extension.
	ID string

	// Path is the absolute path of the definition file, empty when the command
	// was parsed from memory.
	Path string

	// Description explains what the command does. It is required.
	Description string

	// Template is the Markdown body of the definition, the prompt template
	// expanded on submit.
	Template string
}

// DefaultDir returns the directory holding the global command definitions.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("slash: locate home directory: %w", err)
	}
	return filepath.Join(home, ".rienda", "commands"), nil
}

// ValidID reports whether id can name a command definition. Valid identifiers
// are non-empty plain file names without path separators, without a leading
// dot and without the .md extension.
func ValidID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	return !strings.HasSuffix(id, Extension)
}

// Load reads and parses the definition of the command identified by id from dir.
func Load(dir, id string) (Command, error) {
	if !ValidID(id) {
		return Command{}, fmt.Errorf("slash: invalid command id %q", id)
	}

	path := filepath.Join(dir, id+Extension)
	data, err := os.ReadFile(path) //nolint:gosec // the path is built from a validated command id.
	if err != nil {
		return Command{}, fmt.Errorf("slash: read %s: %w", path, err)
	}

	loaded, err := Parse(id, data)
	if err != nil {
		return Command{}, fmt.Errorf("%s: %w", path, err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return Command{}, fmt.Errorf("slash: resolve %s: %w", path, err)
	}
	loaded.Path = absolute
	return loaded, nil
}

// LoadAll reads every command definition in dir and returns them sorted by ID.
// Hidden files, non-Markdown files and non-regular files are ignored. A
// missing dir yields no commands and no error. Definitions that fail to parse
// are skipped and their errors collected, so the caller can report every
// problem at once while still receiving the commands that loaded correctly.
func LoadAll(dir string) ([]Command, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("slash: read directory %s: %w", dir, err)
	}

	commands := make([]Command, 0, len(entries))
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, Extension) ||
			strings.HasPrefix(name, ".") {
			continue
		}

		loaded, err := Load(dir, strings.TrimSuffix(name, Extension))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		commands = append(commands, loaded)
	}

	slices.SortFunc(commands, func(a, b Command) int { return cmp.Compare(a.ID, b.ID) })
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
		return commands, errors.Join(errs...)
	}
	return commands, nil
}
