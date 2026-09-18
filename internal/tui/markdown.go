package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// markdownStyles groups the styles the markdown renderer applies to the
// answers of the model.
type markdownStyles struct {
	// headings styles the headings by level, indexed by level minus one.
	headings []lipgloss.Style

	bold        lipgloss.Style
	italic      lipgloss.Style
	strike      lipgloss.Style
	code        lipgloss.Style
	codeLine    lipgloss.Style
	link        lipgloss.Style
	quote       lipgloss.Style
	quoteBar    lipgloss.Style
	bullet      lipgloss.Style
	rule        lipgloss.Style
	tableHead   lipgloss.Style
	tableBorder lipgloss.Style
}

// heading returns the style of the given heading level, clamped to the levels
// the renderer knows about.
func (ms markdownStyles) heading(level int) lipgloss.Style {
	index := min(max(level, 1), len(ms.headings)) - 1
	return ms.headings[index]
}

// renderMarkdown renders the markdown of a model answer into styled terminal
// lines wrapped to width. It supports the subset of Markdown that models
// commonly produce: headings, paragraphs, fenced and inline code, bullet and
// ordered lists, blockquotes, thematic breaks, tables, emphasis, strikethrough
// and links. Every returned line fits in width.
func renderMarkdown(text string, width int, ms markdownStyles) string {
	base := lipgloss.NewStyle()
	lines := strings.Split(text, "\n")
	rendered := make([]string, 0, len(lines))
	inFence := false

	for i := 0; i < len(lines); i++ {
		line := trimRight(lines[i])

		if isFence(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			rendered = append(rendered, ms.codeLine.Render(clipText(width, line)))
			continue
		}
		if strings.TrimSpace(line) == "" {
			rendered = append(rendered, "")
			continue
		}
		if i+1 < len(lines) && isTableRow(line) && isTableSeparator(trimRight(lines[i+1])) {
			table, last := parseTable(lines, i)
			rendered = append(rendered, renderTable(table, width, ms)...)
			i = last
			continue
		}
		if level, title := heading(line); level > 0 {
			styled := renderInline(title, ms, ms.heading(level))
			rendered = append(rendered, markdownWrap(styled, width)...)
			continue
		}
		if isThematicBreak(line) {
			rendered = append(rendered, ms.rule.Render(strings.Repeat("─", max(1, width))))
			continue
		}
		if content, ok := blockquote(line); ok {
			prefix := ms.quoteBar.Render("│") + " "
			quoted := renderInline(content, ms, ms.quote)
			rendered = append(rendered, markdownPrefixed(prefix, quoted, width)...)
			continue
		}
		if indent, marker, text, ok := listItem(line); ok {
			prefix := indent + ms.bullet.Render(marker)
			inlined := renderInline(text, ms, base)
			rendered = append(rendered, markdownPrefixed(prefix, inlined, width)...)
			continue
		}
		rendered = append(rendered, markdownWrap(renderInline(line, ms, base), width)...)
	}

	// Clipping is a safety net for widths too narrow to fit a prefix, which
	// would otherwise push a line past the edge of the terminal.
	for i, line := range rendered {
		rendered[i] = clipText(width, line)
	}
	return strings.Join(rendered, "\n")
}

// markdownWrap wraps styled text to width, splitting the result into lines.
func markdownWrap(text string, width int) []string {
	if width <= 0 {
		return strings.Split(text, "\n")
	}
	return strings.Split(lipgloss.Wrap(text, width, ""), "\n")
}

// markdownPrefixed renders a line that starts with a prefix, wrapping its
// content to the remaining width and aligning every continuation line under
// the content.
func markdownPrefixed(prefix, content string, width int) []string {
	pad := lipgloss.Width(prefix)
	lines := markdownWrap(content, max(1, width-pad))
	if len(lines) == 0 {
		lines = []string{""}
	}

	lines[0] = prefix + lines[0]
	blank := strings.Repeat(" ", pad)
	for i := 1; i < len(lines); i++ {
		lines[i] = blank + lines[i]
	}
	return lines
}

// renderInline renders the inline spans of one markdown line, applying base to
// the text that carries no span of its own. Spans nest, so emphasis inside a
// heading keeps the heading style.
func renderInline(text string, ms markdownStyles, base lipgloss.Style) string {
	return renderSpans([]rune(text), ms, base)
}

// renderSpans walks the runes of a line, applying the style of every span it
// finds over the style inherited from the enclosing span.
func renderSpans(runes []rune, ms markdownStyles, base lipgloss.Style) string {
	var out strings.Builder
	var plain []rune
	flush := func() {
		if len(plain) > 0 {
			out.WriteString(base.Render(string(plain)))
			plain = plain[:0]
		}
	}

	for i := 0; i < len(runes); {
		inner, style, next, ok := matchSpan(runes, i, ms)
		if !ok {
			plain = append(plain, runes[i])
			i++
			continue
		}
		flush()
		out.WriteString(renderSpans(inner, ms, base.Inherit(style)))
		i = next
	}
	flush()
	return out.String()
}

// matchSpan reports the inline span that starts at at, returning its content,
// its style and the index just past it.
func matchSpan(runes []rune, at int, ms markdownStyles) ([]rune, lipgloss.Style, int, bool) {
	switch {
	case runes[at] == '`':
		if end := indexRune(runes, '`', at+1); end > at+1 {
			return runes[at+1 : end], ms.code, end + 1, true
		}
	case hasSeq(runes, at, "**"):
		if end := indexSeq(runes, "**", at+2); end > at+1 {
			return runes[at+2 : end], ms.bold, end + 2, true
		}
	case hasSeq(runes, at, "__"):
		if end := indexSeq(runes, "__", at+2); end > at+1 {
			return runes[at+2 : end], ms.bold, end + 2, true
		}
	case hasSeq(runes, at, "~~"):
		if end := indexSeq(runes, "~~", at+2); end > at+1 {
			return runes[at+2 : end], ms.strike, end + 2, true
		}
	case runes[at] == '*' || runes[at] == '_':
		if end := emphasisEnd(runes, at); end > 0 {
			return runes[at+1 : end], ms.italic, end + 1, true
		}
	case runes[at] == '[':
		if label, next, ok := matchLink(runes, at); ok {
			return label, ms.link, next, true
		}
	}
	return nil, lipgloss.Style{}, at, false
}

// emphasisEnd returns the index of the closing delimiter of an emphasis span
// that opens at at, or -1 when the span never closes. It keeps underscores
// inside a word from opening emphasis, so snake_case names stay intact.
func emphasisEnd(runes []rune, at int) int {
	delim := runes[at]
	if at+1 >= len(runes) || runes[at+1] == ' ' || runes[at+1] == delim {
		return -1
	}
	if delim == '_' && at > 0 && isWordRune(runes[at-1]) {
		return -1
	}

	for i := at + 1; i < len(runes); i++ {
		if runes[i] != delim || runes[i-1] == ' ' {
			continue
		}
		if i+1 < len(runes) && runes[i+1] == delim {
			continue
		}
		if delim == '_' && i+1 < len(runes) && isWordRune(runes[i+1]) {
			continue
		}
		return i
	}
	return -1
}

// matchLink reports the label of a markdown link that starts at at, returning
// the label and the index just past the closing parenthesis. The target of the
// link is dropped, since a terminal cannot follow it.
func matchLink(runes []rune, at int) ([]rune, int, bool) {
	labelEnd := indexRune(runes, ']', at+1)
	if labelEnd < 0 || labelEnd+1 >= len(runes) || runes[labelEnd+1] != '(' {
		return nil, 0, false
	}
	end := indexRune(runes, ')', labelEnd+2)
	if end < 0 {
		return nil, 0, false
	}
	return runes[at+1 : labelEnd], end + 1, true
}

// isFence reports whether a line opens or closes a fenced code block.
func isFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	return runLen(trimmed, trimmed[0]) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~')
}

// runLen returns the length of the run of c that starts at the beginning of s.
func runLen(s string, c byte) int {
	count := 0
	for count < len(s) && s[count] == c {
		count++
	}
	return count
}

// heading returns the level and the title of an ATX heading line, or a zero
// level when the line is not one.
func heading(line string) (int, string) {
	trimmed := strings.TrimLeft(line, " ")
	level := runLen(trimmed, '#')
	if level == 0 || level > 6 {
		return 0, ""
	}
	rest := trimmed[level:]
	if rest != "" && !strings.HasPrefix(rest, " ") {
		return 0, ""
	}
	return level, strings.TrimSpace(rest)
}

// isThematicBreak reports whether a line is a horizontal rule.
func isThematicBreak(line string) bool {
	trimmed := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
	if len(trimmed) < 3 {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != '-' && trimmed[i] != '*' && trimmed[i] != '_' {
			return false
		}
	}
	return true
}

// blockquote returns the content of a blockquote line and whether the line is
// one.
func blockquote(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	return strings.TrimSpace(trimmed[1:]), true
}

// listItem returns the indentation, the marker and the content of a list item
// line, and whether the line is one.
func listItem(line string) (indent, marker, content string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	indent = line[:len(line)-len(trimmed)]

	if len(trimmed) >= 2 && strings.ContainsRune("-*+", rune(trimmed[0])) && trimmed[1] == ' ' {
		return indent, "• ", strings.TrimSpace(trimmed[2:]), true
	}

	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(trimmed) &&
		(trimmed[digits] == '.' || trimmed[digits] == ')') && trimmed[digits+1] == ' ' {
		return indent, trimmed[:digits+1] + " ", strings.TrimSpace(trimmed[digits+2:]), true
	}
	return "", "", "", false
}

// hasSeq reports whether the runes that begin at the given index start with
// the ASCII sequence.
func hasSeq(runes []rune, at int, seq string) bool {
	if at+len(seq) > len(runes) {
		return false
	}
	for i := 0; i < len(seq); i++ {
		if runes[at+i] != rune(seq[i]) {
			return false
		}
	}
	return true
}

// indexSeq returns the index of the first occurrence of an ASCII sequence at
// or after from, or -1 when it is absent.
func indexSeq(runes []rune, seq string, from int) int {
	for i := from; i+len(seq) <= len(runes); i++ {
		if hasSeq(runes, i, seq) {
			return i
		}
	}
	return -1
}

// indexRune returns the index of the first occurrence of target at or after
// from, or -1 when it is absent.
func indexRune(runes []rune, target rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// isWordRune reports whether r can be part of a word, which keeps underscores
// inside identifiers from opening emphasis.
func isWordRune(r rune) bool {
	switch {
	case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		return true
	default:
		return r > 0x7f
	}
}

// trimRight removes trailing spaces and tabs from a line.
func trimRight(line string) string {
	return strings.TrimRight(line, " \t")
}

// tableAlign selects how the content of a table column is aligned.
type tableAlign int

const (
	// alignLeft pads a cell to the right.
	alignLeft tableAlign = iota
	// alignCenter centers a cell.
	alignCenter
	// alignRight pads a cell to the left.
	alignRight
)

// table is a parsed markdown table.
type table struct {
	// header holds the raw cells of the header row.
	header []string

	// aligns holds the alignment of every column.
	aligns []tableAlign

	// rows holds the raw cells of the body rows.
	rows [][]string

	// columns is the number of columns of the table.
	columns int
}

// isTableRow reports whether a line can be part of a table. A table row always
// carries a pipe, which keeps a thematic break from being mistaken for one.
func isTableRow(line string) bool {
	return strings.Contains(line, "|")
}

// isTableSeparator reports whether a line is the delimiter row that turns the
// line above it into a table header.
func isTableSeparator(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	cells := splitTableRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if !isSeparatorCell(cell) {
			return false
		}
	}
	return true
}

// isSeparatorCell reports whether a delimiter cell is a run of dashes with
// optional alignment colons.
func isSeparatorCell(cell string) bool {
	cell = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(cell), ":"), ":")
	if cell == "" {
		return false
	}
	for i := 0; i < len(cell); i++ {
		if cell[i] != '-' {
			return false
		}
	}
	return true
}

// splitTableRow splits a table row into its trimmed cells, dropping the
// optional pipes that delimit the row.
func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	cells := strings.Split(trimmed, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// parseTable reads the table that starts at the given line, returning it and
// the index of its last line.
func parseTable(lines []string, start int) (table, int) {
	header := splitTableRow(lines[start])
	parsed := table{
		header:  header,
		aligns:  tableAligns(lines[start+1], len(header)),
		columns: len(header),
	}

	last := start + 1
	for i := start + 2; i < len(lines); i++ {
		row := trimRight(lines[i])
		if strings.TrimSpace(row) == "" || !isTableRow(row) {
			break
		}
		parsed.rows = append(parsed.rows, splitTableRow(row))
		last = i
	}
	return parsed, last
}

// tableAligns reads the alignment of every column from the delimiter row.
func tableAligns(separator string, columns int) []tableAlign {
	cells := splitTableRow(separator)
	aligns := make([]tableAlign, columns)
	for i := range aligns {
		aligns[i] = alignLeft
		if i >= len(cells) {
			continue
		}
		cell := strings.TrimSpace(cells[i])
		left := strings.HasPrefix(cell, ":")
		right := strings.HasSuffix(cell, ":")
		switch {
		case left && right:
			aligns[i] = alignCenter
		case right:
			aligns[i] = alignRight
		}
	}
	return aligns
}

// renderTable renders a markdown table as a bordered grid wrapped to width.
// When the table cannot fit, even after shrinking its columns, it falls back to
// a plain rendering that stays within the width.
func renderTable(parsed table, width int, ms markdownStyles) []string {
	if parsed.columns == 0 {
		return nil
	}

	header := tableCells(parsed.header, parsed.columns, ms, ms.tableHead)
	rows := make([][]string, 0, len(parsed.rows))
	for _, row := range parsed.rows {
		rows = append(rows, tableCells(row, parsed.columns, ms, lipgloss.NewStyle()))
	}

	natural := make([]int, parsed.columns)
	measure := func(cells []string) {
		for i, cell := range cells {
			natural[i] = max(natural[i], lipgloss.Width(cell))
		}
	}
	measure(header)
	for _, cells := range rows {
		measure(cells)
	}
	for i := range natural {
		natural[i] = max(natural[i], 1)
	}

	widths, ok := tableColumnWidths(natural, width)
	if !ok {
		return renderPlainTable(header, rows, width, ms)
	}

	out := make([]string, 0, len(rows)+3)
	out = append(out, tableBorderLine(widths, "┌", "┬", "┐", ms))
	out = append(out, tableContentLines(header, widths, parsed.aligns, ms)...)
	out = append(out, tableBorderLine(widths, "├", "┼", "┤", ms))
	for _, cells := range rows {
		out = append(out, tableContentLines(cells, widths, parsed.aligns, ms)...)
	}
	out = append(out, tableBorderLine(widths, "└", "┴", "┘", ms))
	return out
}

// tableCells renders the inline spans of a row, padding the row to the number
// of columns of the table.
func tableCells(row []string, columns int, ms markdownStyles, base lipgloss.Style) []string {
	cells := make([]string, columns)
	for i := range cells {
		if i < len(row) {
			cells[i] = renderInline(row[i], ms, base)
		}
	}
	return cells
}

// tableColumnWidths fits the natural width of every column into the width of
// the table, shrinking the wider ones when the sum overflows. It reports
// whether the table fits at all.
func tableColumnWidths(natural []int, width int) ([]int, bool) {
	const minColumn = 3

	columns := len(natural)
	budget := width - (3*columns + 1)
	if budget < minColumn*columns {
		return nil, false
	}

	total := 0
	for _, column := range natural {
		total += column
	}
	if total <= budget {
		return natural, true
	}

	extra := 0
	for _, column := range natural {
		extra += max(0, column-minColumn)
	}

	widths := make([]int, columns)
	used := 0
	for i, column := range natural {
		widths[i] = minColumn
		if extra > 0 {
			widths[i] += max(0, column-minColumn) * (budget - minColumn*columns) / extra
		}
		used += widths[i]
	}

	for leftover := budget - used; leftover > 0; {
		grew := false
		for i := range widths {
			if leftover == 0 {
				break
			}
			if natural[i] > widths[i] {
				widths[i]++
				leftover--
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	return widths, true
}

// tableBorderLine renders a horizontal border of the grid.
func tableBorderLine(widths []int, left, mid, right string, ms markdownStyles) string {
	var line strings.Builder
	line.WriteString(left)
	for i, column := range widths {
		line.WriteString(strings.Repeat("─", column+2))
		if i < len(widths)-1 {
			line.WriteString(mid)
		} else {
			line.WriteString(right)
		}
	}
	return ms.tableBorder.Render(line.String())
}

// tableContentLines renders one row of the grid, wrapping every cell to its
// column width and aligning it. The row grows as tall as its tallest cell.
func tableContentLines(
	cells []string,
	widths []int,
	aligns []tableAlign,
	ms markdownStyles,
) []string {
	wrapped := make([][]string, len(cells))
	height := 1
	for i, cell := range cells {
		wrapped[i] = markdownWrap(cell, widths[i])
		if len(wrapped[i]) == 0 {
			wrapped[i] = []string{""}
		}
		height = max(height, len(wrapped[i]))
	}

	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		var line strings.Builder
		line.WriteString(ms.tableBorder.Render("│"))
		for i := range cells {
			content := ""
			if row < len(wrapped[i]) {
				content = wrapped[i][row]
			}
			line.WriteString(" ")
			line.WriteString(padCell(content, widths[i], aligns[i]))
			line.WriteString(" ")
			line.WriteString(ms.tableBorder.Render("│"))
		}
		lines = append(lines, line.String())
	}
	return lines
}

// padCell pads a rendered cell to the given width following its alignment. It
// measures the cell without its styling, which the terminal does not print.
func padCell(content string, width int, align tableAlign) string {
	gap := width - lipgloss.Width(content)
	if gap <= 0 {
		return content
	}

	switch align {
	case alignRight:
		return strings.Repeat(" ", gap) + content
	case alignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + content + strings.Repeat(" ", gap-left)
	default:
		return content + strings.Repeat(" ", gap)
	}
}

// renderPlainTable renders a table that cannot fit as a bordered grid: its rows
// joined by pipes and wrapped to the width.
func renderPlainTable(header []string, rows [][]string, width int, ms markdownStyles) []string {
	join := func(cells []string) string {
		return strings.Join(cells, ms.tableBorder.Render(" | "))
	}

	out := markdownWrap(join(header), width)
	out = append(out, ms.tableBorder.Render(strings.Repeat("─", max(1, min(width, 8)))))
	for _, cells := range rows {
		out = append(out, markdownWrap(join(cells), width)...)
	}
	return out
}
