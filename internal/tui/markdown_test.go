package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestRenderMarkdown verifies the markdown formatting of the answers of the
// model.
func TestRenderMarkdown(t *testing.T) {
	markdown := newStyles(true).markdown

	// render returns the plain text of the rendered markdown, so the tests
	// assert the structure without the styling.
	render := func(text string, width int) string {
		return ansi.Strip(renderMarkdown(text, width, markdown))
	}

	t.Run("renders headings aligned with the body", func(t *testing.T) {
		lines := strings.Split(render("# Title\n\n## Sub", 40), "\n")

		require.Equal(t, "Title", lines[0])
		require.Equal(t, "", lines[1])
		require.Equal(t, "Sub", lines[2])
	})

	t.Run("renders inline spans as their text", func(t *testing.T) {
		for input, want := range map[string]string{
			"a **b** c":  "a b c",
			"a __b__ c":  "a b c",
			"a *b* c":    "a b c",
			"a _b_ c":    "a b c",
			"a ~~b~~ c":  "a b c",
			"a `b` c":    "a b c",
			"see [d](x)": "see d",
		} {
			require.Equal(t, want, render(input, 40), input)
		}
	})

	t.Run("styles the spans it renders", func(t *testing.T) {
		styled := renderMarkdown("**bold** and `code`", 40, markdown)

		require.Contains(t, styled, "\x1b[1m", "bold carries its style")
		require.Contains(t, styled, "\x1b[95m", "code carries its style") // color 13
	})

	t.Run("keeps underscores inside a word", func(t *testing.T) {
		require.Equal(t, "call foo_bar_baz now", render("call foo_bar_baz now", 40))
	})

	t.Run("nests emphasis without dropping the outer style", func(t *testing.T) {
		styled := renderMarkdown("**bold *both* bold**", 40, markdown)

		require.Contains(t, styled, "\x1b[1;3m", "bold and italic combine into one sequence")
		require.Equal(t, "bold both bold", ansi.Strip(styled))
	})

	t.Run("renders bullet lists", func(t *testing.T) {
		require.Equal(t, "• one\n• two", render("- one\n- two", 40))
	})

	t.Run("renders ordered lists", func(t *testing.T) {
		require.Equal(t, "1. one\n2. two", render("1. one\n2. two", 40))
	})

	t.Run("aligns wrapped list content under the marker", func(t *testing.T) {
		lines := strings.Split(render("- "+strings.Repeat("word ", 12), 20), "\n")

		require.Greater(t, len(lines), 1)
		require.Contains(t, lines[0], "• word")
		require.True(t, strings.HasPrefix(lines[1], "  "), "continuation aligns under the text")
	})

	t.Run("renders fenced code without its fences", func(t *testing.T) {
		require.Equal(t, "x := 1\nreturn x", render("```go\nx := 1\nreturn x\n```", 40))
	})

	t.Run("renders blockquotes with a bar", func(t *testing.T) {
		require.Equal(t, "│ quoted", render("> quoted", 40))
	})

	t.Run("renders thematic breaks", func(t *testing.T) {
		require.Equal(t, strings.Repeat("─", 10), render("---", 10))
	})

	t.Run("renders a bordered table", func(t *testing.T) {
		out := render("| Name | Age |\n| --- | --- |\n| Alice | 30 |", 40)

		require.Contains(t, out, "┌")
		require.Contains(t, out, "┼")
		require.Contains(t, out, "└")
		require.Contains(t, out, "Name")
		require.Contains(t, out, "Alice")
		require.Contains(t, out, "30")
	})

	t.Run("detects a table without leading pipes", func(t *testing.T) {
		out := render("Name | Age\n--- | ---\nAlice | 30", 40)

		require.Contains(t, out, "┌")
		require.Contains(t, out, "Alice")
	})

	t.Run("styles the header of the table", func(t *testing.T) {
		styled := renderMarkdown("| Name |\n| --- |\n| Alice |", 40, markdown)

		require.Contains(t, styled, "\x1b[1m", "the header is bold")
	})

	t.Run("aligns cells as the delimiter row declares", func(t *testing.T) {
		require.Equal(t, "x    ", ansi.Strip(padCell("x", 5, alignLeft)))
		require.Equal(t, "    x", ansi.Strip(padCell("x", 5, alignRight)))
		require.Equal(t, "  x  ", ansi.Strip(padCell("x", 5, alignCenter)))
	})

	t.Run("aligns the columns of a table", func(t *testing.T) {
		out := render("| Left | Center | Right |\n|:-----|:------:|------:|\n| a | b | c |", 80)

		require.Contains(t, out, "│ a    │")
		require.Contains(t, out, "│   b    │", "the center column centers its content")
		require.Contains(t, out, "│     c │", "the right column right aligns its content")
	})

	t.Run("grows a row when a cell wraps", func(t *testing.T) {
		out := render("| A | B |\n| --- | --- |\n| "+strings.Repeat("word ", 8)+"| short |", 24)

		require.Greater(t, strings.Count(out, "│"), 6, "the wide cell wraps into more rows")
	})

	t.Run("keeps a table within the width", func(t *testing.T) {
		src := "| alpha | beta | gamma |\n| --- | --- | --- |\n| " +
			strings.Repeat("x", 40) + " | y | z |"

		for _, width := range []int{1, 8, 12, 24, 40, 100} {
			for line := range strings.SplitSeq(ansi.Strip(renderMarkdown(src, width, markdown)), "\n") {
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

	t.Run("does not treat a thematic break as a table", func(t *testing.T) {
		out := render("a | b\n---", 20)

		require.Contains(t, out, "a | b")
		require.NotContains(t, out, "┌")
	})

	t.Run("keeps every line within the width", func(t *testing.T) {
		text := strings.Join([]string{
			"# " + strings.Repeat("heading ", 10),
			"",
			strings.Repeat("paragraph ", 20),
			"",
			"- " + strings.Repeat("item ", 20),
			"",
			"1. " + strings.Repeat("step ", 20),
			"",
			"> " + strings.Repeat("quote ", 20),
			"",
			"```",
			strings.Repeat("code", 40),
			"```",
		}, "\n")

		for _, width := range []int{1, 5, 20, 60} {
			for line := range strings.SplitSeq(ansi.Strip(renderMarkdown(text, width, markdown)), "\n") {
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
}
