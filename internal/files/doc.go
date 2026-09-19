// Package files lists the files of a project.
//
// A project is a directory tree of source files. Entries returns the files the
// tree holds together with the directories that contain them, as paths
// relative to the root, sorted by path, and leaves out everything the project
// ignores. The rules of the .gitignore and .ignore files of the tree are
// honored at every level, with the same semantics git applies to them: a
// pattern without a slash matches at any depth, a leading or embedded slash
// anchors it to the directory that declares it, a trailing slash limits it to
// directories and a leading exclamation mark re-includes a match. The content
// of the version control directories is never listed.
//
// The rules are evaluated in process, so the listing is the same whether or
// not git is installed and whether or not the tree is a repository.
package files
