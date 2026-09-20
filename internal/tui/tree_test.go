package tui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/session"
)

// turnEntry builds a stored turn carrying a single text block.
func turnEntry(id, parent string, role llm.Role, text string) session.Entry {
	return session.Entry{
		ID:       id,
		ParentID: parent,
		Message:  textMessage(role, text),
	}
}

// toolResultEntry builds the user turn that carries the result of a tool
// invocation, which the tree skips because it is an activity of a turn.
func toolResultEntry(id, parent string) session.Entry {
	return session.Entry{
		ID:       id,
		ParentID: parent,
		Message: llm.Message{
			Role: llm.RoleUser,
			Blocks: []llm.Block{{
				Type:             llm.BlockToolResult,
				ToolResultCallID: "call_1",
				ToolResult:       []llm.Block{{Type: llm.BlockText, Text: "output"}},
			}},
		},
	}
}

// branchedDialogue returns the entries of a session that branches: the answer
// of the first turn was written again from the turn it followed, and the branch
// the session leaves open is the one that carried a tool invocation. It returns
// the entries of the whole tree and the entries of the active branch.
func branchedDialogue() ([]session.Entry, []session.Entry) {
	entries := []session.Entry{
		turnEntry("m1", "", llm.RoleUser, "first"),
		turnEntry("m2", "m1", llm.RoleAssistant, "one"),
		toolResultEntry("m3", "m2"),
		turnEntry("m4", "m3", llm.RoleAssistant, "two"),
		turnEntry("m5", "m2", llm.RoleAssistant, "other"),
		turnEntry("m6", "m5", llm.RoleUser, "more"),
	}
	return entries, []session.Entry{entries[0], entries[1], entries[2], entries[3]}
}

// nodesField projects every node of a tree through the same accessor, which
// keeps the assertions of the tree tests next to the fields they check.
func nodesField[T any](nodes []treeNode, project func(node treeNode) T) []T {
	fields := make([]T, 0, len(nodes))
	for _, node := range nodes {
		fields = append(fields, project(node))
	}
	return fields
}

// TestTreeNodes verifies the turns the session tree shows and how it places
// them.
func TestTreeNodes(t *testing.T) {
	t.Run("shows one node per turn", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, nil)

		require.Len(t, nodes, 5, "the tool results of a turn are an activity, not a turn")
		require.Equal(
			t,
			[]string{"m1", "m2", "m4", "m5", "m6"},
			nodesField(nodes, func(node treeNode) string { return node.entry.ID }),
		)
	})

	t.Run("places every turn in the tree", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, nil)

		require.Equal(
			t,
			[]int{-1, 0, 1, 1, 3},
			nodesField(nodes, func(node treeNode) int { return node.parent }),
			"a turn follows the nearest turn before it",
		)
		require.Equal(
			t,
			[]int{0, 1, 2, 2, 3},
			nodesField(nodes, func(node treeNode) int { return len(node.guides) }),
			"the guides of a turn place it at its depth",
		)
		require.Equal(
			t,
			[]int{1, 2, 0, 1, 0},
			nodesField(nodes, func(node treeNode) int { return node.children }),
		)
		require.Equal(
			t,
			[]bool{true, true, false, true, true},
			nodesField(nodes, func(node treeNode) bool { return node.last }),
			"the last turn of a group closes it",
		)
	})

	t.Run("marks the branch the session leaves open", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, nil)

		require.Equal(
			t,
			[]bool{true, true, true, false, false},
			nodesField(nodes, func(node treeNode) bool { return node.active }),
		)
		require.Equal(
			t,
			[]bool{false, false, true, false, false},
			nodesField(nodes, func(node treeNode) bool { return node.current }),
			"the session is at the last turn of the active branch",
		)
	})

	t.Run("opens a branch of its own at the root", func(t *testing.T) {
		entries := []session.Entry{
			turnEntry("m1", "", llm.RoleUser, "first"),
			turnEntry("m2", "", llm.RoleUser, "again"),
		}

		nodes := treeNodes(entries, entries[1:], nil)

		require.Equal(
			t,
			[]int{-1, -1},
			nodesField(nodes, func(node treeNode) int { return node.parent }),
			"the first prompt written again opens a branch at the root",
		)
		require.Equal(
			t,
			[]int{0, 0},
			nodesField(nodes, func(node treeNode) int { return len(node.guides) }),
		)
		require.True(t, nodes[1].current)
	})

	t.Run("shows the message of a turn on a single line", func(t *testing.T) {
		entries := []session.Entry{turnEntry("m1", "", llm.RoleUser, "  fix\n\n the  bug ")}

		nodes := treeNodes(entries, entries, nil)

		require.Equal(t, "fix the bug", nodes[0].text)
	})

	t.Run("names the turn of an answer without a message", func(t *testing.T) {
		entries := []session.Entry{{
			ID: "m1",
			Message: llm.Message{
				Role:   llm.RoleAssistant,
				Blocks: []llm.Block{{Type: llm.BlockThinking, Thinking: "plan"}},
			},
		}}

		nodes := treeNodes(entries, entries, nil)

		require.Equal(t, "(no message)", nodes[0].text)
	})

	t.Run("matches the message and the tag of a turn", func(t *testing.T) {
		entries := []session.Entry{turnEntry("m1", "", llm.RoleUser, "fix the bug")}
		entries[0].Tag = "parser"

		nodes := treeNodes(entries, entries, nil)

		require.Equal(t, "fix the bug parser", nodes[0].search())
	})

	t.Run("places a branch beside the turn it follows", func(t *testing.T) {
		// A branch opened from a turn of the past lands under it, however late
		// it was written, so the tree reads in the order it grew.
		entries := []session.Entry{
			turnEntry("m1", "", llm.RoleUser, "first"),
			turnEntry("m2", "m1", llm.RoleAssistant, "one"),
			turnEntry("m3", "m2", llm.RoleUser, "second"),
			turnEntry("m4", "m3", llm.RoleAssistant, "two"),
			turnEntry("m5", "m2", llm.RoleAssistant, "other"),
		}

		nodes := treeNodes(entries, entries, nil)

		require.Equal(
			t,
			[]string{"m1", "m2", "m3", "m4", "m5"},
			nodesField(nodes, func(node treeNode) string { return node.entry.ID }),
			"the branch written last stays under the turn it follows",
		)
		require.Equal(
			t,
			[]int{-1, 0, 1, 2, 1},
			nodesField(nodes, func(node treeNode) int { return node.parent }),
		)
	})

	t.Run("draws the lines that connect a subtree to its turn", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, nil)

		require.Equal(
			t,
			[]string{
				"",
				"   └─ ",
				"      ├─ ",
				"      └─ ",
				"         └─ ",
			},
			nodesField(nodes, func(node treeNode) string {
				return treeGuides(node.guides) + treeConnector(node)
			}),
			"every level of the tree draws the column that keeps it connected",
		)
	})

	t.Run("draws the line of a level that still holds a turn", func(t *testing.T) {
		entries := []session.Entry{
			turnEntry("m1", "", llm.RoleUser, "first"),
			turnEntry("m2", "m1", llm.RoleAssistant, "one"),
			turnEntry("m3", "m2", llm.RoleUser, "second"),
			turnEntry("m4", "m3", llm.RoleAssistant, "two"),
			turnEntry("m5", "m2", llm.RoleAssistant, "other"),
		}

		nodes := treeNodes(entries, entries, nil)

		require.Equal(
			t,
			[]string{"", "   └─ ", "      ├─ ", "      │  └─ ", "      └─ "},
			nodesField(nodes, func(node treeNode) string {
				return treeGuides(node.guides) + treeConnector(node)
			}),
			"the branch that follows a turn keeps the line of the turn it hangs from",
		)
	})

	t.Run("shows nothing for a session without turns", func(t *testing.T) {
		require.Empty(t, treeNodes(nil, nil, nil))
	})
}

// TestTreeFolding verifies hiding the turns that follow a turn, which lets the
// reader walk a long tree a subtree at a time.
func TestTreeFolding(t *testing.T) {
	t.Run("keeps the turns inside a folded subtree out of the nodes", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, map[string]bool{"m2": true})

		require.Equal(
			t,
			[]string{"m1", "m2"},
			nodesField(nodes, func(node treeNode) string { return node.entry.ID }),
		)
		require.True(t, nodes[1].folded)
		require.Equal(t, 2, nodes[1].children, "a folded turn keeps the count of its children")
	})

	t.Run("folds the subtree of the highlighted turn", func(t *testing.T) {
		tree := tree{entries: nil}
		entries, branch := branchedDialogue()
		tree.entries, tree.branch = entries, branch
		tree.filter = newFilter(
			0,
			func(int) string { return "" },
			"Search turns",
			newStyles(true),
			true,
		)
		tree.fold()
		require.Len(t, tree.nodes, 5)

		require.True(t, tree.foldChildren(1), "the first answer holds turns")
		require.Len(t, tree.nodes, 2)
		require.True(t, tree.nodes[1].folded)

		require.True(t, tree.foldChildren(1), "the folded turn unfolds again")
		require.Len(t, tree.nodes, 5)
		require.False(t, tree.nodes[1].folded)
	})

	t.Run("keeps the highlight on the turn it held", func(t *testing.T) {
		// A second turn opens a branch of its own, so folding the first one
		// leaves the reader where it stands.
		entries := []session.Entry{
			turnEntry("m1", "", llm.RoleUser, "first"),
			turnEntry("m2", "m1", llm.RoleAssistant, "one"),
			turnEntry("m3", "m2", llm.RoleUser, "second"),
			turnEntry("m4", "m3", llm.RoleAssistant, "two"),
			turnEntry("m5", "", llm.RoleUser, "other"),
		}
		tree := tree{entries: entries, branch: entries, folded: map[string]bool{}}
		tree.filter = newFilter(
			0,
			func(int) string { return "" },
			"Search turns",
			newStyles(true),
			true,
		)
		tree.fold()

		tree.filter.cursor = 4
		require.Equal(t, "m5", tree.entryID(tree.filter.selected()))

		require.True(t, tree.foldChildren(1))

		require.Equal(t, "m5", tree.entryID(tree.filter.selected()))
		require.Len(t, tree.nodes, 3)
	})

	t.Run("lands on the turn that folds the subtree it hid", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := tree{entries: entries, branch: branch, folded: map[string]bool{}}
		tree.filter = newFilter(
			0,
			func(int) string { return "" },
			"Search turns",
			newStyles(true),
			true,
		)
		tree.fold()

		// The reader stands inside the subtree the fold hides, so the
		// highlight climbs to the turn that folded it.
		tree.filter.cursor = 4
		require.Equal(t, "m6", tree.entryID(tree.filter.selected()))

		tree.foldChildren(1)

		require.Equal(t, "m2", tree.entryID(tree.filter.selected()))
	})

	t.Run("reports the turns that hold nothing", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := tree{entries: entries, branch: branch, folded: map[string]bool{}}
		tree.filter = newFilter(
			0,
			func(int) string { return "" },
			"Search turns",
			newStyles(true),
			true,
		)
		tree.fold()

		require.False(t, tree.foldChildren(2), "the answer the session is at closes its branch")
		require.False(t, tree.foldChildren(-1))
		require.False(t, tree.foldChildren(len(tree.nodes)))
	})
}

// TestTreeForks verifies whether writing after a turn opens a branch.
func TestTreeForks(t *testing.T) {
	entries, branch := branchedDialogue()
	nodes := treeNodes(entries, branch, nil)
	tree := tree{nodes: nodes}

	t.Run("opens a branch after an answer that has turns after it", func(t *testing.T) {
		require.True(t, tree.forks(1), "the first answer was continued by another turn")
	})

	t.Run("continues the branch after the answer that closes it", func(t *testing.T) {
		require.False(t, tree.forks(2), "nothing follows the answer the session is at")
	})

	t.Run("opens a branch after a prompt, which a new one replaces", func(t *testing.T) {
		require.True(t, tree.forks(0))
		require.True(t, tree.forks(4), "a prompt is always written again beside itself")
	})
}
