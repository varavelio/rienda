package tui

import (
	"maps"
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

		nodes := treeNodes(entries, branch, nil, "coder")

		require.Len(t, nodes, 5, "the tool results of a turn are an activity, not a turn")
		require.Equal(
			t,
			[]string{"m1", "m2", "m4", "m5", "m6"},
			nodesField(nodes, func(node treeNode) string { return node.entry.ID }),
		)
	})

	t.Run("places every turn in the tree", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, nil, "coder")

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

		nodes := treeNodes(entries, branch, nil, "coder")

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

		nodes := treeNodes(entries, entries[1:], nil, "coder")

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

		nodes := treeNodes(entries, entries, nil, "coder")

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

		nodes := treeNodes(entries, entries, nil, "coder")

		require.Equal(t, "(no message)", nodes[0].text)
	})

	t.Run("matches the message and the tag of a turn", func(t *testing.T) {
		entries := []session.Entry{turnEntry("m1", "", llm.RoleUser, "fix the bug")}
		entries[0].Tag = "parser"

		nodes := treeNodes(entries, entries, nil, "coder")

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

		nodes := treeNodes(entries, entries, nil, "coder")

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

		nodes := treeNodes(entries, branch, nil, "coder")

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

		nodes := treeNodes(entries, entries, nil, "coder")

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
		require.Empty(t, treeNodes(nil, nil, nil, "coder"))
	})
}

// treeFixture builds the tree screen over the entries and the branch of a
// session, unfolded and ready to fold, with the query the screen gives it.
func treeFixture(entries, branch []session.Entry) tree {
	built := tree{entries: entries, branch: branch}
	built.filter = newFilter(
		0,
		func(int) string { return "" },
		"Search turns",
		newStyles(true),
		true,
	)
	built.fold(nil)
	return built
}

// TestTreeFolding verifies hiding the turns that follow a turn, which lets the
// reader walk a long tree a subtree at a time.
func TestTreeFolding(t *testing.T) {
	t.Run("keeps the turns inside a folded subtree out of the nodes", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch, map[string]bool{"m2": true}, "coder")

		require.Equal(
			t,
			[]string{"m1", "m2"},
			nodesField(nodes, func(node treeNode) string { return node.entry.ID }),
		)
		require.True(t, nodes[1].folded)
		require.Equal(t, 2, nodes[1].children, "a folded turn keeps the count of its children")
	})

	t.Run("folds and unfolds the subtree of the highlighted turn", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)
		require.Len(t, tree.nodes, 5)

		tree.filter.cursor = 1
		tree.toggleFolded()
		require.Len(t, tree.nodes, 2)
		require.True(t, tree.nodes[1].folded)

		tree.toggleFolded()
		require.Len(t, tree.nodes, 5)
		require.False(t, tree.nodes[1].folded)
	})

	t.Run("leaves the tree as it is when the turn holds nothing", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.filter.cursor = 2
		require.Equal(t, "m4", tree.entryID(tree.filter.selected()))

		tree.toggleFolded()

		require.Len(t, tree.nodes, 5)
		require.Empty(t, tree.folded, "a turn nothing follows holds no subtree to fold")
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
		tree := treeFixture(entries, entries)

		tree.filter.cursor = 1
		require.Equal(t, "m2", tree.entryID(tree.filter.selected()))

		tree.toggleFolded()

		require.Equal(
			t,
			"m2",
			tree.entryID(tree.filter.selected()),
			"the highlight stays on the turn it folded instead of jumping to the first one",
		)
		require.Len(t, tree.nodes, 3)
	})

	t.Run("leaves the tree as it is when it shows no turn", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)
		tree.filter.shown = nil

		tree.toggleFolded()

		require.Len(t, tree.nodes, 5)
		require.Empty(t, tree.folded)
	})

	t.Run("keeps the folds the reader already made", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.filter.cursor = 1
		tree.toggleFolded()
		tree.toggleFolded() // the subtree shows again, so another turn can fold
		tree.filter.cursor = 0
		tree.toggleFolded()

		require.Equal(
			t,
			[]string{"m1"},
			nodesField(tree.nodes, func(node treeNode) string { return node.entry.ID }),
		)
		require.Equal(
			t,
			map[string]bool{"m1": true},
			tree.folded,
			"folding a turn keeps the folds around it instead of replacing them",
		)
	})
}

// TestTreeWideFolding verifies folding the whole tree at once and folding every
// subtree except the branch the session runs.
func TestTreeWideFolding(t *testing.T) {
	t.Run("folds every turn that holds a subtree", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.toggleAll()

		require.Equal(
			t,
			[]string{"m1"},
			nodesField(tree.nodes, func(node treeNode) string { return node.entry.ID }),
			"the outline keeps only the turn that opens the conversation",
		)
		require.True(t, tree.nodes[0].folded)
		require.Equal(
			t,
			map[string]bool{"m1": true, "m2": true, "m5": true},
			tree.folded,
			"every turn that holds a subtree folds",
		)
	})

	t.Run("unfolds the whole tree when every subtree is folded", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.toggleAll()
		tree.toggleAll()

		require.Len(t, tree.nodes, 5)
		require.Empty(t, tree.folded)
	})

	t.Run("folds the tree again once a subtree is shown", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.toggleAll()
		tree.toggleFolded() // the turn under the highlight shows its subtree again
		tree.toggleAll()

		require.Equal(
			t,
			[]string{"m1"},
			nodesField(tree.nodes, func(node treeNode) string { return node.entry.ID }),
			"a tree that still shows a subtree folds instead of unfolding",
		)
		require.True(t, tree.foldedWhole())
	})

	t.Run("lands on the turn that folds the subtree it hid", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		// The reader stands inside the subtree the fold hides, so the
		// highlight climbs to the turn that folded it.
		tree.filter.cursor = 4
		require.Equal(t, "m6", tree.entryID(tree.filter.selected()))

		tree.toggleAll()

		require.Equal(
			t,
			"m1",
			tree.entryID(tree.filter.selected()),
			"the highlight climbs to the nearest turn the outline shows",
		)
	})

	t.Run("folds every subtree except the branch the session runs", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.foldOthers()

		require.Equal(
			t,
			[]string{"m1", "m2", "m4", "m5"},
			nodesField(tree.nodes, func(node treeNode) string { return node.entry.ID }),
			"the branch the session runs stays whole beside the branches it left",
		)
		require.False(t, tree.nodes[0].folded, "the turns of the branch keep their subtrees")
		require.False(t, tree.nodes[1].folded)
		require.True(t, tree.nodes[3].folded, "the branch beside it folds away")
	})

	t.Run("keeps a tree that already shows nothing but the branch", func(t *testing.T) {
		entries, branch := branchedDialogue()
		tree := treeFixture(entries, branch)

		tree.foldOthers()
		folded := maps.Clone(tree.folded)
		shown := len(tree.nodes)

		tree.foldOthers()

		require.Equal(t, folded, tree.folded, "folding the others again changes nothing")
		require.Len(t, tree.nodes, shown)
	})
}

// TestTreeForks verifies whether writing after a turn opens a branch.
func TestTreeForks(t *testing.T) {
	entries, branch := branchedDialogue()
	nodes := treeNodes(entries, branch, nil, "coder")
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

// TestTreeCompaction verifies the checkpoint node of the session tree.
func TestTreeCompaction(t *testing.T) {
	t.Run("draws the checkpoint as a labeled node", func(t *testing.T) {
		entry := session.Entry{
			ID:                "c1",
			Kind:              session.KindCompaction,
			CompactionSummary: "the summary",
			CompactionKeptID:  "u1",
		}

		require.True(t, isTurnEntry(entry))
		require.Equal(t, compactionBody, turnText(entry))
		require.Equal(t, "Compaction:", treeName(treeNode{entry: entry}))
	})

	t.Run("renders the checkpoint with its color in the tree", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		first := stored.store.Branch()[0]
		_, err := stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			first.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)

		m.buildTree()
		rendered := m.render()

		require.Contains(t, plain(rendered), "Compaction:")
		require.Contains(t, plain(rendered), compactionBody)
		require.Contains(
			t,
			rendered,
			m.styles.compaction.title.Render("Compaction:"),
			"the node carries the color of a checkpoint",
		)
	})

	t.Run("keeps the tag of a checkpoint visible beside its label", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		first := stored.store.Branch()[0]
		compaction, err := stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			first.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)
		require.NoError(t, stored.store.SetTag(compaction.ID, "checkpoint"))

		m.buildTree()
		rendered := m.render()

		require.Contains(t, plain(rendered), "#checkpoint")
		require.Contains(t, plain(rendered), "Compaction:")
		require.Contains(
			t,
			rendered,
			m.styles.compaction.title.Render("Compaction:"),
			"the checkpoint keeps its white",
		)
		require.Contains(
			t,
			rendered,
			m.styles.tag.Render("#checkpoint"),
			"the tag keeps its own color",
		)
	})

	t.Run("shows a checkpoint that holds children", func(t *testing.T) {
		m, stored := treeModel(t,
			textMessage(llm.RoleUser, "hello"),
			textMessage(llm.RoleAssistant, "hi"),
		)
		first := stored.store.Branch()[0]
		compaction, err := stored.store.AppendCompaction(
			t.Context(),
			"the summary",
			first.ID,
			1,
			"m",
			llm.Usage{},
		)
		require.NoError(t, err)
		_, err = stored.store.Append(t.Context(), session.Entry{
			ParentID: compaction.ID,
			Message:  textMessage(llm.RoleAssistant, "after the checkpoint"),
		})
		require.NoError(t, err)

		m.buildTree()

		require.NotEmpty(t, m.tree.nodes)
		last := m.tree.nodes[len(m.tree.nodes)-1]
		require.Equal(t, session.KindCompaction, m.tree.nodes[len(m.tree.nodes)-2].entry.Kind)
		require.Equal(t, "after the checkpoint", last.text)
	})
}
