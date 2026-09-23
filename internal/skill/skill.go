package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Locations of a skill relative to the workspace. They carry forward slashes
// whatever the platform, unlike skillsDir, because the model reads them and the
// operating system never does: the same catalog has to work on every host and
// inside a dev container.
const (
	// skillsLocation is the directory that holds the skills of a project.
	skillsLocation = "./.agents/skills"

	// locationPrefix is the beginning of the location of a skill.
	locationPrefix = skillsLocation + "/"
)

// skillsDir is the directory, relative to the workspace, that holds the skills
// of a project. It uses the separator of the platform because it is only ever
// handed to the operating system.
var skillsDir = filepath.Join(".agents", "skills")

// skillFile is the name of the file that declares a skill.
const skillFile = "SKILL.md"

// skill is one usable skill of a workspace.
type skill struct {
	// dir is the name of the directory that holds the SKILL.md file, which is
	// what orders the catalog and what resolves a duplicate.
	dir string

	// name is the name the frontmatter declares, the identity of the skill.
	name string

	// description explains what the skill does and when to use it.
	description string
}

// Result is what the discovery of the skills of a workspace produced.
type Result struct {
	// Section is the skills section to append to the system prompt, empty when
	// the workspace declares no usable skill.
	Section string

	// Diagnostics lists the non-fatal problems found while discovering, one
	// entry per skill, identified by the relative path of its SKILL.md.
	Diagnostics []string
}

// Discover reads the skills of a workspace and returns the section to publish
// and the problems it found. It never fails: a skill it cannot use is reported
// as a diagnostic and skipped, and every other skill is still offered. A
// workspace that declares no skill, and a session that runs in no directory,
// yield no section and no diagnostic.
func Discover(workdir string) Result {
	if workdir == "" {
		return Result{}
	}

	base := filepath.Join(workdir, skillsDir)
	entries, err := os.ReadDir(base)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A project without skills is the common case and is not a problem.
		return Result{}
	case err != nil:
		return Result{Diagnostics: []string{skillsLocation + ": " + err.Error()}}
	}

	// The order is Rienda's and not the file system's, so the catalog reads the
	// same on every platform and on every run.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	var (
		found       []skill
		diagnostics []string
	)
	for _, entry := range entries {
		offered, problem := inspect(base, entry)
		if problem != "" {
			diagnostics = append(diagnostics, problem)
		}
		if offered.dir != "" {
			found = append(found, offered)
		}
	}

	// A name declared twice keeps the skill whose directory comes last, which
	// is unambiguous because a file system cannot hold two directories with the
	// same name. The shadowed skill is still reported, so it never disappears
	// in silence.
	winners := make(map[string]string, len(found))
	for _, candidate := range found {
		winners[candidate.name] = candidate.dir
	}

	offered := make([]skill, 0, len(winners))
	for _, candidate := range found {
		winner := winners[candidate.name]
		if winner != candidate.dir {
			diagnostics = append(
				diagnostics,
				diagnose(locationOf(candidate.dir), "shadowed by "+locationOf(winner)),
			)
			continue
		}
		offered = append(offered, candidate)
	}

	slices.Sort(diagnostics)
	return Result{Section: section(offered), Diagnostics: diagnostics}
}

// inspect reads the skill a directory entry holds, or returns the diagnostic
// that explains why the entry holds none. It returns the zero skill when the
// entry is not a skill at all, which is not a problem: a directory without a
// SKILL.md is simply not a skill.
//
// A skill that is still offered is returned together with the diagnostic that
// lists the recommendations it departs from, so one skill produces at most one
// diagnostic however many problems it has.
func inspect(base string, entry fs.DirEntry) (skill, string) {
	name := entry.Name()
	if strings.HasPrefix(name, ".") {
		return skill{}, ""
	}

	// The entry is stated rather than asked, so a symbolic link is followed and
	// a skill stored outside the workspace is still offered.
	dir := filepath.Join(base, name)
	info, err := os.Stat(dir)
	if err != nil {
		return skill{}, diagnose(locationOf(name), err.Error())
	}
	if !info.IsDir() {
		return skill{}, ""
	}

	file := filepath.Join(dir, skillFile)
	info, err = os.Stat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return skill{}, ""
	case err != nil:
		return skill{}, diagnose(locationOf(name), err.Error())
	case !info.Mode().IsRegular():
		return skill{}, ""
	}

	block, err := frontmatterOf(file)
	if err != nil {
		return skill{}, diagnose(locationOf(name), err.Error())
	}

	parsed, problems := parseFrontmatter(block)
	if parsed.unusable() {
		return skill{}, diagnose(locationOf(name), problems...)
	}

	offered := skill{dir: name, name: parsed.name, description: parsed.description}
	if advisory := recommendations(parsed, name); len(advisory) > 0 {
		return offered, diagnose(locationOf(name), advisory...)
	}
	return offered, ""
}

// frontmatterOf reads the frontmatter of a SKILL.md file, closing the file when
// it is done with it. It never reads the Markdown body of the file.
func frontmatterOf(path string) ([]byte, error) {
	//nolint:gosec // the path is a skill of the session workspace.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open skill: %w", err)
	}
	defer func() { _ = file.Close() }()

	return readFrontmatter(file)
}

// locationOf returns the location of a skill relative to the workspace, always
// with forward slashes and always prefixed with ./, so the model reads the same
// path on every platform and inside a dev container. The structure of the
// format is fixed and one level deep, so the only variable part is the name of
// the skill directory and the path never has to be computed.
func locationOf(dir string) string {
	return locationPrefix + dir + "/" + skillFile
}

// diagnose builds one diagnostic: the location of the file the user has to fix,
// followed by the problems found, joined into a single line.
func diagnose(location string, problems ...string) string {
	return location + ": " + strings.Join(problems, "; ")
}
