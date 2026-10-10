package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/varavelio/rienda/internal/config"
	"github.com/varavelio/rienda/internal/credentials"
	"github.com/varavelio/rienda/internal/provider/native"
	"github.com/varavelio/rienda/internal/providermod"
	"github.com/varavelio/rienda/internal/session"
	"github.com/varavelio/rienda/internal/state"
)

// riendaDirName is the directory inside the home directory that holds the
// provider modules, the credentials and the state Rienda keeps for a user.
const riendaDirName = ".rienda"

// cacheDirName is the directory inside the Rienda directory that holds the
// shared cache of the provider modules.
const cacheDirName = "cache"

// cacheDir returns the directory of the shared cache: the Rienda cache of the
// installation, which every run of the home directory shares. A missing home
// yields an empty path, which leaves the primitive out of the modules.
func cacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, riendaDirName, cacheDirName)
}

// providersEnvVar overrides the provider directory the same way RIENDA_CONFIG
// overrides the configuration path, which is what the test suite uses to
// point Rienda at its own provider fixtures.
const providersEnvVar = "RIENDA_PROVIDERS"

// credentialsEnvVar overrides the credentials file the same way.
const credentialsEnvVar = "RIENDA_CREDENTIALS" //nolint:gosec // an environment variable name, not a secret.

// stateEnvVar overrides the state file the same way.
const stateEnvVar = "RIENDA_STATE"

// Discover runs the provider discovery of an installation without a session:
// the auth screen of the product lists what it returns. An installation whose
// discovery fails yields no providers rather than refusing the screen, since
// reading the providers is exactly what the screen reports.
func Discover() ([]providermod.Provider, error) {
	providers, _, err := discoverProviders(context.Background(), Options{})
	return providers, err
}

// discoverProviders resolves the provider set of a session: built-in
// modules, shadowed by the user modules of the provider directory. The
// configuration the user wrote travels to the modules as their ctx.config,
// which is where the extensions read it.
func discoverProviders(
	ctx context.Context,
	opts Options,
) ([]providermod.Provider, *config.Config, error) {
	userDir, err := providersDir(opts.ProvidersDir)
	if err != nil {
		return nil, nil, err
	}
	path, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("harness: resolve configuration: %w", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, nil, err
	}
	providers, err := providermod.Discover(
		context.WithoutCancel(ctx),
		providermod.Sources{Embedded: native.Builtin(), UserDir: userDir},
		providermod.Options{
			Config:         cfg.Data(),
			MaxOutputBytes: native.CatalogBudget,
			CacheDir:       cacheDir(),
		},
		logProvider,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("harness: discover providers: %w", err)
	}
	return providers, cfg, nil
}

// logProvider reports one line per provider module that failed: the reason
// is a diagnostic, never a failure of the session.
func logProvider(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rienda: "+format+"\n", args...)
}

// loadConfig reads the configuration file of a run. The file no longer
// declares providers or models, so a file that still does refuses to load:
// the only way to define a provider is a module.
func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("harness: load configuration: %w", err)
	}
	return cfg, nil
}

// providersDir returns the directory holding the user provider modules: the
// requested one, the environment one, or the global providers directory,
// which may not exist yet and then yields no user providers.
func providersDir(requested string) (string, error) {
	if dir := strings.TrimSpace(requested); dir != "" {
		return dir, nil
	}
	if dir := strings.TrimSpace(os.Getenv(providersEnvVar)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("harness: locate home directory: %w", err)
	}
	return filepath.Join(home, riendaDirName, "providers"), nil
}

// credentialStore loads the credentials of a user from the credentials file
// of the run: the requested one, the environment one, or the global one.
func credentialStore(opts Options) (*credentials.Store, error) {
	path := strings.TrimSpace(opts.CredentialsPath)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(credentialsEnvVar))
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("harness: locate home directory: %w", err)
		}
		path = filepath.Join(home, riendaDirName, "credentials.json")
	}
	store, err := credentials.Load(path)
	if errors.Is(err, credentials.ErrMalformed) {
		logProvider("credentials %s is malformed; every provider counts as unauthenticated", path)
		return &credentials.Store{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("harness: load credentials: %w", err)
	}
	return store, nil
}

// userState loads the global state of a user from the state file of the run:
// the requested one, the environment one, or the global one. A malformed
// state degrades to the zero document with one logged warning.
func userState(opts Options) state.Doc {
	path := strings.TrimSpace(opts.StatePath)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(stateEnvVar))
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, riendaDirName, "state.json")
		}
	}
	doc, err := state.Load(path)
	if err != nil {
		logProvider("state %s: %v; starting without remembered state", path, err)
		return state.Doc{}
	}
	return doc
}

// modelRoster returns every model reference the discovered providers hold,
// sorted, which is the roster a session runs and a front end offers.
func modelRoster(providers []providermod.Provider) []string {
	refs := make([]string, 0, len(providers))
	for _, item := range providers {
		refs = append(refs, item.Refs()...)
	}
	slices.Sort(refs)
	return refs
}

// initialModel decides the model a session starts on: the explicit override,
// the model of a resumed branch, the model the user last used, the head of
// the roster and, when the roster is empty, no model at all. Stale entries
// fall through to the next rule with a logged warning.
func initialModel(
	opts Options,
	store *credentials.Store,
	roster []string,
	dir string,
) (string, error) {
	if ref := strings.TrimSpace(opts.ModelRef); ref != "" {
		if !slices.Contains(roster, ref) {
			return "", undefinedModel(ref)
		}
		return ref, nil
	}

	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID != "" {
		if ref, ok := resumedModel(dir, sessionID, roster); ok {
			return ref, nil
		}
	}

	doc := userState(Options{})
	if doc.LastModel != "" {
		if slices.Contains(roster, doc.LastModel) {
			return doc.LastModel, nil
		}
		logProvider("model %q is no longer available; using %q", doc.LastModel, rosterHead(roster))
	}

	return rosterHead(roster), nil
}

// resumedModel returns the model a stored session runs on its active branch
// when that reference is still on the roster.
func resumedModel(dir, sessionID string, roster []string) (string, bool) {
	store, err := session.Open(dir, sessionID, nil)
	if err != nil {
		return "", false
	}
	defer closeStore(store)
	ref := store.ActiveModel()
	if ref == "" || !slices.Contains(roster, ref) {
		if ref != "" {
			logProvider("model %q is no longer available; using %q", ref, rosterHead(roster))
		}
		return "", false
	}
	return ref, true
}

// rosterHead returns the head of a roster, the model nothing better names.
func rosterHead(roster []string) string {
	if len(roster) == 0 {
		return ""
	}
	return roster[0]
}
