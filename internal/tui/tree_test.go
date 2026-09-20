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

		nodes := treeNodes(entries, branch)

		require.Len(t, nodes, 5, "the tool results of a turn are an activity, not a turn")
		require.Equal(
			t,
			[]string{"m1", "m2", "m4", "m5", "m6"},
			nodesField(nodes, func(node treeNode) string { return node.entry.ID }),
		)
	})

	t.Run("places every turn in the tree", func(t *testing.T) {
		entries, branch := branchedDialogue()

		nodes := treeNodes(entries, branch)

		require.Equal(
			t,
			[]int{-1, 0, 1, 1, 3},
			nodesField(nodes, func(node treeNode) int { return node.parent }),
			"a turn follows the nearest turn before it",
		)
		require.Equal(
			t,
			[]int{0, 1, 2, 2, 3},
			nodesField(nodes, func(node treeNode) int { return node.depth }),
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

		nodes := treeNodes(entries, branch)

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

		nodes := treeNodes(entries, entries[1:])

		require.Equal(
			t,
			[]int{-1, -1},
			nodesField(nodes, func(node treeNode) int { return node.parent }),
			"the first prompt written again opens a branch at the root",
		)
		require.Equal(
			t,
			[]int{0, 0},
			nodesField(nodes, func(node treeNode) int { return node.depth }),
		)
		require.True(t, nodes[1].current)
	})

	t.Run("shows the message of a turn on a single line", func(t *testing.T) {
		entries := []session.Entry{turnEntry("m1", "", llm.RoleUser, "  fix\n\n the  bug ")}

		nodes := treeNodes(entries, entries)

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

		nodes := treeNodes(entries, entries)

		require.Equal(t, "(no message)", nodes[0].text)
	})

	t.Run("matches the message and the tag of a turn", func(t *testing.T) {
		entries := []session.Entry{turnEntry("m1", "", llm.RoleUser, "fix the bug")}
		entries[0].Tag = "parser"

		nodes := treeNodes(entries, entries)

		require.Equal(t, "fix the bug parser", nodes[0].search())
	})

	t.Run("shows nothing for a session without turns", func(t *testing.T) {
		require.Empty(t, treeNodes(nil, nil))
	})
}

// TestTreeForks verifies whether writing after a turn opens a branch.
func TestTreeForks(t *testing.T) {
	entries, branch := branchedDialogue()
	nodes := treeNodes(entries, branch)
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
