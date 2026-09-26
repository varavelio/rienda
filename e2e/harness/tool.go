//go:build e2e

package harness

import (
	"path/filepath"
	"testing"
)

// Extension directory names inside the rienda directory of an instance.
const (
	toolsDirName  = "tools"
	hooksDirName  = "hooks"
	indexFileName = "index.js"
)

// Tool describes one user tool written into the tools directory of an
// instance, in the exact format the binary reads.
type Tool struct {
	// Dir is the name of the directory that holds the index.js file. It is
	// the tool name the model calls.
	Dir string

	// Script is the content of the index.js file.
	Script string
}

// write stores the tool into the tools directory of an instance.
func (t Tool) write(tb *testing.T, home string) {
	tb.Helper()
	if t.Dir == "" {
		tb.Fatal("harness: every tool declaration needs a dir")
	}
	writeFile(tb, filepath.Join(home, riendaDirName, toolsDirName, t.Dir, indexFileName), t.Script)
}

// Hook describes one user hook written into the hooks directory of an
// instance, in the exact format the binary reads.
type Hook struct {
	// Dir is the name of the directory that holds the index.js file. It is
	// the hook name agents declare.
	Dir string

	// Script is the content of the index.js file.
	Script string
}

// write stores the hook into the hooks directory of an instance.
func (h Hook) write(tb *testing.T, home string) {
	tb.Helper()
	if h.Dir == "" {
		tb.Fatal("harness: every hook declaration needs a dir")
	}
	writeFile(tb, filepath.Join(home, riendaDirName, hooksDirName, h.Dir, indexFileName), h.Script)
}
