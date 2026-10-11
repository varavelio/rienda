// Package tui implements the interactive terminal interface of Rienda.
//
// It is the default mode of the binary: it lists and opens sessions, runs
// prompts through the harness package, and renders the streamed events as a
// conversation. The interface is a single Bubble Tea model with rendering in
// a separate file, driven through a small Session interface that keeps it
// independent from providers and the filesystem.
//
// It also provides fuzzy-filtered pickers, a command center, a session tree
// for navigating branches, and file mentions for pointing the model at
// project paths.
package tui
