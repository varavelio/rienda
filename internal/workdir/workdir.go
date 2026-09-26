package workdir

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// contextKey carries the working directory of an invocation.
type contextKey struct{}

// WithWorkdir attaches dir as the working directory of the tools invoked with
// ctx.
func WithWorkdir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, contextKey{}, dir)
}

// FromContext returns the working directory attached to ctx, if any.
func FromContext(ctx context.Context) (string, bool) {
	dir, ok := ctx.Value(contextKey{}).(string)
	if !ok || dir == "" {
		return "", false
	}
	return dir, true
}

// Base returns the base directory of a run: the context directory, the
// configured directory, then the process working directory.
func Base(ctx context.Context, configured string) (string, error) {
	base := configured
	if dir, ok := FromContext(ctx); ok {
		base = dir
	}
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("workdir: locate process working directory: %w", err)
		}
		base = cwd
	}
	if err := Validate(base); err != nil {
		return "", err
	}
	return base, nil
}

// Resolve returns the absolute location of path against base. An absolute path
// is cleaned and used as is, a relative path is joined to base, and an empty
// path returns base.
func Resolve(base, path string) string {
	if path == "" {
		return base
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

// Validate reports whether dir is an existing directory.
func Validate(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("workdir: %w", err)
	}
	if !info.IsDir() {
		return &NotDirError{Dir: dir}
	}
	return nil
}

// NotDirError reports a path that exists but is not a directory.
type NotDirError struct {
	// Dir is the offending path.
	Dir string
}

// Error describes the offending path.
func (e *NotDirError) Error() string {
	return "working directory " + e.Dir + " is not a directory"
}
