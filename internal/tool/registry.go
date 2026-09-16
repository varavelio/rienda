package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/varavelio/rienda/internal/llm"
)

// Registry holds the tools available to a harness.
//
// Registrations are safe for concurrent use, so tools contributed at startup,
// for example by future plugins, never race with runs already in flight.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry builds a registry holding tools.
func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register adds a tool to the registry. It fails when the tool is nil, when
// its name is not a valid tool name or when another tool already uses it.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("tool: cannot register a nil tool")
	}

	definition := t.Definition()
	if !ValidName(definition.Name) {
		return fmt.Errorf(
			"tool: invalid tool name %q: names are 1 to 64 characters of letters, digits, underscores or hyphens",
			definition.Name,
		)
	}
	if len(definition.Parameters) == 0 || !json.Valid(definition.Parameters) {
		return fmt.Errorf(
			"tool: tool %q must declare a valid JSON Schema in Parameters",
			definition.Name,
		)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[definition.Name]; exists {
		return fmt.Errorf("tool: tool %q is already registered", definition.Name)
	}
	r.tools[definition.Name] = t
	return nil
}

// Lookup returns the tool registered under name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names returns the registered tool names sorted alphabetically.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Definitions returns the definitions of the named tools in the given order.
// Repeated names are collapsed and an unknown name is an error.
func (r *Registry) Definitions(names []string) ([]llm.Tool, error) {
	if len(names) == 0 {
		return nil, nil
	}

	definitions := make([]llm.Tool, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true

		t, ok := r.Lookup(name)
		if !ok {
			return nil, fmt.Errorf(
				"tool: unknown tool %q (available: %s)",
				name,
				strings.Join(r.Names(), ", "),
			)
		}
		definitions = append(definitions, t.Definition())
	}
	return definitions, nil
}

// ValidName reports whether name can name a tool. Tool names are 1 to 64
// characters of ASCII letters, digits, underscores and hyphens, the strictest
// charset accepted by the supported providers.
func ValidName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
