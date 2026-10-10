package tool

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/varavelio/rienda/internal/jsruntime"
)

// entryFile is the only entry file name of an extension.
const entryFile = "index.js"

// DiscoverOptions holds what every discovered script tool needs.
type DiscoverOptions struct {
	// Dir is the directory holding the tool directories.
	Dir string

	// Workdir is the base directory handed to the runtime.
	Workdir string

	// Config is the configuration handed to ctx.config.
	Config any

	// CacheDir is the shared cache handed to ctx.cache when set.
	CacheDir string

	// StoreDir is the permanent store handed to ctx.store when set.
	StoreDir string
}

// DiscoverScripts compiles every user tool of dir and returns the tools it
// found together with one diagnostic per problem. It never fails: a missing
// directory yields nothing, and a broken tool is reported and skipped.
//
// Loading happens once per session, before any run exists, so there is no
// invocation context to pass down.
func DiscoverScripts(opts DiscoverOptions) ([]Tool, []string) {
	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{`tool ".": ` + err.Error()}
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	var (
		tools       []Tool
		diagnostics []string
	)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(opts.Dir, name, entryFile)); err != nil {
			continue
		}
		tool, problem := loadScriptTool(opts, name)
		if problem != "" {
			diagnostics = append(diagnostics, problem)
			continue
		}
		tools = append(tools, tool)
	}
	slices.Sort(diagnostics)
	return tools, diagnostics
}

// loadScriptTool compiles the tool in the named directory, or reports why it
// cannot be used.
func loadScriptTool(opts DiscoverOptions, name string) (Tool, string) {
	if !ValidName(name) {
		return nil, `tool "` + name + `": invalid tool name`
	}
	module, err := jsruntime.Compile(filepath.Join(opts.Dir, name, entryFile))
	if err != nil {
		return nil, `tool "` + name + `": ` + err.Error()
	}
	tool, err := NewScriptTool(ScriptToolOptions{
		Name:     name,
		Module:   module,
		Workdir:  opts.Workdir,
		Config:   opts.Config,
		CacheDir: opts.CacheDir,
		StoreDir: opts.StoreDir,
	})
	if err != nil {
		return nil, `tool "` + name + `": ` + err.Error()
	}
	return tool, ""
}
