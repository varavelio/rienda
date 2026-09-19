package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestMarkdownRenderer verifies the markdown rendering of the model answers.
func TestMarkdownRenderer(t *testing.T) {
	// render returns the plain text of a rendered answer, so the tests assert
	// the structure without the styling.
	render := func(text string, width int) string {
		var renderer markdownRenderer
		return ansi.Strip(renderer.render(text, width, true))
	}

	t.Run("renders the markdown structure", func(t *testing.T) {
		out := render("# Title\n\nA paragraph with **bold** and `code`.\n\n- item", 80)

		require.Contains(t, out, "Title")
		require.NotContains(t, out, "# Title", "the heading marker is consumed")
		require.NotContains(t, out, "**", "the emphasis markers are consumed")
		require.NotContains(t, out, "`", "the code markers are consumed")
		require.Contains(t, out, "A paragraph with bold")
		require.Contains(t, out, "code")
		require.Contains(t, out, "• item")
	})

	t.Run("renders tables as a grid", func(t *testing.T) {
		out := render("| Name | Age |\n| --- | --- |\n| Alice | 30 |", 80)

		require.Contains(t, out, "│", "the table draws its column separators")
		require.Contains(t, out, "Name")
		require.Contains(t, out, "Age")
		require.Contains(t, out, "Alice")
		require.Contains(t, out, "30")
	})

	t.Run("renders blockquotes and code blocks", func(t *testing.T) {
		out := render("> quoted\n\n```go\nfunc main() {}\n```", 80)

		require.Contains(t, out, "quoted")
		require.Contains(t, out, "func main() {}")
		require.NotContains(t, out, "```", "the fence markers are consumed")
	})

	t.Run("keeps every line within the width", func(t *testing.T) {
		src := "## Heading\n\nA long token: https://example.com/aaaaaaaaaaaaaaaaaaaaaa\n\n" +
			"| Name | Age | City |\n|:--|:-:|--:|\n| Alice | 30 | New York |\n\n" +
			"```go\nfunc verylongfunctionname(argument string) string { return argument }\n```\n\n- bullet\n"

		for _, width := range []int{1, 10, 16, 24, 40, 80} {
			for line := range strings.SplitSeq(ansi.Strip(render(src, width)), "\n") {
				require.LessOrEqual(
					t,
					ansi.StringWidth(line),
					width,
					"width %d line %q",
					width,
					line,
				)
			}
		}
	})

	t.Run("aligns the content to the left", func(t *testing.T) {
		require.Equal(t, "a paragraph line", render("a paragraph line", 80))
	})

	t.Run("trims the blank cells glamour pads the lines with", func(t *testing.T) {
		var renderer markdownRenderer
		styled := renderer.render("a paragraph line", 80, true)

		require.NotContains(t, styled, "  ", "no run of padded cells survives")
		require.NotContains(t, styled, "line ", "no padded cell survives")
		require.True(t, strings.HasSuffix(styled, reset), "the line closes its styling")
	})

	t.Run("renders the same answer for both terminal backgrounds", func(t *testing.T) {
		var dark, light markdownRenderer

		require.Contains(t, ansi.Strip(dark.render("# Title", 80, true)), "Title")
		require.Contains(t, ansi.Strip(light.render("# Title", 80, false)), "Title")
	})

	t.Run("falls back to the raw text without a width", func(t *testing.T) {
		var renderer markdownRenderer

		require.Equal(t, "# Title", renderer.render("# Title", 0, true))
	})
}

// TestTrimTrailingBlank verifies the trimming of the blank cells glamour pads
// the rendered lines with.
func TestTrimTrailingBlank(t *testing.T) {
	t.Run("drops padded cells and their styling", func(t *testing.T) {
		line := "\x1b[38;5;252mhi\x1b[m" + strings.Repeat("\x1b[38;5;252m \x1b[m", 3)

		require.Equal(t, "\x1b[38;5;252mhi"+reset, trimTrailingBlank(line))
	})

	t.Run("keeps a line that only holds styling empty", func(t *testing.T) {
		require.Equal(t, "", trimTrailingBlank(strings.Repeat("\x1b[38;5;252m \x1b[m", 4)))
	})

	t.Run("stops before an OSC-8 hyperlink close", func(t *testing.T) {
		line := "\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\"

		require.Equal(t, line+reset, trimTrailingBlank(line))
	})
}
