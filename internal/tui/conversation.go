package tui

import "strings"

// conversation holds the rendered rows of the transcript, grouped by block, and
// the window of rows the interface shows.
type conversation struct {
	// rows holds every rendered row, oldest first.
	rows []string

	// starts holds the first row of each block; the value after the last block
	// is the total number of rows.
	starts []int

	// height is the number of rows the window shows.
	height int

	// offset is the index of the first visible row.
	offset int

	// follow keeps the window pinned to the newest rows as they arrive. It is
	// set while the window rests on the bottom and cleared when the user
	// scrolls away, so a re-render of an earlier block cannot drag the window
	// back to the bottom.
	follow bool
}

// newConversation builds an empty conversation with a window of one row.
func newConversation() conversation {
	return conversation{starts: []int{0}, height: 1, follow: true}
}

// blockCount returns the number of blocks the conversation holds.
func (c *conversation) blockCount() int {
	return len(c.starts) - 1
}

// appendBlock renders one block at the end of the conversation. An empty text
// keeps an empty block, so the block indexes stay aligned with the transcript.
// The window follows the new rows only while it rests on the bottom.
func (c *conversation) appendBlock(text string) {
	if text != "" {
		c.rows = append(c.rows, strings.Split(text, "\n")...)
	}
	c.starts = append(c.starts, len(c.rows))
	if c.follow {
		c.offset = c.maxOffset()
	}
}

// truncate drops every block from the given index on.
func (c *conversation) truncate(index int) {
	index = min(max(index, 0), c.blockCount())
	c.rows = c.rows[:c.starts[index]]
	c.starts = c.starts[:index+1]
}

// invalidate drops every rendered block, used when something the rendering
// depends on changes: the terminal width, the styles or the preferences.
func (c *conversation) invalidate() {
	c.truncate(0)
}

// setHeight sets the number of rows the window shows.
func (c *conversation) setHeight(height int) {
	c.height = max(1, height)
}

// atBottom reports whether the newest row is visible.
func (c *conversation) atBottom() bool {
	return c.offsetRows() >= c.maxOffset()
}

// offsetRows returns the index of the first visible row, clamped to the rows
// the conversation holds.
func (c *conversation) offsetRows() int {
	return min(max(c.offset, 0), c.maxOffset())
}

// maxOffset returns the index of the first row of the last window.
func (c *conversation) maxOffset() int {
	return max(0, len(c.rows)-c.height)
}

// scroll moves the window by the given number of rows, negative towards the
// oldest ones and positive towards the newest ones.
func (c *conversation) scroll(delta int) {
	c.move(c.offsetRows() + delta)
}

// scrollBlock moves the window to the start of the previous block when the
// direction is negative, or of the next block when it is positive, so the
// reader advances message by message.
func (c *conversation) scrollBlock(direction int) {
	offset := c.offsetRows()
	target := c.maxOffset()
	if direction < 0 {
		target = 0
		for _, start := range c.starts {
			if start >= offset {
				break
			}
			target = start
		}
	} else {
		for _, start := range c.starts {
			if start > offset {
				target = start
				break
			}
		}
	}
	c.move(target)
}

// scrollToTop moves the window to the oldest rows.
func (c *conversation) scrollToTop() {
	c.move(0)
}

// scrollToBottom moves the window to the newest rows and follows them again.
func (c *conversation) scrollToBottom() {
	c.move(c.maxOffset())
}

// move places the window at the given row, clamping it to the rows the
// conversation holds, and records whether it rests on the bottom.
func (c *conversation) move(offset int) {
	c.offset = min(max(offset, 0), c.maxOffset())
	c.follow = c.atBottom()
}

// view returns the visible rows, padded above so a conversation shorter than
// the window rests on its bottom.
func (c *conversation) view() string {
	offset := c.offsetRows()
	visible := c.rows[offset:min(offset+c.height, len(c.rows))]

	lines := make([]string, 0, c.height)
	if pad := c.height - len(visible); pad > 0 {
		lines = append(lines, make([]string, pad)...)
	}
	lines = append(lines, visible...)
	return strings.Join(lines, "\n")
}
