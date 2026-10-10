package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// ErrMalformed reports a credentials file that exists but is not a valid
// credentials document. Callers degrade it to an all-unauthenticated state.
var ErrMalformed = errors.New("credentials: malformed file")

// Entry is the credential fields of one provider. Unknown keys a user or a
// future release wrote are preserved on rewrite through Raw.
type Entry struct {
	// APIKey is the credential sent in the Authorization header.
	APIKey string `json:"api_key,omitempty"`

	// Raw holds every other field of the entry, untouched.
	Raw map[string]json.RawMessage `json:"-"`
}

// Store is the credentials of one user, loaded once and refreshed on
// demand.
type Store struct {
	path    string
	entries map[string]Entry
}

// Load reads the credentials at path. A file that does not exist is an empty
// store and no error: a fresh install is a normal state. A malformed file is
// the documented ErrMalformed error, which the caller degrades to a warning
// and an empty reading.
func Load(path string) (*Store, error) {
	store := &Store{path: path, entries: map[string]Entry{}}
	data, err := os.ReadFile(path) //nolint:gosec // the caller selects the credentials path.
	switch {
	case os.IsNotExist(err):
		return store, nil
	case err != nil:
		return nil, fmt.Errorf("credentials: read %s: %w", path, err)
	}
	entries := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("%w %s: %w", ErrMalformed, path, err)
	}
	store.entries = make(map[string]Entry, len(entries))
	for name, raw := range entries {
		entry := Entry{Raw: map[string]json.RawMessage{}}
		_ = json.Unmarshal(raw, &entry)
		entry.Raw = rawFields(raw)
		store.entries[name] = entry
	}
	return store, nil
}

// rawFields splits an entry JSON into its fields, stripping api_key.
func rawFields(raw json.RawMessage) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return fields
	}
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "api_key")
	return fields
}

// Names returns the provider names the store holds, in sorted order.
func (s *Store) Names() []string {
	names := make([]string, 0, len(s.entries))
	for name := range s.entries {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// sortStrings sorts names in place, ascending.
func sortStrings(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

// Authenticated reports whether the provider holds a non-empty api key.
func (s *Store) Authenticated(name string) bool {
	return s.entries[name].APIKey != ""
}

// APIKey returns the key stored for the provider, empty when absent.
func (s *Store) APIKey(name string) string {
	return s.entries[name].APIKey
}

// Set stores the api key of a provider and persists the document. The write
// re-reads the file first, so a concurrent writer's entries survive, and it
// replaces the file atomically, mode 0600 on Unix.
func (s *Store) Set(name, apiKey string) error {
	// A fresh read so the rewrite keeps what other processes wrote since the
	// store was loaded.
	fresh, err := Load(s.path)
	if err != nil {
		fresh = &Store{path: s.path, entries: map[string]Entry{}}
	}
	entry := fresh.entries[name]
	entry.APIKey = strings.TrimSpace(apiKey)
	fresh.entries[name] = entry
	data, err := encode(fresh.entries)
	if err != nil {
		return fmt.Errorf("credentials: encode: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("credentials: prepare %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return fmt.Errorf("credentials: staging: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // the rename below decides.
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("credentials: staging: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("credentials: staging: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("credentials: staging: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("credentials: swap: %w", err)
	}

	// The in-memory reading now matches the file.
	s.entries[name] = entry
	return nil
}

// fields flattens an entry into its JSON document, merging the unknown
// fields back in.
func (e Entry) fields() map[string]json.RawMessage {
	doc := make(map[string]json.RawMessage, len(e.Raw)+1)
	maps.Copy(doc, e.Raw)
	if e.APIKey != "" {
		data, err := json.Marshal(e.APIKey)
		if err == nil {
			doc["api_key"] = data
		}
	}
	return doc
}

// encode serializes the entries into one credentials document, sorted by
// name for a stable, reviewable file.
func encode(entries map[string]Entry) ([]byte, error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sortStrings(names)
	document := make(map[string]map[string]json.RawMessage, len(entries))
	for _, name := range names {
		document[name] = entries[name].fields()
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err //nolint:wrapcheck // marshaling a map cannot fail usefully.
	}
	return append(data, '\n'), nil
}

// Reload re-reads the file, discarding the cached reading, so a key another
// process wrote becomes visible.
func (s *Store) Reload() error {
	fresh, err := Load(s.path)
	if err != nil {
		return err
	}
	s.entries = fresh.entries
	return nil
}
