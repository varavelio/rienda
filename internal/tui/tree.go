package tui

import (
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// treeTagPrompt opens the input that labels a turn, which reads as the sharp
// the tree shows before every tag.
const treeTagPrompt = "# "

// treeLevel is the width of one level of the tree: the column a turn of that
// level opens and the gap before the next one.
const treeLevel = "   "

// treeLine draws the column of a level that still holds a turn to close, which
// connects the turns of a subtree to the turn they follow.
const treeLine = "│  "

// treeGap keeps the column of a level that holds nothing more, so the turns
// below stay aligned with the ones above.
const treeGap = "   "

// treeMessageMax caps the columns the message of a turn may take, so a long
// message never floods the tree however wide the terminal is.
const treeMessageMax = 96

// treeMessageReserve is the columns a row keeps outside the message of its
// turn: the author, the connector that places the turn and the marks that close
// the row, which the message must never push out of it. It counts the runes of
// the widest of each, because the connector and the marks are single glyphs the
// terminal draws in one column each. The columns of the levels above the turn
// are measured apart, because they grow with its depth.
var treeMessageReserve = utf8.RuneCountInString("Agent (agent): ") +
	utf8.RuneCountInString("└─ ") +
	utf8.RuneCountInString("  ✓ ●") +
	utf8.RuneCountInString("› ")

// tree is the session tree the interface navigates: the turns of the
// conversation, the query that narrows them and the input that edits the tag
// of one.
type tree struct {
	// entries holds the entries of the session the tree shows, which it keeps
	// to rebuild its nodes whenever the user folds or unfolds a turn.
	entries []session.Entry

	// branch holds the entries of the branch the session runs, which the tree
	// marks.
	branch []session.Entry

	// folded names the turns whose children the user hid, by entry identifier.
	// It is what the reader decided, not what the session stores, so it lives
	// with the screen and is gone once the tree closes.
	folded map[string]bool

	// nodes lists the turns of the session the tree shows, in append order,
	// the subtrees the user folded left out.
	nodes []treeNode

	// filter narrows the turns by the query the user types, which matches the
	// message of a turn and the tag that labels it.
	filter filter

	// tag edits the tag of the highlighted turn while editing is set.
	tag textinput.Model

	// editing reports that the tag input holds the keys instead of the query.
	editing bool

	// err reports the failure of the last change to the session, which the
	// screen shows instead of leaving it with the user.
	err string
}

// treeNode is one turn of the session tree: a prompt written by the user or an
// answer of the agent. The tool invocations and the reasoning that connect two
// turns are not nodes: they belong to the branch around them and travel with
// it, so a branch keeps the context it was opened from.
type treeNode struct {
	// entry is the stored turn the node shows.
	entry session.Entry

	// text is the message of the turn on a single line, which the tree shows
	// and the query is matched against.
	text string

	// parent is the index of the turn the node follows, or -1 when the node
	// opens a branch.
	parent int

	// guides holds, for every level above the node, whether that level still
	// holds a turn to close. A level that does draws the vertical line that
	// connects the turns of a subtree to the turn they follow, so the tree
	// reads as a tree instead of a list of indented rows. Its length is the
	// depth of the node.
	guides []bool

	// children is the number of turns that follow the node.
	children int

	// folded reports that the turn hides the turns that follow it, which the
	// user folded to walk a long tree. A folded turn keeps the count of its
	// children, so the screen can say how many turns it holds.
	folded bool

	// last reports that no turn follows the node inside the group of turns it
	// belongs to, which the rendering draws with the connector that closes a
	// group.
	last bool

	// active reports that the node belongs to the branch the session leaves
	// open.
	active bool

	// current reports that the node holds the turn the session is at.
	current bool
}

// search returns the text of the node the query of the tree is matched
// against: the message of the turn and the tag that labels it.
func (n treeNode) search() string {
	if n.entry.Tag == "" {
		return n.text
	}
	return n.text + " " + n.entry.Tag
}

// newTreeScreen builds the tree screen of the interface: the list that
// narrows the turns of the session and the input that edits their tags. The
// list reads the turns through text, which the model provides, so the screen
// never holds a copy of them.
func newTreeScreen(text func(int) string, base styles, isDark bool) tree {
	tag := textinput.New()
	tag.Prompt = treeTagPrompt
	tag.Placeholder = "tag, empty removes it"
	tag.SetStyles(newFilterStyles(base, isDark))
	tag.Blur()

	return tree{
		filter: newFilter(0, text, "Search turns", base, isDark),
		tag:    tag,
	}
}

// treeNodes builds the nodes of the session tree from the entries of a
// session: one node per turn, linked to the turn it follows and placed under
// it, so the tree reads in the order it grew. The branch tells which turns the
// session leaves open, so the tree can mark where the conversation stands, and
// folded names the turns whose children the user hid, which keeps their
// subtrees out of the nodes.
//
// The nodes are walked from the roots of the forest, depth first, so the turns
// of a subtree stay together under the turn they follow however late they were
// written: a branch opened from a turn of the past lands beside it, not at the
// end of the tree.
func treeNodes(entries, branch []session.Entry, folded map[string]bool) []treeNode {
	active := make(map[string]bool, len(branch))
	for _, entry := range branch {
		active[entry.ID] = true
	}
	current := currentTurn(branch)

	// above links every entry to the turn it hangs from, and children groups
	// the turns by the turn they follow, in the order they were written. An
	// activity hangs from the turn it belongs to, so it never breaks the chain
	// between two turns.
	above := map[string]string{"": ""}
	children := map[string][]string{}
	turns := make(map[string]session.Entry, len(entries))
	for _, entry := range entries {
		parent := above[entry.ParentID]
		if !isTurnEntry(entry) {
			above[entry.ID] = parent
			continue
		}
		above[entry.ID] = entry.ID
		turns[entry.ID] = entry
		children[parent] = append(children[parent], entry.ID)
	}

	nodes := make([]treeNode, 0, len(turns))
	index := make(map[string]int, len(turns))
	// walk appends the subtree of a turn, carrying the guides of the levels
	// above it. A level that still holds a turn to close draws its line, so the
	// turns of a subtree stay connected to the turn they follow.
	var walk func(id string, parent int, guides []bool)
	walk = func(id string, parent int, guides []bool) {
		entry := turns[id]
		siblings := children[above[entry.ParentID]]

		node := treeNode{
			entry:    entry,
			text:     turnText(entry),
			parent:   parent,
			guides:   guides,
			last:     id == siblings[len(siblings)-1],
			folded:   folded[id],
			active:   active[id],
			current:  id == current,
			children: len(children[id]),
		}
		index[id] = len(nodes)
		nodes = append(nodes, node)

		if node.folded {
			return
		}

		// The level of the turn draws a line while a turn of its own group
		// follows it. A turn that opens a branch holds no level of its own, so
		// the turns below it start at the root.
		line := false
		if parent >= 0 {
			line = !node.last
		}
		for _, child := range children[id] {
			walk(child, index[id], append(slices.Clone(guides), line))
		}
	}
	for _, root := range children[""] {
		walk(root, -1, nil)
	}
	return nodes
}

// currentTurn returns the identifier of the turn the session is at: the last
// turn of the active branch. The branch ends in the active leaf, which is a
// turn of the conversation or an activity of the turn around it, as the tool
// results an interrupted run leaves behind are.
func currentTurn(branch []session.Entry) string {
	for _, entry := range slices.Backward(branch) {
		if isTurnEntry(entry) {
			return entry.ID
		}
	}
	return ""
}

// isTurnEntry reports whether an entry is a turn of the conversation: a prompt
// written by the user or an answer of the agent. The user turns that carry
// tool results and the answers that only request tools are activities of a
// turn, so the tree skips them and the reader finds the same stops the
// conversation offers.
func isTurnEntry(entry session.Entry) bool {
	return opensTurn(entry) || closesTurn(entry)
}

// turnText returns the message of one turn on a single line, so a turn of the
// tree never takes more than one row.
func turnText(entry session.Entry) string {
	text := strings.Join(strings.Fields(textOf(entry.Message.Blocks)), " ")
	if text == "" {
		return "(no message)"
	}
	return text
}

// fold builds the tree from the entries it holds, keeping the turns the user
// folded out of the nodes and the highlight on the turn it held, so folding
// never moves the reader away from where they are.
func (t *tree) fold() {
	held := t.entryID(t.filter.selected())
	t.nodes = treeNodes(t.entries, t.branch, t.folded)
	t.filter.setCount(len(t.nodes))
	t.filter.cursor = t.focusOn(held)
}

// focusOn returns the position of the turn identified by id, or of the nearest
// turn before it the tree still shows, which is the turn that was folded when
// the reader stood inside the subtree it hides. It returns the first position
// when the tree no longer shows any turn of the path.
func (t *tree) focusOn(id string) int {
	position := make(map[string]int, len(t.entries))
	for index, entry := range t.entries {
		position[entry.ID] = index
	}

	for id != "" {
		if at, found := t.position(id); found {
			return at
		}
		index, found := position[id]
		if !found {
			break
		}
		id = t.entries[index].ParentID
	}
	return 0
}

// position returns the position the highlight of the tree holds the turn
// identified by id, which may have moved when a subtree was folded.
func (t *tree) position(id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	for position, index := range t.filter.shown {
		if t.nodes[index].entry.ID == id {
			return position, true
		}
	}
	return 0, false
}

// entryID returns the identifier of the turn at a node index, or the empty
// string when there is no such turn.
func (t *tree) entryID(index int) string {
	if index < 0 || index >= len(t.nodes) {
		return ""
	}
	return t.nodes[index].entry.ID
}

// foldChildren folds or unfolds the children of the turn at a node index,
// reporting whether the turn holds any. Folding a turn hides the turns that
// follow it, which lets the reader walk a long tree a subtree at a time.
func (t *tree) foldChildren(index int) bool {
	if index < 0 || index >= len(t.nodes) || t.nodes[index].children == 0 {
		return false
	}

	if t.folded == nil {
		t.folded = make(map[string]bool)
	}
	id := t.nodes[index].entry.ID
	if t.folded[id] {
		delete(t.folded, id)
	} else {
		t.folded[id] = true
	}
	t.fold()
	return true
}

// focus puts the highlight on the turn the session is at, so the tree opens
// where the conversation stands.
func (t *tree) focus() {
	for position, index := range t.filter.shown {
		if t.nodes[index].current {
			t.filter.cursor = position
			return
		}
	}
}

// forks reports whether writing after the highlighted turn opens a branch: the
// message would hang from the turn the session returned to, beside the turns
// that already follow it, instead of continuing them.
func (t *tree) forks(index int) bool {
	node := t.nodes[index]
	if node.entry.Message.Role == llm.RoleUser {
		// Returning to a prompt rewrites it after the turn before it, so the
		// prompt it replaces stays in the tree as a branch of its own.
		return true
	}
	return node.children > 0
}

// update forwards a message to the input the tree listens to: the tag input
// while a tag is edited, the query input otherwise.
func (t *tree) update(msg tea.Msg) tea.Cmd {
	if t.editing {
		var cmd tea.Cmd
		t.tag, cmd = t.tag.Update(msg)
		return cmd
	}
	return t.filter.update(msg)
}

// setStyles applies the styles of the current terminal background to the
// inputs of the tree.
func (t *tree) setStyles(base styles, isDark bool) {
	t.filter.setStyles(base, isDark)
	t.tag.SetStyles(newFilterStyles(base, isDark))
}

// setWidth sets the columns the query and the tag inputs show.
func (t *tree) setWidth(width int) {
	t.filter.setWidth(width)
	t.tag.SetWidth(max(1, width-filterPromptWidth))
}
