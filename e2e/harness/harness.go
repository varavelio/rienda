//go:build e2e

package harness

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Options configures one harness instance. Every instance gets a fresh home
// directory and a fresh workspace directory of its own.
type Options struct {
	// Script lists the model responses the fake provider serves, in order.
	Script []Turn

	// Agents lists the agent definitions written into the global agents
	// directory of the instance.
	Agents []Agent

	// Config overrides the configuration file of the instance. It defaults to
	// DefaultConfig. Providers that declare no base URL point at the fake
	// provider of the harness, whichever connection they declare.
	Config *Config

	// SkipConfigFile leaves the default configuration path empty, so runs
	// exercise the behavior of a first run.
	SkipConfigFile bool

	// Env appends variables to the environment of every invocation, overriding
	// the inherited ones.
	Env []string
}

// Harness is one isolated rienda installation driven by a single test: a home
// directory with its own configuration and agent definitions, a workspace
// directory, and a fake provider that answers the requests of the compiled
// binary.
type Harness struct {
	home     string
	workdir  string
	env      []string
	provider *FakeProvider
}

// New starts a fresh instance for one test: it creates the home directory of
// the instance, starts the fake provider, writes the configuration and the
// agent definitions of the options, and returns the harness that drives it.
func New(t *testing.T, opts Options) *Harness {
	t.Helper()

	provider := newFakeProvider(t, opts.Script)
	instance := &Harness{
		home:     t.TempDir(),
		workdir:  t.TempDir(),
		env:      slices.Clone(opts.Env),
		provider: provider,
	}

	if !opts.SkipConfigFile {
		cfg := DefaultConfig()
		if opts.Config != nil {
			cfg = *opts.Config
		}
		cfg.write(t, instance.ConfigPath(), provider.BaseURL())
	}
	for _, definition := range opts.Agents {
		definition.write(t, instance.AgentsDir())
	}

	return instance
}

// Home returns the home directory of the instance, the value of HOME for every
// invocation and the directory that holds the stored sessions.
func (h *Harness) Home() string {
	return h.home
}

// Workdir returns the workspace directory of the instance, the working
// directory of every invocation that does not override it.
func (h *Harness) Workdir() string {
	return h.workdir
}

// Provider returns the fake provider of the instance.
func (h *Harness) Provider() *FakeProvider {
	return h.provider
}

// Run executes the compiled binary with the environment of the instance and
// waits for it to exit.
func (h *Harness) Run(t *testing.T, args ...string) *Result {
	t.Helper()
	return execute(t, h.workdir, os.Environ(), h.environment(nil), args...)
}

// RunIn executes the compiled binary in another workspace directory, which
// exercises the resolution of a workspace the invocation does not declare.
func (h *Harness) RunIn(t *testing.T, dir string, args ...string) *Result {
	t.Helper()
	return execute(t, dir, os.Environ(), h.environment(nil), args...)
}

// RunEnv executes the compiled binary like Run, with additional environment
// variables that override the ones of the instance.
func (h *Harness) RunEnv(t *testing.T, env []string, args ...string) *Result {
	t.Helper()
	return execute(t, h.workdir, os.Environ(), h.environment(env), args...)
}

// environment returns the environment of every invocation of the instance: the
// variables of the options, the home directory of the instance and the
// additions, in override order.
func (h *Harness) environment(additions []string) []string {
	env := make([]string, 0, len(h.env)+1+len(additions))
	env = append(env, h.env...)
	env = append(env, "HOME="+h.home)
	return append(env, additions...)
}

// AgentPath returns the path of the definition file of an agent, whether or
// not the instance declares it, so tests can assert the failures that name it.
func (h *Harness) AgentPath(id string) string {
	return filepath.Join(h.AgentsDir(), Agent{ID: id}.FileName())
}
