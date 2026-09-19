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
//
// A run is followed through the conversation the model renders: the answers of
// the agent, the reasoning of the model and the tool invocations with their
// output. The conversation scrolls with the arrow keys, Page Up and Page Down
// move a block at a time and Home and End jump to either end; the position is
// kept while a run streams, so reading back never fights the incoming output.
//
// A single status line closes the conversation and reports what the run is
// doing at the moment, from the model writing an answer to a tool running,
// together with the key that interrupts it. It is the only spinner: the
// reasoning and tool blocks are static, and the footer under the input only
// shows the token usage and the keys the interface listens to.
//
// The command center (ctrl+p) holds the options of the harness, which start
// hiding the tool output and the reasoning so that a working session stays
// readable.
package tui
