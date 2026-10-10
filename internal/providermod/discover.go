package providermod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/varavelio/rienda/internal/jsruntime"
)

// runDiscovery implements Discover: it selects the winning module of every
// name, executes the winners in order and validates their declarations.
func runDiscovery(
	ctx context.Context,
	src Sources,
	opts Options,
	logf func(string, ...any),
) ([]Provider, error) {
	// No logger is a silent logger: callers that only walk the roster still
	// get valid providers.
	if logf == nil {
		logf = func(format string, args ...any) {}
	}

	// The winner of every name is the user module when one exists, else the
	// embedded module. A user module that cannot be read is logged and
	// skipped in the same breath as one that fails at run time.
	winners := make(map[string]moduleFile, len(src.Embedded))
	for name, source := range src.Embedded {
		winners[name] = moduleFile{name: name, source: source}
	}
	users, err := userModules(src.UserDir)
	if err != nil {
		return nil, err
	}
	for name, path := range users {
		source, err := os.ReadFile(path) //nolint:gosec // the caller selects the directory.
		if err != nil {
			logf("providermod: provider %q: %v", name, err)
			continue
		}
		winners[name] = moduleFile{name: name, path: path, source: string(source)}
	}

	providers := make([]Provider, 0, len(winners))
	for _, name := range modulesNames(winners) {
		file := winners[name]
		decl, ok := runModule(ctx, name, file, src.UserDir, opts, logf)
		if !ok {
			continue
		}
		providers = append(providers, Provider{Name: name, Decl: decl})
	}
	slices.SortFunc(providers, func(a, b Provider) int { return strings.Compare(a.Name, b.Name) })
	return providers, nil
}

// moduleFile is one winning module: its name, its source and, when it lives
// on disk, the path it was read from (user modules keep it, embedded ones do
// not).
type moduleFile struct {
	name   string
	path   string
	source string
}

// modulesNames returns the winner names in sorted order, the exact order the
// modules run in.
func modulesNames(winners map[string]moduleFile) []string {
	names := make([]string, 0, len(winners))
	for name := range winners {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// runModule compiles and executes one module in the directory its provider
// owns, and returns the declaration it declared. The second result is false
// when the module failed and its provider has to stay absent; the reason was
// already logged.
func runModule(
	ctx context.Context,
	name string,
	file moduleFile,
	userDir string,
	opts Options,
	logf func(string, ...any),
) (Declaration, bool) {
	// Every module runs with its own provider directory as the file scope,
	// so its cache lives beside its declaration and touches nothing else.
	dir := ""
	if userDir != "" {
		dir = filepath.Join(userDir, name)
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			logf("providermod: provider %q: %v", name, err)
			return Declaration{}, false
		}
	}

	var module *jsruntime.Module
	var err error
	if file.path != "" {
		module, err = jsruntime.Compile(file.path)
	} else {
		module, err = jsruntime.CompileSource(name, file.source)
	}
	if err != nil {
		logf("providermod: provider %q: %v", name, err)
		return Declaration{}, false
	}

	var declared *Declaration
	invokeErr := module.Invoke(ctx, jsruntime.Options{
		Workdir:        dir,
		FileRoot:       dir,
		MaxOutputBytes: opts.MaxOutputBytes,
		CacheDir:       opts.CacheDir,
		StoreDir:       opts.StoreDir,
		Config:         opts.Config,
	}, nil, func(rt *jsruntime.Runtime, exports *jsruntime.Exports) error {
		main := exports.Self()
		if main.Undefined() || !main.IsCallable() {
			return errors.New("module.exports must be a function(ctx)")
		}
		raw, callErr := main.Call(ctx, rt.ContextValue())
		if callErr != nil {
			//nolint:wrapcheck // the module name is attached by the caller.
			return callErr
		}
		plain, ok := raw.JSON()
		if !ok {
			return fmt.Errorf("module returned %v instead of a declaration object", raw)
		}
		decl, decodeErr := decodeDeclaration(plain)
		if decodeErr != nil {
			return decodeErr
		}
		declared = decl
		return nil
	})
	if invokeErr != nil {
		logf("providermod: provider %q: %v", name, invokeErr)
		return Declaration{}, false
	}
	if violations := validate(*declared); len(violations) > 0 {
		logf("providermod: provider %q: %v", name, violations[0])
		return Declaration{}, false
	}
	return *declared, true
}

// decodeDeclaration audits the plain data a module returned: an object with
// one array of objects, everything else malformed. The JSON round trip also
// normalizes numbers to the shapes the Go declarations declare.
func decodeDeclaration(plain any) (*Declaration, error) {
	raw, err := json.Marshal(plain)
	if err != nil {
		return nil, fmt.Errorf("module returned data that is not serializable: %w", err)
	}
	var decl Declaration
	if err := json.Unmarshal(raw, &decl); err != nil {
		return nil, fmt.Errorf("module returned a malformed declaration: %w", err)
	}
	if decl.Protocol == "" && decl.BaseURL == "" && decl.Auth == "" && len(decl.Models) == 0 {
		return nil, errors.New("module returned an empty declaration")
	}
	return &decl, nil
}

// userModules reports the user providers: every subdirectory of dir holding
// an index.js, as name -> path of that index.js. A missing directory yields
// no user modules and is not an error.
func userModules(dir string) (map[string]string, error) {
	if dir == "" {
		return map[string]string{}, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("providermod: list %s: %w", dir, err)
	}
	modules := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "index.js")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		modules[entry.Name()] = path
	}
	return modules, nil
}
