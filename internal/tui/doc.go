// Package tui implements the interactive terminal user interface of Rienda.
//
// The interface is the default mode of the binary: when the command line
// entry point receives no command it delegates here. It offers the previous
// sessions of the workspace to continue, lists the available agent
// definitions to start a new one, opens the chosen session through the harness
// package and renders the events of every run as they arrive. The prompt
// accepts several lines, so the user can write long instructions before
// sending them.
//
// The interface is built around a single Bubble Tea model so the whole state
// is explicit and testable: the model never performs input or output, it only
// reacts to messages, and the rendering lives in its own file. Sessions and
// runs are reached through the small Session interface, which keeps the model
// independent from providers and from the filesystem.
package tui
