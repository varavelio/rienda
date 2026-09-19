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
		conversation.appendBlock("one\ntwo\nthree")

		require.Equal(t, "two\nthree", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("pads a short conversation above", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(3)
		conversation.appendBlock("one")

		require.Equal(t, "\n\none", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("keeps empty blocks aligned with the transcript", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("")
		conversation.appendBlock("one")

		require.Equal(t, 2, conversation.blockCount())
		require.Equal(t, "\none", conversation.view())
	})

	t.Run("follows the newest rows as they arrive", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo")
		conversation.appendBlock("three")

		require.Equal(t, "two\nthree", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("scrolls back and stops following", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree")

		conversation.scroll(-1)

		require.Equal(t, "one\ntwo", conversation.view())
		require.False(t, conversation.atBottom())

		conversation.appendBlock("four")

		require.Equal(t, "one\ntwo", conversation.view())
		require.False(t, conversation.atBottom())
	})

	t.Run("follows again at the bottom", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree")
		conversation.scroll(-2)

		conversation.scroll(2)

		require.Equal(t, "two\nthree", conversation.view())
		require.True(t, conversation.atBottom())
	})

	t.Run("never scrolls past the ends", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo")

		conversation.scroll(-10)
		require.Equal(t, "one\ntwo", conversation.view())

		conversation.scroll(10)
		require.Equal(t, "one\ntwo", conversation.view())
	})

	t.Run("keeps the position when an earlier block is re-rendered", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one\ntwo\nthree")
		conversation.scroll(-1)
		require.Equal(t, "one\ntwo", conversation.view())

		// Streaming re-renders the first block: it is dropped and appended
		// again while the next ones keep arriving.
		conversation.truncate(0)
		conversation.appendBlock("one\ntwo\nthree")
		conversation.appendBlock("four")

		require.Equal(t, "one\ntwo", conversation.view(), "the window stays put")
		require.False(t, conversation.follow, "the window stopped following")
	})

	t.Run("jumps to the top and the bottom", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("a\nb\nc\nd")

		conversation.scrollToTop()
		require.Equal(t, "a\nb", conversation.view())
		require.False(t, conversation.follow)

		conversation.scrollToBottom()
		require.Equal(t, "c\nd", conversation.view())
		require.True(t, conversation.follow)
	})

	t.Run("moves block by block", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("a1\na2")
		conversation.appendBlock("b1\nb2")
		conversation.appendBlock("c1\nc2")
		// Rows: a1 a2 b1 b2 c1 c2, blocks start at 0, 2, 4.

		conversation.scrollBlock(-1)
		require.Equal(t, 2, conversation.offsetRows(), "back to the previous block")

		conversation.scrollBlock(-1)
		require.Equal(t, 0, conversation.offsetRows(), "back to the block before it")

		conversation.scrollBlock(-1)
		require.Equal(t, 0, conversation.offsetRows(), "the top is the end of the way back")

		conversation.scrollBlock(1)
		require.Equal(t, 2, conversation.offsetRows(), "forward to the next block")

		conversation.scrollBlock(1)
		require.Equal(t, 4, conversation.offsetRows(), "forward to the last block")

		conversation.scrollBlock(1)
		require.True(t, conversation.follow, "the end of the way forward follows again")
	})

	t.Run("drops the blocks from an index on", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(3)
		conversation.appendBlock("one")
		conversation.appendBlock("two")
		conversation.appendBlock("three")

		conversation.truncate(1)

		require.Equal(t, 1, conversation.blockCount())
		require.Equal(t, "\n\none", conversation.view())

		conversation.appendBlock("again")
		require.Equal(t, "\none\nagain", conversation.view())
	})

	t.Run("invalidates every block", func(t *testing.T) {
		conversation := newConversation()
		conversation.setHeight(2)
		conversation.appendBlock("one")
		conversation.appendBlock("two")

		conversation.invalidate()

		require.Equal(t, 0, conversation.blockCount())
		require.Empty(t, strings.TrimSpace(conversation.view()))
	})
}
