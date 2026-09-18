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
