package agent

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

// Extension is the file extension of agent definition files.
const Extension = ".md"

// Agent is a single agent definition loaded from disk.
type Agent struct {
	// ID is the file name of the definition without its extension.
	ID string

	// Path is the absolute path of the definition file, empty when the agent
	// was parsed from memory.
	Path string

	// Description explains what the agent does and when to use it. It is
	// required.
	Description string

	// Model is the model reference the agent runs, in provider/model form.
	// It is required.
	Model string

	// Tools lists the names of the tools available to the agent. An empty
	// list means the agent cannot use any tool.
	Tools []string

	// SystemPrompt is the Markdown body of the definition.
	SystemPrompt string
}

// DefaultDir returns the directory holding the global agent definitions.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent: locate home directory: %w", err)
	}
	return filepath.Join(home, ".rienda", "agents"), nil
}

// ValidID reports whether id can name an agent definition. Valid identifiers
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

// Load reads and parses the definition of the agent identified by id from dir.
func Load(dir, id string) (Agent, error) {
	if !ValidID(id) {
		return Agent{}, fmt.Errorf("agent: invalid agent id %q", id)
	}

	path := filepath.Join(dir, id+Extension)
	data, err := os.ReadFile(path) //nolint:gosec // the path is built from a validated agent id.
	if err != nil {
		return Agent{}, fmt.Errorf("agent: read %s: %w", path, err)
	}

	loaded, err := Parse(id, data)
	if err != nil {
		return Agent{}, fmt.Errorf("%s: %w", path, err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return Agent{}, fmt.Errorf("agent: resolve %s: %w", path, err)
	}
	loaded.Path = absolute
	return loaded, nil
}

// LoadAll reads every agent definition in dir and returns them sorted by ID.
// Hidden files, non-Markdown files and non-regular files are ignored. A
// missing dir yields no agents and no error. Definitions that fail to parse
// are skipped and their errors collected, so the caller can report every
// problem at once while still receiving the agents that loaded correctly.
func LoadAll(dir string) ([]Agent, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("agent: read directory %s: %w", dir, err)
	}

	agents := make([]Agent, 0, len(entries))
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
		agents = append(agents, loaded)
	}

	slices.SortFunc(agents, func(a, b Agent) int { return cmp.Compare(a.ID, b.ID) })
	if len(errs) > 0 {
		return agents, errors.Join(errs...)
	}
	return agents, nil
}
