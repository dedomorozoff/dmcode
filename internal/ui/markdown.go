package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Markdown rendering for the agent's replies.
//
// The reply used to go through the same plain-text wrapper as everything else,
// which re-wrapped on spaces and left the markup in place: the user saw literal
// ** around bold words and lost the indentation of every code block. The
// renderer below turns the markup into styled rows before wrapping, and hands
// code blocks to a verbatim path so their own line breaks survive.
//
// Only the subset models actually emit is handled — headings, emphasis, inline
// code, lists, fenced code, rules. Anything unrecognised passes through as
// prose, so an unusual reply degrades to plain text instead of losing content.

var (
	styleH1      = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	styleH2      = lipgloss.NewStyle().Bold(true).Foreground(cAgent)
	styleH3      = lipgloss.NewStyle().Bold(true)
	styleCodeBlk = lipgloss.NewStyle().Foreground(cAgent)
	styleBullet  = lipgloss.NewStyle().Foreground(cDim)
	styleRule    = lipgloss.NewStyle().Foreground(cBorder)
	styleItalic  = lipgloss.NewStyle().Italic(true)

	// cEmph carries the emphasis in colour as well as weight. Weight alone is not
	// enough: many terminals ship a font with no separate bold face and do not
	// synthesise one, so a bold span in the same colour as its surroundings comes
	// out looking exactly like the rest of the line. A second, louder channel
	// makes the emphasis visible on those terminals too.
	cEmph = lipgloss.Color("14")
)

// mdIndent is the gutter a code block keeps so it reads as a block rather than
// as more prose.
const mdIndent = "  "

// renderCalls counts markdown renderings, so a reply that comes out as raw
// markup can be traced to "was the renderer reached at all, and did it run this
// many times" — the question that decides between a parsing bug and a routing
// bug. It is read only by /debug; the increment is a plain int and costs
// nothing measurable next to the rendering it sits in front of.
var renderCalls int

// renderMarkdown lays out one markdown document as rows no wider than width.
//
// base is the style of ordinary prose; emphasis and code override it locally.
// The rows come back already styled, so the caller must not re-render them with
// row.style — that would nest the escape sequences and the width accounting
// would stop matching what the terminal draws.
func renderMarkdown(text string, width int, base lipgloss.Style) []string {
	renderCalls++
	if width < 1 {
		width = 1
	}
	// Models emit CRLF often enough that leaving it in would leave a stray \r at
	// the end of every row, which the terminal draws as a stray overwrite.
	text = strings.ReplaceAll(text, "\r\n", "\n")

	var out []string
	inCode := false

	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)

		// A fence toggles the code block. The closing fence is matched on
		// indentation too, because a block nested in a list item keeps its
		// fence indented.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			continue
		}
		if inCode {
			out = append(out, wrapCodeLine(raw, width)...)
			continue
		}
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		// Horizontal rule: three or more of -, * or _ on their own.
		if isRule(trimmed) {
			out = append(out, styleRule.Render(strings.Repeat("─", width)))
			out = append(out, "")
			continue
		}
		// Tables are collected first: they span several lines, so each row has to
		// be pulled out with the rows around it rather than rendered in place.
		if isTableDelimiter(trimmed) || (strings.HasPrefix(trimmed, "|") && tableHasHeaderNext(lines, i)) {
			tbl, consumed := renderTable(lines[i:], width)
			out = append(out, tbl...)
			i += consumed
			continue
		}
		// Blockquote: the ">" marker is replaced by a vertical bar so the quoted
		// line reads as an aside rather than as text that failed to render.
		if body, ok := blockquote(trimmed); ok {
			for j, r := range wrapInline(body, width-2, styleItalic) {
				if j == 0 {
					out = append(out, styleRule.Render("▎")+" "+r)
				} else {
					out = append(out, "  "+r)
				}
			}
			continue
		}
		// Headings: the marker is dropped and the level drives the style.
		if lvl, body, ok := heading(trimmed); ok {
			st := styleH3
			switch lvl {
			case 1:
				st = styleH1
			case 2:
				st = styleH2
			}
			out = append(out, wrapInline(body, width, st)...)
			out = append(out, "")
			continue
		}
		// Bulleted list. The marker becomes a bullet and the body is wrapped to
		// the width left by the marker, so the text still lines up after a wrap
		// instead of overflowing the panel by the width of the gutter.
		if body, ok := listItem(trimmed); ok {
			const gutter = 2 // "• "
			avail := width - gutter
			if avail < 8 {
				avail = width
			}
			for i, r := range wrapInline(body, avail, base) {
				if i == 0 {
					out = append(out, styleBullet.Render("•")+" "+r)
				} else {
					out = append(out, strings.Repeat(" ", gutter)+r)
				}
			}
			continue
		}
		out = append(out, wrapInline(raw, width, base)...)
	}

	// A trailing blank from the last block would pad the transcript with a row
	// the user cannot tell apart from a deliberate gap.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// wrapCodeLine keeps a code line as it is. An over-long line is hard-cut rather
// than wrapped: a wrapped code line reads as a second, different line of code.
func wrapCodeLine(raw string, width int) []string {
	prefix := mdIndent
	avail := width - ansi.StringWidth(prefix)
	if avail < 4 {
		avail, prefix = width, ""
	}
	content := strings.TrimRight(raw, " \t")
	if ansi.StringWidth(content) == 0 {
		return []string{""}
	}
	var out []string
	for _, chunk := range cutToWidth(content, avail) {
		out = append(out, prefix+styleCodeBlk.Render(chunk))
	}
	return out
}

// cutToWidth splits a line into width-cell pieces without regard for spaces.
func cutToWidth(s string, width int) []string {
	if width < 1 || ansi.StringWidth(s) <= width {
		return []string{s}
	}
	var out []string
	rest := s
	for ansi.StringWidth(rest) > width {
		head, tail := cutCells(rest, width)
		out = append(out, head)
		rest = tail
	}
	if rest != "" {
		out = append(out, rest)
	}
	return out
}

// wrapInline renders emphasis and inline code in one line of prose, then wraps
// the result. wrapCells is escape-aware, so the wrap respects the styling
// instead of cutting through it.
func wrapInline(s string, width int, base lipgloss.Style) []string {
	rows := wrapCells(mdInline(s, base), width)
	if len(rows) == 0 {
		return []string{""}
	}
	return rows
}

// mdInline walks one line and applies the inline styles. It is a small scanner
// rather than a full parser because the cases are few and the failure mode is
// benign: an unclosed marker is left as literal text.
//
// Every style is *derived* from base rather than applied around an already
// rendered string. Wrapping a rendered span in another style nests two escape
// sequences, and the inner reset then cancels the outer weight — which is what
// made **bold** come out looking like the plain text around it.
func mdInline(s string, base lipgloss.Style) string {
	var b strings.Builder
	// pending accumulates the plain run so styling is applied to exactly the
	// marked span instead of one style call per character.
	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			b.WriteString(base.Render(plain.String()))
			plain.Reset()
		}
	}

	for i := 0; i < len(s); {
		switch {
		case s[i] == '`':
			// Inline code: the closing backtick must be on the same line.
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				flush()
				code := s[i+1 : i+1+j]
				b.WriteString(base.Foreground(cTool).Render("`" + code + "`"))
				i += j + 2
				continue
			}
			plain.WriteByte(s[i])
			i++

		case strings.HasPrefix(s[i:], "**"), strings.HasPrefix(s[i:], "__"):
			mark := s[i : i+2]
			if j := strings.Index(s[i+2:], mark); j >= 0 {
				flush()
				inner := mdInline(s[i+2:i+2+j], base.Bold(true).Foreground(cEmph))
				b.WriteString(inner)
				i += j + 4
				continue
			}
			plain.WriteString(mark)
			i += 2

		case s[i] == '*' || s[i] == '_':
			mark := s[i]
			// A lone marker only opens italics when a second one follows on the
			// same line; a list bullet reaching here would not close, so it stays
			// literal.
			if j := strings.IndexByte(s[i+1:], mark); j > 0 {
				flush()
				// Derived, not wrapped — see the note on mdInline.
				b.WriteString(mdInline(s[i+1:i+1+j], base.Italic(true)))
				i += j + 2
				continue
			}
			plain.WriteByte(mark)
			i++

		default:
			plain.WriteByte(s[i])
			i++
		}
	}
	flush()
	return b.String()
}

// heading reports the level of an ATX heading and the text after it.
func heading(s string) (int, string, bool) {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(s) || s[n] != ' ' {
		return 0, "", false
	}
	// A trailing run of # is decoration in CommonMark and is dropped.
	body := strings.TrimSpace(s[n:])
	return n, strings.TrimSpace(strings.TrimRight(body, "#")), true
}

// listItem reports the body of a bulleted or numbered list item. Leading
// indentation is carried into the body so a sub-item still reads as nested
// after the marker is replaced.
func listItem(s string) (string, bool) {
	trimmedLeft := strings.TrimLeft(s, " \t")
	indent := s[:len(s)-len(trimmedLeft)]
	for _, mark := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmedLeft, mark) {
			return indent + "  " + strings.TrimSpace(trimmedLeft[len(mark):]), true
		}
	}
	// Numbered: "1. " or "1) ", with the marker kept so the numbering shows.
	i := 0
	for i < len(trimmedLeft) && trimmedLeft[i] >= '0' && trimmedLeft[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(trimmedLeft) {
		if (trimmedLeft[i] == '.' || trimmedLeft[i] == ')') && trimmedLeft[i+1] == ' ' {
			return indent + "  " + trimmedLeft[:i+1] + " " + strings.TrimSpace(trimmedLeft[i+2:]), true
		}
	}
	return "", false
}

// isRule reports whether the line is a thematic break.
func isRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c && s[i] != ' ' {
			return false
		}
	}
	return true
}

// blockquote reports the body of a ">" line.
func blockquote(s string) (string, bool) {
	if !strings.HasPrefix(s, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(s, "> ")), true
}

// splitRow breaks a pipe-delimited table row into its cells. The optional outer
// pipes are dropped, so "| a | b |" and "a | b" give the same cells. A trailing
// pipe produces no empty trailing cell — "| a | b |" is two columns, not three.
func splitRow(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	// A row that ended up empty on the right only added a phantom column.
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// isTableDelimiter reports whether the line is the |---|---| separator that
// marks the previous line as a table header.
func isTableDelimiter(s string) bool {
	if !strings.Contains(s, "-") || !strings.HasPrefix(s, "|") {
		return false
	}
	for _, c := range s {
		if c != '|' && c != '-' && c != ':' && c != ' ' {
			return false
		}
	}
	return strings.Count(s, "-") >= 3
}

// tableHasHeaderNext reports whether lines[i] is a header followed by a
// delimiter, which is what distinguishes a table from prose that happens to
// contain a pipe.
func tableHasHeaderNext(lines []string, i int) bool {
	return i+1 < len(lines) && isTableDelimiter(strings.TrimSpace(lines[i+1]))
}

// renderTable lays out a pipe table as aligned columns and reports how many
// input lines it consumed.
//
// Alignment is what makes a table readable: the raw pipes force the reader to
// line up columns by eye, which is exactly the work the markup was asking to be
// done for them. A table that does not fit the panel falls back to the raw rows
// rather than being cut into nonsense.
func renderTable(lines []string, width int) ([]string, int) {
	var rows [][]string
	consumed := 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			break
		}
		if !strings.Contains(t, "|") {
			break
		}
		if isTableDelimiter(t) {
			// The delimiter carries alignment, not content.
			consumed++
			continue
		}
		rows = append(rows, splitRow(t))
		consumed++
	}
	if len(rows) == 0 {
		return nil, 0
	}

	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	// The header row defines the table. A row with more cells than the header is
	// malformed, and letting it widen the table would draw columns with no
	// heading over them, so the extra cells are dropped instead.
	if len(rows) > 0 && len(rows[0]) > 0 {
		head := len(rows[0])
		for i, r := range rows {
			if len(r) > head {
				rows[i] = r[:head]
			}
		}
		cols = head
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for c, cell := range r {
			if w := ansi.StringWidth(cell); w > widths[c] {
				widths[c] = w
			}
		}
	}

	// The borders cost a cell of padding on each side of every column plus the
	// separators, so the cells have to fit in what is left or the table would
	// overflow the panel and break the frame.
	overhead := 3*cols - 1
	if width > 0 && totalCells(widths)+overhead > width {
		// Too wide: keep the rows but drop the framing, which buys back most of
		// the overhead, and let the longest column absorb the rest.
		return renderTablePlain(rows, width), consumed
	}

	var out []string
	line := func(sep string) string {
		var b strings.Builder
		b.WriteString(sep)
		for c := 0; c < cols; c++ {
			b.WriteString(pad(strings.Repeat("─", widths[c]+2), widths[c]+2))
			if c < cols-1 {
				b.WriteString(sep)
			}
		}
		b.WriteString(sep)
		return styleRule.Render(b.String())
	}
	for ri, r := range rows {
		var b strings.Builder
		b.WriteString(styleRule.Render("│"))
		for c := 0; c < cols; c++ {
			cell := ""
			if c < len(r) {
				cell = mdInline(r[c], styleAgent)
			}
			b.WriteString(" " + pad(cell, widths[c]) + " ")
			if c < cols-1 {
				b.WriteString(styleRule.Render("│"))
			}
		}
		b.WriteString(styleRule.Render("│"))
		out = append(out, b.String())
		if ri == 0 {
			// Two separate rows, not one row with a newline inside it: a
			// multi-line string breaks every row count and width check
			// downstream, and the viewport would clip it.
			out = append(out, line("├"), line("┴"))
		}
	}
	return out, consumed
}

// renderTablePlain lays a table out without borders, for a panel too narrow to
// carry them. The columns are still padded against each other so the shape of
// the table survives.
func renderTablePlain(rows [][]string, width int) []string {
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for c, cell := range r {
			if w := ansi.StringWidth(cell); w > widths[c] {
				widths[c] = w
			}
		}
	}

	// The room available is the panel minus one space between each pair of
	// columns. The overflow is taken from the widest column first, because that
	// is the one that decides whether a row fits; only when a single column is
	// still too wide is the surplus shared out, so a too-wide table degrades
	// into even columns rather than one truncated and six full-width ones.
	avail := width - (cols - 1)
	if avail < cols {
		avail = cols
	}
	// The loop takes one cell off the widest column per pass. It stops at one
	// cell per column, which is the floor: a column narrower than a single
	// character cannot hold anything, so a table that still does not fit is
	// simply too many columns for the panel and has to wrap vertically instead.
	for totalCells(widths) > avail {
		widest, wi := 0, -1
		for c, w := range widths {
			if w > widest {
				widest, wi = w, c
			}
		}
		if wi < 0 || widths[wi] <= 1 {
			break
		}
		widths[wi]--
	}

	var out []string
	for _, r := range rows {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			cell := ""
			if c < len(r) {
				cell = strings.Join(cutToWidth(mdInline(r[c], styleAgent), widths[c]), "")
			}
			b.WriteString(pad(cell, widths[c]))
			if c < cols-1 {
				b.WriteString(" ")
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func totalCells(widths []int) int {
	n := 0
	for _, w := range widths {
		n += w
	}
	return n
}

// pad fits a styled string into width cells: it pads on the right when the
// string is short, and cuts it when it is long. Cutting is not optional here —
// pad is what keeps a shrunk table inside the panel, and a cell that ignored its
// column width would push the row back out again.
func pad(s string, width int) string {
	d := width - ansi.StringWidth(s)
	if d > 0 {
		return s + strings.Repeat(" ", d)
	}
	if d == 0 {
		return s
	}
	// Hard-cut, measured in cells so the escapes are never split.
	cut := []rune(s)
	for n := 0; n < len(cut); n++ {
		if ansi.StringWidth(string(cut[:n+1])) > width {
			return string(cut[:n])
		}
	}
	return s
}
