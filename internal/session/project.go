package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// projectHashLength is the number of hexadecimal characters of the project
// hash appended to a project identifier.
const projectHashLength = 8

// projectSlugMaxBytes caps the readable part of a project identifier so the
// directory name always fits in a portable file name.
const projectSlugMaxBytes = 100

// ProjectID returns the directory-safe identifier of a project, derived from
// the path or identifier injected by the caller. The identifier combines a
// readable slug with a short hash of the cleaned input, so projects with
// colliding slugs never share a directory:
//
//	ProjectID("/home/eduardo/projects/myproject")
//	// home-eduardo-projects-myproject-3f9a1c2d
//
// Equivalent spellings of the same path, like a trailing separator, produce
// the same identifier.
func ProjectID(project string) (string, error) {
	trimmed := strings.TrimSpace(project)
	if trimmed == "" {
		return "", errors.New("session: project is required")
	}

	canonical := filepath.Clean(trimmed)
	slug := truncateProjectSlug(projectSlug(canonical))
	if slug == "" {
		return "", fmt.Errorf(
			"session: project %q has no characters usable in a directory name",
			project,
		)
	}

	sum := sha256.Sum256([]byte(canonical))
	return slug + "-" + hex.EncodeToString(sum[:])[:projectHashLength], nil
}

// ProjectDir returns the directory that holds the sessions of the project
// identified by projectID inside baseDir. The identifier must come from
// ProjectID.
func ProjectDir(baseDir, projectID string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return "", errors.New("session: base directory is required")
	}
	if !validDirName(projectID) {
		return "", fmt.Errorf(
			"session: invalid project id %q: use ProjectID to derive it",
			projectID,
		)
	}
	return filepath.Join(baseDir, projectID), nil
}

// projectSlug converts a project path into a readable slug: letters, digits
// and underscores survive, any run of other characters becomes a single
// hyphen and leading or trailing hyphens are dropped.
func projectSlug(path string) string {
	var builder strings.Builder
	builder.Grow(len(path))

	separator := false
	for _, r := range path {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			separator = false
			builder.WriteRune(r)
			continue
		}
		separator = true
	}
	return builder.String()
}

// truncateProjectSlug cuts a slug to the byte budget without splitting a rune
// and without leaving a trailing separator.
func truncateProjectSlug(slug string) string {
	if len(slug) <= projectSlugMaxBytes {
		return slug
	}

	cut := projectSlugMaxBytes
	for cut > 0 && !utf8.RuneStart(slug[cut]) {
		cut--
	}
	return strings.TrimRight(slug[:cut], "-_")
}

// validDirName reports whether name can be a single directory name on every
// supported platform.
func validDirName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}
