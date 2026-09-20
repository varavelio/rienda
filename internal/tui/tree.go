package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// treeTagPrompt opens the input that labels a turn, which reads as the sharp
// the tree shows before every tag.
const treeTagPrompt = "# "

// treeIndent is what one level of the tree adds before the turns that follow
// another turn.
const treeIndent = "   "

// tree is the session tree the interface navigates: the turns of the
// conversation, the query that narrows them and the input that edits the tag
// of one.
type tree struct {
	// nodes lists the turns of the session, in append order.
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

	// depth is the number of turns between the node and the turn that opens
	// its path, zero for that one.
	depth int

	// children is the number of turns that follow the node.
	children int

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
// session: one node per turn, in append order, linked to the turn it follows.
// The branch tells which turns the session leaves open, so the tree can mark
// where the conversation stands.
func treeNodes(entries, branch []session.Entry) []treeNode {
	active := make(map[string]bool, len(branch))
	for _, entry := range branch {
		active[entry.ID] = true
	}
	current := currentTurn(branch)

	nodes := make([]treeNode, 0, len(entries))
	ancestor := map[string]int{"": -1}
	lastChild := make(map[int]int, len(entries))
	for _, entry := range entries {
		parent, found := ancestor[entry.ParentID]
		if !found {
			parent = -1
		}
		if !isTurnEntry(entry) {
			ancestor[entry.ID] = parent
			continue
		}

		node := treeNode{
			entry:   entry,
			text:    turnText(entry),
			parent:  parent,
			last:    true,
			active:  active[entry.ID],
			current: entry.ID == current,
		}
		if parent >= 0 {
			node.depth = nodes[parent].depth + 1
			if previous, attached := lastChild[parent]; attached {
				nodes[previous].last = false
			}
			nodes[parent].children++
			lastChild[parent] = len(nodes)
		}

		ancestor[entry.ID] = len(nodes)
		nodes = append(nodes, node)
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
