package tui

import (
	"strings"

	"charm.land/glamour/v2"
	glamourstyles "charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// escape introduces an ANSI escape sequence.
const escape = '\x1b'

// reset closes every active styling. It is appended to a rendered line so the
// styling of an answer cannot leak into the block that follows it.
const reset = "\x1b[m"

// markdownRenderer renders the markdown of the model answers with glamour. It
// keeps the glamour term renderer it builds, rebuilding it only when the width
// or the terminal background changes, since building one is comparatively
// expensive and the answers stream through many renders.
type markdownRenderer struct {
	renderer *glamour.TermRenderer
	width    int
	isDark   bool
	ready    bool
}

// render formats text as markdown wrapped to width for the given terminal
// background, returning the rendered lines with the blank cells glamour pads
// them with removed. It falls back to the raw text when it cannot render, so a
// rendering problem never hides an answer.
func (mr *markdownRenderer) render(text string, width int, isDark bool) string {
	if width <= 0 {
		return text
	}
	if !mr.ready || mr.width != width || mr.isDark != isDark {
		mr.renderer = newMarkdownRenderer(width, isDark)
		mr.width = width
		mr.isDark = isDark
		mr.ready = true
	}
	if mr.renderer == nil {
		return text
	}

	rendered, err := mr.renderer.Render(text)
	if err != nil {
		return text
	}
	return trimMarkdown(rendered)
}

// newMarkdownRenderer builds the glamour renderer of a width and terminal
// background. The document indentation and margin are dropped so the rendered
// markdown aligns with the label of the block, and table wrapping keeps a wide
// table inside the width.
func newMarkdownRenderer(width int, isDark bool) *glamour.TermRenderer {
	config := glamourstyles.DarkStyleConfig
	if !isDark {
		config = glamourstyles.LightStyleConfig
	}
	zero := uint(0)
	config.Document.Indent = &zero
	config.Document.Margin = &zero
	config.Document.BlockPrefix = ""
	config.Document.BlockSuffix = ""

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(config),
		glamour.WithWordWrap(width),
		glamour.WithTableWrap(true),
	)
	if err != nil {
		return nil
	}
	return renderer
}

// trimMarkdown drops the blank lines glamour wraps the document in and the
// blank cells it pads every line with, so the rendered answer aligns with the
// label of its block.
func trimMarkdown(rendered string) string {
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = trimTrailingBlank(line)
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// trimTrailingBlank removes the trailing blank cells of a rendered line
// together with the SGR sequences that only styled those cells, then closes any
// style left open with a reset. Other escape sequences, such as the OSC-8
// hyperlinks glamour emits, stop the trimming so they are never left dangling.
func trimTrailingBlank(line string) string {
	trimmed := line
	for {
		switch {
		case strings.HasSuffix(trimmed, " "), strings.HasSuffix(trimmed, "\t"):
			trimmed = trimmed[:len(trimmed)-1]
			continue
		}
		if n := trailingSGR(trimmed); n > 0 {
			trimmed = trimmed[:len(trimmed)-n]
			continue
		}
		break
	}

	if ansi.Strip(trimmed) == "" {
		return ""
	}
	return trimmed + reset
}

// trailingSGR returns the length of the SGR sequence that ends line, or zero
// when the line does not end with one.
func trailingSGR(line string) int {
	start := strings.LastIndexByte(line, escape)
	if start < 0 || start >= len(line)-1 {
		return 0
	}
	if line[start+1] != '[' || line[len(line)-1] != 'm' {
		return 0
	}
	for i := start + 2; i < len(line)-1; i++ {
		if char := line[i]; (char < '0' || char > '9') && char != ';' {
			return 0
		}
	}
	return len(line) - start
}
