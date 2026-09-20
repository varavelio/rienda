// Package tui implements the interactive terminal user interface of Rienda.
//
// The interface is the default mode of the binary: when the command line
// entry point receives no command it delegates here. It opens on a single list
// that starts a new session and continues any previous one of the workspace at
// once, offers the agent definitions to run when a new session needs one, and
// opens the chosen session through the harness package, rendering the events
// of every run as they arrive. The prompt accepts several lines, so the user
// can write long instructions before sending them.
//
// Every list of the interface, from the start list to the agent picker and the
// command center, narrows as the user types: the query input stays focused, so
// writing filters the entries by fuzzy match while the arrows move the
// highlight over the result, which always leads with the best match. A list
// that matches nothing says so instead of showing an empty screen, and escape
// clears the query before it leaves the list.
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
// move from turn to turn, landing on the prompts and the answers and skipping
// the reasoning and the tool invocations, and Home and End jump to either end;
// the position is kept while a run streams, so reading back never fights the
// incoming output. Every turn closes with how long the agent worked on it,
// shown as a faint footnote under its last message and, while the run is in
// flight, beside the status line.
//
// A single status line closes the conversation and reports what the run is
// doing at the moment, from the model writing an answer to a tool running,
// together with the key that interrupts it. Its spinner is the Varavel fluid
// mark, the same mark that opens the identity line of every phase, kept static
// there, which gives the interface a motif of its own. It is the only
// animation: the reasoning and tool blocks are static, and the footer under the
// input only shows the token usage and the keys the interface listens to. The
// status line occupies no room once the run is over, so the conversation grows
// into its rows instead of leaving them blank.
//
// The command center (ctrl+p) starts a new session, reopens the list that
// starts a new one or continues a previous one, and holds the options of the
// harness. The tool output and the reasoning start compact, previewing only
// their trailing lines so a working session stays readable, and can be
// expanded to their full text; the answers of the model can also be shown as
// plain text instead of markdown. A new session taken from the command center
// leaves the conversation behind and starts an empty one, while the reopened
// list also returns to the conversation it was opened over.
//
// An @ in the prompt opens the completion of the files of the project, so the
// user never has to remember a path to point the model at a file. The
// suggestions are ranked by how well they match what the user wrote, the
// arrows move the highlight, and enter and tab complete the highlighted one by
// writing its path where the query was, keeping the @ that opens the mention.
// Escape cancels the completion without touching the prompt. A completed
// directory keeps the completion open so the path can keep narrowing, while a
// completed file closes it. The completion only writes the path: the model is
// the one that reads the file it was pointed at. Every mention reads the
// project again in the background, so a file created while the interface runs
// shows up, and the listing follows the rules of the project instead of the
// state of the machine, so the entries the project ignores never show up.
package tui
