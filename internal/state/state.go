package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Doc is the document the state file holds.
type Doc struct {
	// LastModel is the model reference the user last switched to, the model
	// new sessions start from.
	LastModel string `json:"last_model,omitempty"`

	// LastThinking maps a model reference to the thinking level the user
	// last picked for it, the level a new session of that model starts on.
	LastThinking map[string]string `json:"last_thinking,omitempty"`

	// PinnedModels lists the model references the user pinned, in the order
	// they pinned them, which is the order the interface cycles them in.
	PinnedModels []string `json:"pinned_models,omitempty"`
}

// Load reads the document at path. A file that does not exist is the zero
// Doc and no error: a fresh install is a normal state. A file that exists
// but is not a document is an error, which a caller may degrade to a
// warning.
func Load(path string) (Doc, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller selects the state path.
	if os.IsNotExist(err) {
		return Doc{}, nil
	}
	if err != nil {
		return Doc{}, fmt.Errorf("state: read %s: %w", path, err)
	}
	var doc Doc
	if err := json.Unmarshal(data, &doc); err != nil {
		return Doc{}, fmt.Errorf("state: parse %s: %w", path, err)
	}
	return doc, nil
}

// Write stores the document at path atomically: the content is written to a
// temporary file in the same directory and renamed over the file, so a
// reader never sees a partial document and the previous one survives a
// crash.
func Write(path string, doc Doc) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("state: encode: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: prepare %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return fmt.Errorf("state: staging: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // the rename below decides.
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("state: staging: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("state: staging: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("state: staging: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("state: swap: %w", err)
	}
	return nil
}

// TogglePin pins ref in the document at path when it is not pinned and
// unpins it when it is, and reports whether ref ended up pinned. The
// document is read fresh before the write, so a writer that joined while the
// session was open keeps its entries, and a file that does not exist yet is
// created. The write is atomic, so an interrupted toggle never leaves a
// partial document behind.
func TogglePin(path, ref string) (bool, error) {
	doc, err := Load(path)
	if err != nil {
		return false, err
	}

	pinned := !slices.Contains(doc.PinnedModels, ref)
	if pinned {
		doc.PinnedModels = append(doc.PinnedModels, ref)
	} else {
		// Removing every occurrence normalizes a hand-edited duplicate, which
		// the read side tolerates but never writes.
		doc.PinnedModels = slices.DeleteFunc(doc.PinnedModels, func(entry string) bool {
			return entry == ref
		})
	}

	if err := Write(path, doc); err != nil {
		return false, err
	}
	return pinned, nil
}
