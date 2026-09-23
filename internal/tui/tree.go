package tui

import (
	"maps"
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

// treeWindowMargin returns the number of rows the tree keeps visible below the
// highlighted turn: half the rows it shows at once, rounded down, so the
// highlight rests in the middle of the screen and the reader sees as many turns
// before it as after it. The margin is given up near the end of the tree, where
// there are no turns left to reveal, so the tree still fills the screen with
// turns.
func treeWindowMargin(rows int) int {
	return rows / 2
}

// treeGutter opens every row of the tree with the mark that places its turn in
// the conversation, in a column of its own: the dot of the branch the session
// runs and the dot of the turn the session is at. The turns of the branches the
// session left behind keep the column blank, so the marks of every row stay
// aligned whatever the tree holds and the reader follows the branch the session
// runs by scanning that column, instead of looking for a mark after each
// message.
const (
	// treeBranchMark opens the rows of the branch the session runs, which is
	// the conversation the next turn continues.
	treeBranchMark = "●"
	// treeCurrentMark opens the row of the turn the session is at, from which
	// the next message hangs.
	treeCurrentMark = "●"
	// treeGutterGap fills the gutter of a turn of a branch the session left
	// behind, so a row that carries no mark keeps the column.
	treeGutterGap = " "
)

// treeRowReserve is the columns a row keeps outside the guides and the
// connector that place its turn: the cursor, the gutter that marks the branch,
// the author and the blank between the author and the message. It counts the
// runes of the widest of each, because the cursor, the gutter and the mark are
// single glyphs the terminal draws in one column each. The guides and the
// connector are measured from the turn itself, because they grow with its level
// and the message must never push them out of the row.
var treeRowReserve = utf8.RuneCountInString("Agent (agent): ") + // the author
	utf8.RuneCountInString("› ") + // the cursor of a row
	utf8.RuneCountInString(treeCurrentMark) + // the mark of the gutter
	utf8.RuneCountInString("  ") // the gutter blank and the one before the message

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

	// owner is the agent the session was created with, which labels the turns
	// written before the first agent selection of the branch.
	owner string

	// folded names the turns whose children the user hid, by entry identifier.
	// It is what the reader decided, not what the session stores, so it lives
	// with the screen and is gone once the tree closes. The empty set shows
	// every turn.
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

	// agent names the agent that wrote the turn, which labels it. A
	// conversation that switched agent shows in the tree which agent wrote
	// what.
	agent string

	// parent is the index of the turn the node follows, or -1 when the node
	// opens a branch.
	parent int

	// guides holds, for every branch above the node, whether that branch still
	// holds a turn to close. A branch that does draws the vertical line that
	// connects the turns of a subtree to the turn they follow, so the tree
	// reads as a tree instead of a list of rows. Its length is the level of the
	// node: every branch on the path to it opens a level, and the turns that
	// continue one another share it.
	guides []bool

	// children is the number of turns that follow the node.
	children int

	// folded reports that the turn hides the turns that follow it, which the
	// user folded to walk a long tree. A folded turn keeps the count of its
	// children, so the screen can say how many turns it holds.
	folded bool

	// continued reports that the node is the only turn written after the turn
	// before it, so it keeps the level of that turn and is drawn right below it
	// instead of opening a level of its own. It is what keeps a long linear
	// conversation in a single column.
	continued bool

	// open reports that the column of the node still holds a turn to close
	// below it, either a turn written beside it or the turn it continues. It
	// decides the connector the rendering draws for the node.
	open bool

	// active reports that the node belongs to the branch the session leaves
	// open.
	active bool

	// current reports that the node holds the turn the session is at.
	current bool
}

// search returns the text of the node the query of the tree is matched
// against: the message of the turn, the label of the selection it reports and
// the tag that labels it, so a switch is found by what it changed as well as by
// the value it wrote.
func (n treeNode) search() string {
	text := n.text
	switch n.entry.Kind {
	case session.KindAgent, session.KindModel:
		text = selectionLabel(n.entry.Kind) + " " + text
	}
	if n.entry.Tag == "" {
		return text
	}
	return text + " " + n.entry.Tag
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
// session: one node per turn of the conversation and per selection of what the
// branch runs, linked to the turn it follows and placed under it, so the tree
// reads in the order it grew. The branch tells which turns the session leaves
// open, so the tree can mark where the conversation stands, and folded names
// the turns whose children the user hid, which keeps their subtrees out of the
// nodes.
//
// A turn written after the one before it, the only child of its parent, keeps
// the level of that turn and is drawn right below it, so a linear conversation
// reads down a single column however long it grows. Only a turn the
// conversation wrote beside another, a parent with several children, opens a
// level of its own for each branch, so the tree grows to the right when the
// conversation branches and never merely because it is long.
//
// The nodes are walked from the roots of the forest, depth first, so the turns
// of a subtree stay together under the turn they follow however late they were
// written: a branch opened from a turn of the past lands beside it, not at the
// end of the tree.
func treeNodes(entries, branch []session.Entry, folded map[string]bool, owner string) []treeNode {
	active := make(map[string]bool, len(branch))
	for _, entry := range branch {
		active[entry.ID] = true
	}
	current := currentTurn(branch)

	// above links every entry to the node it hangs from, and children groups
	// the nodes by the node they follow, in the order they were written. An
	// activity hangs from the turn it belongs to, so it never breaks the chain
	// between two turns.
	//
	// effective carries, for every entry, the agent the branch runs after it,
	// so a turn is labeled with the agent that wrote it: a selection sets it
	// and the entries that follow inherit it, which is what keeps the tree
	// honest about a conversation that changed agent.
	above := map[string]string{"": ""}
	effective := map[string]string{"": owner}
	children := map[string][]string{}
	stored := make(map[string]session.Entry, len(entries))
	for _, entry := range entries {
		parent := above[entry.ParentID]
		if entry.Kind == session.KindAgent {
			effective[entry.ID] = entry.AgentID
		} else {
			effective[entry.ID] = effective[entry.ParentID]
		}

		if !isTreeNode(entry) {
			above[entry.ID] = parent
			continue
		}
		above[entry.ID] = entry.ID
		stored[entry.ID] = entry
		children[parent] = append(children[parent], entry.ID)
	}

	nodes := make([]treeNode, 0, len(stored))
	index := make(map[string]int, len(stored))
	// walk appends the subtree of a node, carrying the guides of the branches
	// above it. A branch that still holds a turn to close draws its guide, so
	// the turns of a subtree stay connected to the turn they follow.
	var walk func(id string, parent int, guides []bool, continued, open bool)
	walk = func(id string, parent int, guides []bool, continued, open bool) {
		entry := stored[id]
		node := treeNode{
			entry:     entry,
			text:      nodeText(entry),
			agent:     effective[entry.ParentID],
			parent:    parent,
			guides:    guides,
			continued: continued,
			open:      open,
			folded:    folded[id],
			active:    active[id],
			current:   id == current,
			children:  len(children[id]),
		}
		index[id] = len(nodes)
		nodes = append(nodes, node)

		if node.folded {
			return
		}

		switch branches := children[id]; len(branches) {
		case 0:
		case 1:
			// The only turn written after this one continues it, so it keeps
			// the level and reads right below it.
			walk(branches[0], index[id], guides, true, open)
		default:
			// The conversation branches here: every turn opens a level of its
			// own beside the turns it was written with, and the level of this
			// one keeps drawing its guide while a turn of its own follows.
			for position, child := range branches {
				walk(
					child,
					index[id],
					append(slices.Clone(guides), open),
					false,
					position < len(branches)-1,
				)
			}
		}
	}
	roots := children[""]
	for position, root := range roots {
		walk(root, -1, nil, false, position < len(roots)-1)
	}
	return nodes
}

// currentTurn returns the identifier of the node the session is at: the last
// node of the active branch. The branch ends in the active leaf, which is a
// turn of the conversation, a selection of what it runs, or an activity of the
// turn around it, as the tool results an interrupted run leaves behind are.
func currentTurn(branch []session.Entry) string {
	for _, entry := range slices.Backward(branch) {
		if isTreeNode(entry) {
			return entry.ID
		}
	}
	return ""
}

// isTreeNode reports whether an entry is drawn as a node of the session tree: a
// turn of the conversation, a prompt written by the user or an answer of the
// agent, a compaction checkpoint, or a selection of what the branch runs. The
// user turns that carry tool results, the answers that only request tools and
// the reasoning are activities of a turn, so the tree skips them and the reader
// finds the same stops the conversation offers.
func isTreeNode(entry session.Entry) bool {
	return opensTurn(entry) || closesTurn(entry) ||
		entry.Kind == session.KindCompaction ||
		entry.Kind == session.KindAgent ||
		entry.Kind == session.KindModel
}

// nodeText returns the text of one node of the tree on a single line, so a node
// never takes more than one row: the message of a turn, the transition a
// selection wrote, or the note that names a checkpoint.
func nodeText(entry session.Entry) string {
	switch entry.Kind {
	case session.KindCompaction:
		return compactionBody
	case session.KindAgent, session.KindModel:
		return selectionText(entry)
	}

	text := strings.Join(strings.Fields(textOf(entry.Message.Blocks)), " ")
	if text == "" {
		return "(no message)"
	}
	return text
}

// fold rebuilds the nodes of the tree from the entries it holds with the given
// folded turns, keeping the turns they hide out of the nodes and the highlight
// on the turn it held, so folding never moves the reader away from where they
// are. The empty set shows the whole tree.
func (t *tree) fold(folded map[string]bool) {
	held := t.entryID(t.filter.selected())
	t.folded = folded
	t.nodes = treeNodes(t.entries, t.branch, t.folded, t.owner)
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

// toggleFolded folds the highlighted turn when the turns that follow it are
// shown and unfolds it otherwise, so one key walks a long tree a subtree at a
// time. A turn the reader cannot fold, because nothing follows it or because
// the tree shows no turn at all, leaves the tree as it is.
func (t *tree) toggleFolded() {
	index := t.filter.selected()
	if index < 0 || t.nodes[index].children == 0 {
		return
	}

	folded := make(map[string]bool, len(t.folded))
	maps.Copy(folded, t.folded)
	if id := t.nodes[index].entry.ID; t.folded[id] {
		delete(folded, id)
	} else {
		folded[id] = true
	}
	t.fold(folded)
}

// toggleAll folds every turn that holds a subtree when the tree still shows
// one, and unfolds the whole tree when every subtree is folded, so one key
// switches the tree between whole and outline.
func (t *tree) toggleAll() {
	if t.foldedWhole() {
		t.fold(nil)
		return
	}

	folded := make(map[string]bool)
	for _, node := range treeNodes(t.entries, t.branch, nil, t.owner) {
		if node.children > 0 {
			folded[node.entry.ID] = true
		}
	}
	t.fold(folded)
}

// foldOthers folds every turn that holds a subtree except the turns of the
// branch the session runs, so the tree shows the whole branch and leaves the
// branches beside it folded. Folding a tree that already shows nothing but the
// branch keeps it as it is, which makes the fold safe to repeat.
func (t *tree) foldOthers() {
	folded := make(map[string]bool)
	for _, node := range treeNodes(t.entries, t.branch, nil, t.owner) {
		if node.children > 0 && !node.active {
			folded[node.entry.ID] = true
		}
	}
	t.fold(folded)
}

// foldedWhole reports whether every turn of the tree that holds a subtree is
// folded, which is what tells a tree showing nothing but the turns that open a
// branch from one the reader still has something to fold.
func (t *tree) foldedWhole() bool {
	for _, node := range t.nodes {
		if node.children > 0 && !t.folded[node.entry.ID] {
			return false
		}
	}
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
