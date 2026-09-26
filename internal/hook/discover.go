package hook

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/varavelio/rienda/internal/jsruntime"
	"github.com/varavelio/rienda/internal/tool"
)

// entryFile is the only entry file name of an extension.
const entryFile = "index.js"

// pointNames lists every known hook point.
var pointNames = []string{
	"beforeRun",
	"afterRun",
	"beforeModelRequest",
	"afterModelResponse",
	"beforeToolExecute",
	"afterToolExecute",
}

// knownPoint reports whether name is a hook point.
func knownPoint(name string) bool {
	return slices.Contains(pointNames, name)
}

// Registry holds the discovered hook extensions.
type Registry struct {
	modules map[string]*jsruntime.Module
	points  map[string]map[string]bool
	opts    jsruntime.Options
}

// Discover compiles every hook extension of dir and reports the problems it
// found. It never fails: a missing directory yields nothing, and a broken
// extension is reported and skipped.
//
// Loading happens once per session, before any run exists, so there is no
// invocation context to pass down.
func Discover(dir string, base jsruntime.Options) (Registry, []string) {
	registry := Registry{
		modules: make(map[string]*jsruntime.Module),
		points:  make(map[string]map[string]bool),
		opts:    base,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return registry, nil
		}
		return registry, []string{`hook ".": ` + err.Error()}
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	var diagnostics []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name, entryFile)); err != nil {
			continue
		}
		points, problems := registry.load(dir, name)
		for _, problem := range problems {
			diagnostics = append(diagnostics, `hook "`+name+`": `+problem)
		}
		if points != nil {
			registry.modules[name] = points.module
			registry.points[name] = points.implemented
		}
	}
	slices.Sort(diagnostics)
	return registry, diagnostics
}

// loadedModule is a compiled extension with its implemented points.
type loadedModule struct {
	module      *jsruntime.Module
	implemented map[string]bool
}

// load compiles the extension in the named directory, returning the module
// when it implements at least one known hook point and every problem found.
func (r Registry) load(dir, name string) (*loadedModule, []string) {
	if !tool.ValidName(name) {
		return nil, []string{"invalid hook name"}
	}
	module, err := jsruntime.Compile(filepath.Join(dir, name, entryFile))
	if err != nil {
		return nil, []string{err.Error()}
	}
	implemented, problems := inspect(module, r.opts)
	if len(implemented) == 0 && len(problems) == 0 {
		problems = append(problems, "implements no known hook point")
	}
	if len(implemented) == 0 {
		return nil, problems
	}
	return &loadedModule{module: module, implemented: implemented}, problems
}

// inspect reads the exports of a module, returning the known hook points it
// implements as functions and one problem per export that cannot be used.
func inspect(module *jsruntime.Module, opts jsruntime.Options) (map[string]bool, []string) {
	implemented := make(map[string]bool)
	var problems []string
	_ = module.Invoke(
		context.Background(),
		opts,
		nil,
		func(_ *jsruntime.Runtime, exports *jsruntime.Exports) error {
			for _, name := range exports.Keys() {
				if !knownPoint(name) {
					problems = append(problems, "unknown export "+name)
					continue
				}
				value, ok := exports.Field(name)
				if !ok || !value.IsCallable() {
					problems = append(problems, name+" is not a function")
					continue
				}
				implemented[name] = true
			}
			return nil
		},
	)
	return implemented, problems
}

// Names returns the discovered hook names in sorted order.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r.modules))
	for name := range r.modules {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
