package files

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/boyter/gocodewalker"
)

// walkQueueSize is how many entries the walk buffers before it blocks, which
// is enough to keep the walking goroutine ahead of the reader.
const walkQueueSize = 1024

// vcsDirectories are the version control directories of a project. Their
// content is never listed, whatever the ignore files say.
var vcsDirectories = []string{".git", ".hg", ".svn"}

// Entry is one path of a project.
type Entry struct {
	// Path is the location of the entry relative to the root of the project,
	// with slash separators whatever the platform.
	Path string

	// IsDir reports whether the entry is a directory.
	IsDir bool
}

// Lister lists the entries of a project rooted at a directory.
type Lister struct {
	root string
}

// New returns a Lister for the project rooted at the given directory. The root
// is resolved to an absolute path, so the entries it lists do not depend on
// the working directory of the process.
func New(root string) *Lister {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Lister{root: root}
}

// Entries returns the files of the project and the directories that contain
// them, sorted by path and relative to the root of the project. It reads the
// filesystem on every call, so a caller that lists the same project
// repeatedly caches the result itself.
func (l *Lister) Entries() ([]Entry, error) {
	if err := l.checkRoot(); err != nil {
		return nil, err
	}

	locations, err := l.walk()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(locations))
	directories := make(map[string]bool)
	for _, location := range locations {
		path, ok := l.relative(location)
		if !ok {
			continue
		}
		entries = append(entries, Entry{Path: path})
		for dir := parentDir(path); dir != ""; dir = parentDir(dir) {
			directories[dir] = true
		}
	}
	for dir := range directories {
		entries = append(entries, Entry{Path: dir, IsDir: true})
	}

	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return entries, nil
}

// checkRoot reports whether the project root is a directory that can be read.
func (l *Lister) checkRoot() error {
	info, err := os.Stat(l.root)
	if err != nil {
		return fmt.Errorf("files: list %s: %w", l.root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("files: list %s: not a directory", l.root)
	}
	return nil
}

// walk returns the absolute location of every file of the project that the
// ignore rules keep. Hidden files are listed, the way git lists them, because
// a project refers to files such as .gitignore or .github/workflows; the
// version control directories are neither listed nor descended into.
func (l *Lister) walk() ([]string, error) {
	queue := make(chan *gocodewalker.File, walkQueueSize)
	walker := gocodewalker.NewFileWalker(l.root, queue)
	walker.IncludeHidden = true
	walker.ExcludeDirectory = vcsDirectories
	// A file or directory that cannot be read never stops the walk; the walk
	// reports what it could read.
	walker.SetErrorHandler(func(error) bool { return true })

	// The walk closes the queue once it is done, so reading the queue to its
	// end already waits for it; the error is collected through its own channel
	// so that reading it is ordered after the walk that produced it.
	done := make(chan error, 1)
	go func() { done <- walker.Start() }()

	locations := make([]string, 0, walkQueueSize)
	for file := range queue {
		locations = append(locations, file.Location)
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("files: walk %s: %w", l.root, err)
	}
	return locations, nil
}

// relative returns the path of a walked file relative to the root of the
// project, or reports that the file must not be listed because it belongs to a
// version control directory. A .git entry is a file in worktrees and
// submodules, which is why the directory check is made on the path instead of
// relying on the walk to skip directories only.
func (l *Lister) relative(location string) (string, bool) {
	path, err := filepath.Rel(l.root, location)
	if err != nil {
		return "", false
	}
	path = filepath.ToSlash(path)
	if path == "." || strings.HasPrefix(path, "../") || isVCS(path) {
		return "", false
	}
	return path, true
}

// isVCS reports whether a project-relative path belongs to a version control
// directory.
func isVCS(path string) bool {
	dir, _, _ := strings.Cut(path, "/")
	return slices.Contains(vcsDirectories, dir)
}

// parentDir returns the directory that holds the given relative path, or the
// empty string when the path has no parent.
func parentDir(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}
