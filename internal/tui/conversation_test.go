package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConversation verifies the window the interface shows over the rendered
// rows of the transcript.
func TestConversation(t *testing.T) {
	t.Run("fills the window with the newest rows", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree", true)

		require.Equal(t, "two\nthree", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("pads a short conversation above", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(3)
		conversation.appendBlock("one", true)

		require.Equal(t, "\n\none", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("keeps empty blocks aligned with the transcript", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("", true)
		conversation.appendBlock("one", true)

		require.Equal(t, 2, conversation.blockCount())
		require.Equal(t, "\none", conversation.view())
	})

	t.Run("follows the newest rows as they arrive", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo", true)
		conversation.appendBlock("three", true)

		require.Equal(t, "two\nthree", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("scrolls back and stops following", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree", true)

		conversation.scroll(-1)

		require.Equal(t, "one\ntwo", conversation.view())
		require.False(t, conversation.atBottom())

		conversation.appendBlock("four", true)

		require.Equal(t, "one\ntwo", conversation.view())
		require.False(t, conversation.atBottom())
	})

	t.Run("follows again at the bottom", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree", true)
		conversation.scroll(-2)

		conversation.scroll(2)

		require.Equal(t, "two\nthree", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("never scrolls past the ends", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo", true)

		conversation.scroll(-10)
		require.Equal(t, "one\ntwo", conversation.view())

		conversation.scroll(10)
		require.Equal(t, "one\ntwo", conversation.view())
	})

	t.Run("keeps the position when an earlier block is re-rendered", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree", true)
		conversation.scroll(-1)
		require.Equal(t, "one\ntwo", conversation.view())

		// Streaming re-renders the first block: it is dropped and appended
		// again while the next ones keep arriving.
		conversation.truncate(0)
		conversation.appendBlock("one\ntwo\nthree", true)
		conversation.appendBlock("four", true)

		require.Equal(t, "one\ntwo", conversation.view(), "the window stays put")
		require.False(t, conversation.follow, "the window stopped following")
	})

	t.Run("jumps to the top and the bottom", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("a\nb\nc\nd", true)

		conversation.scrollToTop()
		require.Equal(t, "a\nb", conversation.view())
		require.False(t, conversation.follow)

		conversation.scrollToBottom()
		require.Equal(t, "c\nd", conversation.view())
		require.True(t, conversation.follow)
	})

	t.Run("moves from turn to turn", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("a1\na2", true)
		conversation.appendBlock("b1\nb2", true)
		conversation.appendBlock("c1\nc2", true)
		// Rows: a1 a2 b1 b2 c1 c2, turns start at 0, 2, 4.

		conversation.scrollBlock(-1)
		require.Equal(t, 2, conversation.offsetRows(), "back to the previous turn")

		conversation.scrollBlock(-1)
		require.Equal(t, 0, conversation.offsetRows(), "back to the turn before it")

		conversation.scrollBlock(-1)
		require.Equal(t, 0, conversation.offsetRows(), "the top is the end of the way back")

		conversation.scrollBlock(1)
		require.Equal(t, 2, conversation.offsetRows(), "forward to the next turn")

		conversation.scrollBlock(1)
		require.Equal(t, 4, conversation.offsetRows(), "forward to the last turn")

		conversation.scrollBlock(1)
		require.True(t, conversation.follow, "the end of the way forward follows again")
	})

	t.Run("skips the blocks that are not turns", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(1)
		conversation.appendBlock("you1", true)    // a turn, row 0
		conversation.appendBlock("think1", false) // reasoning, an activity, row 1
		conversation.appendBlock("tool1", false)  // a tool invocation, an activity, row 2
		conversation.appendBlock("agent1", true)  // a turn, row 3
		require.Equal(t, 3, conversation.offsetRows(), "the window rests on the last turn")

		conversation.scrollBlock(-1)
		require.Zero(t, conversation.offsetRows(), "the activities are skipped on the way back")

		conversation.scrollBlock(1)
		require.Equal(t, 3, conversation.offsetRows(), "the next turn skips the activities")

		conversation.scrollBlock(-1)
		require.Zero(t, conversation.offsetRows(), "the way back lands on the turn itself")

		// With no turn further down, the way forward reaches the end.
		conversation.scrollBlock(1)
		conversation.scrollBlock(1)
		require.True(t, conversation.follow, "the end of the conversation is reached")
	})

	t.Run("drops the blocks from an index on", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(3)
		conversation.appendBlock("one", true)
		conversation.appendBlock("two", true)
		conversation.appendBlock("three", true)

		conversation.truncate(1)

		require.Equal(t, 1, conversation.blockCount())
		require.Equal(t, "\n\none", conversation.view())

		conversation.appendBlock("again", true)
		require.Equal(t, "\none\nagain", conversation.view())
	})

	t.Run("invalidates every block", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one", true)
		conversation.appendBlock("two", true)

		conversation.invalidate()

		require.Equal(t, 0, conversation.blockCount())
		require.Empty(t, strings.TrimSpace(conversation.view()))
	})
}
