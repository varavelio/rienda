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
// output. The conversation scrolls with the arrow keys or one notch of the
// mouse wheel, Page Up and Page Down move from turn to turn, landing on the
// prompts and the answers and skipping the reasoning and the tool invocations,
// and Home and End jump to either end; the position is kept while a run
// streams, so reading back never fights the incoming output. The wheel always
// scrolls the conversation, wherever the pointer rests, and never the prompt:
// the prompt keeps the arrows for its own cursor, so a long message is read
// while a long prompt is written. The interface reports the mouse for the wheel
// alone: a click or a drag is ignored. Every turn closes with how long the agent worked on it,
// shown as a faint footnote under its last message and, while the run is in
// flight, beside the status line. A turn of a resumed session keeps its time
// too: the interface derives it from the moments the turn opened and closed,
// which the session file already stores, so reopening a conversation never
// loses the wait it took.
//
// A single status line closes the conversation and reports what the run is
// doing at the moment, from the model writing an answer to a tool running,
// together with the key that interrupts it. Its spinner is the Varavel fluid
// mark, the same mark that opens the identity line of every phase, kept static
// there, which gives the interface a motif of its own. It is the only
// animation: the reasoning and tool blocks are static, and the footer under the
// input shows the live context figure of the branch the session runs, colored
// by how much of the model window it uses, together with the keys the interface
// listens to. The status line occupies no room once the run is over, so the
// conversation grows into its rows instead of leaving them blank.
//
// Neither the escape that interrupts a run nor the ctrl+c and ctrl+d that leave
// the interface act on a single press: the first one arms the request, which the
// status line, the closing row of the phase or the preparation screen announces
// in place of its hints, and the second press of the same key, within a few
// seconds, runs it, so a stray key never cancels a run nor closes a session.
//
// The command center (ctrl+p) starts a new session, reopens the list that
// starts a new one or continues a previous one, names the session, shows the
// tree of the session, compacts the conversation on demand, and holds the
// options of the harness. Naming the session gives it the title the start list
// offers it under, which is how a session the user comes back to is found
// again; an empty name removes it and leaves the one derived from the first
// message in its place. The name is shown in the identity line of the
// conversation, after the session id, only when the user gave one.
// The manual compaction is offered only when the branch still holds something
// to summarize and no run is in flight, and it shares the code path, the prompt
// and the events of an automatic one. A command the interface cannot run stays
// faint and takes no highlight, and its note says why it cannot run, so the
// reader is never left guessing whether the entry is broken or simply not
// applicable yet. The tool output and the reasoning start
// compact, previewing only their trailing lines so a working session stays
// readable, and can be expanded to their full text; the answers of the model
// can also be shown as plain text instead of markdown. A new session taken from
// the command center leaves the conversation behind and starts an empty one,
// while the reopened list also returns to the conversation it was opened over.
//
// The tree of the session (ctrl+t) shows the whole conversation instead of the
// branch the interface runs: one node per turn, a prompt of the user or an
// answer of the agent, labeled with its author and its message. The tool
// invocations and the reasoning are not nodes: they belong to the turn around
// them and travel with the branch, so a branch keeps the context of the turn it
// starts from. The turns are drawn in the order the conversation grew, each
// subtree under the turn it follows and connected to it by the vertical lines
// of the levels above, so a branch opened from a turn of the past lands beside
// that turn instead of at the end of the tree. Every row opens with the mark of
// the branch its turn belongs to, drawn in a column of its own: a faint dot for
// the turns of the branch the session runs, a bright one for the turn the
// session is at and a blank for the turns of the branches it left behind, so
// the branch the conversation runs reads down that column. Enter returns the
// session to the highlighted turn, leaving the turns that followed it in the
// tree as a branch of their own. Returning to a prompt rewinds to the turn
// before it and offers the prompt in the input, ready to be edited and sent
// again, which is the same as returning to the answer it followed. Every rewind
// is announced by the block above the prompt, between two blank rows, which
// says whether the next message opens a branch or continues the branch the
// session returned to. A turn can also carry a tag (ctrl+t), a word the user
// attaches to it to find it again, which the tree shows before the author in a
// color of its own and the query matches along with the message. The turns that
// follow a turn fold away with ctrl+f, a single key that hides and shows the
// subtree under the highlighted turn, so a long tree is walked a subtree at a
// time. The whole tree folds and unfolds at once with ctrl+a, which shows a
// long conversation as the turns that open a branch, and ctrl+o folds every
// subtree except the branch the session runs, which leaves that branch whole
// beside the branches it left behind. Every key toggles or repeats safely, so
// the reader folds without remembering whether the fold is already in effect;
// folding is how the reader looks at the session, not what it stores, so it
// lasts as long as the tree is open. A checkpoint that summarizes the oldest
// turns is a turn like any other in both places: the conversation renders it
// labeled Compaction where the compaction happened, and the tree draws it as a
// node, so a branch shows at a glance where it was summarized.
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
