package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/editor/syntax"
)

// cachedLine holds the rendered rows of one history line. It is index-aligned
// with m.history: a streamed token rewrites the text of the last agent line,
// so on the next frame every earlier line still matches its cache entry and is
// reused as-is, and only the line that changed is re-rendered and re-wrapped.
//
// The refs travel inside the rendered rows rather than beside them, which is
// the whole reason they are in the cache at all: a cache that kept the styled
// strings and dropped the places they stand for would empty the click index on
// exactly the frames that reuse the cache, so the jump would work once and then
// stop working on every message that scrolled past.
type cachedLine struct {
	kind  lineKind
	text  string
	width int
	rows  renderedRows
}

// transcriptRow describes how one history entry is laid out in the chat panel:
// a gutter in front of the first row and a matching hanging indent on the rows
// that follow, so wrapped text stays aligned under its own marker instead of
// jumping back to column zero.
type transcriptRow struct {
	style lipgloss.Style
	first string
	rest  string
	// verbatim marks pre-formatted art (the logo, an image preview), which keeps
	// its own line breaks: re-wrapping box-drawing runes would scramble the
	// picture.
	verbatim bool
	// markdown marks text the model produced, which is rendered as markdown
	// rather than as literal prose.
	markdown bool
	// prestyled marks art that carries its own colour. The transcript wraps every
	// other row in row.style, which would put the panel's foreground in front of
	// the picture's own and reset it at the end of the row — a preview of a
	// screenshot comes out tinted and half its colours replaced by the default.
	prestyled bool
	// diff marks a change block (kindDiff): its rows are coloured per line by
	// diffRows before wrapping, so they ride through unstyled, but the block
	// keeps the transcript's left margin like the tool result it belongs to.
	diff bool
	// syn is the highlighter the diff block colours its code rows with. The
	// row style fills it in; nil means the plain, unhighlighted path.
	syn *syntax.Highlighter
	// captionLast marks a block whose final row is a caption rather than part of
	// the picture, so a block too wide to draw can fall back to that row instead of
	// disappearing. The logo has no such row and so keeps being dropped whole.
	captionLast bool
}

func (m *uiModel) rowStyle(kind lineKind) transcriptRow {
	switch kind {
	case kindUser:
		return transcriptRow{style: styleUser, first: "› ", rest: "  "}
	case kindTool:
		return transcriptRow{style: styleTool, first: "⏺ ", rest: "  "}
	case kindToolRes:
		return transcriptRow{style: styleToolRes, first: "  ⎟ ", rest: "    "}
	case kindDiff:
		// The diff colours its own rows: the styles are applied per row by
		// diffRows in rowRows, so the block rides through as prestyled — but it
		// is not verbatim, it keeps the transcript's left margin like the
		// tool result it belongs to.
		return transcriptRow{style: lipgloss.NewStyle(), first: "    ", rest: "    ", prestyled: true, diff: true, syn: m.diffHighlighter()}
	case kindAgent:
		return transcriptRow{style: styleAgent, markdown: true}
	case kindSys:
		return transcriptRow{style: styleSys}
	case kindErr:
		return transcriptRow{style: styleErr}
	case kindLogo:
		return transcriptRow{style: styleLogo, verbatim: true}
	case kindImage:
		// An empty style on purpose, paired with prestyled: the preview brings its
		// own colours, so there is nothing to add and something to break.
		return transcriptRow{
			style:       lipgloss.NewStyle(),
			verbatim:    true,
			prestyled:   true,
			captionLast: true,
		}
	}
	return transcriptRow{style: styleAgent}
}

// codeRef is a place in a file that one transcript row stands for. It is what a
// click on a change block resolves to, and it exists as a value of its own
// because the alternative is gone by the time the click arrives: a diff row has
// been styled, gutter-numbered, maybe wrapped onto a second screen row and
// maybe ANSI-truncated, and the line it names survives only as digits inside
// cells the renderer owns. Recovering it from the drawn row would mean
// re-deriving the gutter's arithmetic and guessing which blank gutter was a
// wrapped continuation — a different question than the one being asked.
type codeRef struct {
	// path is the file as the block's header spelled it: the path the model
	// passed to the tool, so usually relative to the workspace root.
	path string
	// line is 1-based, as the gutter prints it. Zero means the row names no
	// position at all: a block's header, the tail of a capped block, or any
	// history line that is not a change.
	line int
}

// ok reports whether the ref names a place a jump could actually go.
func (r codeRef) ok() bool { return r.path != "" && r.line > 0 }

// String is the file:line a status line shows, and the empty string for a ref
// that names nothing.
func (r codeRef) String() string {
	if !r.ok() {
		return ""
	}
	return r.path + ":" + strconv.Itoa(r.line)
}

// renderedRows is one history entry laid out: the rows as they go into the
// viewport, and index-aligned with them, the place in a file each row stands
// for. The two are produced together because they are decided together — a diff
// row that wrapped onto a second screen row is still the same line of the same
// file, and only the row that knows so can say it.
type renderedRows struct {
	rows []string
	refs []codeRef
}

// at is the place row i stands for, or the zero value for a row that names
// none — which is every row of every history line but a change block's.
func (rr renderedRows) at(i int) codeRef {
	if i < 0 || i >= len(rr.refs) {
		return codeRef{}
	}
	return rr.refs[i]
}

func (rr *renderedRows) add(row string, ref codeRef) {
	rr.rows = append(rr.rows, row)
	rr.refs = append(rr.refs, ref)
}

func (rr *renderedRows) addAll(other renderedRows) {
	rr.rows = append(rr.rows, other.rows...)
	rr.refs = append(rr.refs, other.refs...)
}

// rowRows turns one history entry into the transcript rows it occupies. Prose is
// wrapped to width under a hanging indent. Pre-formatted art keeps its own line
// breaks and is dropped whole when it cannot fit: a banner sliced to the panel
// edge, or broken across rows, reads as corruption rather than as a logo. The
// header names the app regardless, so losing the art costs nothing.
func rowRows(text string, width int, row transcriptRow) renderedRows {
	// Markdown is only applied where the model produced it. Tool output and the
	// user's own prompts are literal text — a path or a JSON payload that happens
	// to contain "**" must survive untouched.
	if row.markdown {
		rows := renderMarkdown(text, width, row.style)
		for i, r := range rows {
			rows[i] = row.first + r
		}
		// A citation in the reply is a place the user can click. The rows are
		// read as they are drawn, so a place the markdown renderer rearranged
		// is still found — see citeRows for what that costs.
		return renderedRows{rows: rows, refs: citeRows(rows)}
	}
	if row.diff {
		return diffRows(text, width, row)
	}
	if !row.verbatim {
		return renderedRows{rows: wrapIndent(text, width, row.first, row.rest)}
	}
	rows := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for _, r := range rows {
		if ansi.StringWidth(r) > width {
			// An image preview whose last row is its caption falls back to the
			// caption: the picture is too wide to draw honestly, but "a picture was
			// sent, this one" is still worth saying. Dropping the whole block
			// would leave a turn sent as nothing but a screenshot with nothing at
			// all on screen to say so.
			if row.captionLast && len(rows) > 0 {
				last := rows[len(rows)-1]
				if ansi.StringWidth(last) <= width {
					return renderedRows{rows: []string{last}}
				}
			}
			return renderedRows{}
		}
	}
	return renderedRows{rows: rows}
}

// diffRow is one line of a change block as the renderer sees it: the marker,
// the line's position in the file (0 when the row carries none — the tail of
// a capped block), and the code itself. The wire format is "-12: code" /
// "+12: code"; the colon keeps a code line that begins with digits from
// being read as a number.
type diffRow struct {
	mark string
	num  int
	code string
}

// parseDiffRow reads one DiffBlock line. A row without the number shape
// (the tail, or a marker-only row) keeps its whole tail as code.
func parseDiffRow(line string) diffRow {
	if line == "" {
		return diffRow{}
	}
	mark, rest := "", line
	if line[0] == '+' || line[0] == '-' {
		mark, rest = line[:1], line[1:]
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i > 0 && i < len(rest) && rest[i] == ':' {
		if n, err := strconv.Atoi(rest[:i]); err == nil {
			return diffRow{mark: mark, num: n, code: strings.TrimPrefix(rest[i+1:], " ")}
		}
	}
	return diffRow{mark: mark, code: rest}
}

// diffRows renders a change block as a piece of the editor: a dim line
// number in the gutter, then the code — highlighted with the editor's theme
// when the language is one it knows, the +/- rows carrying a dim green or
// red background over the theme's own foreground, the way a git pager does.
// Numbers come from the block itself: old-file positions for removals,
// new-file positions for additions. Rows the highlighter does not cover are
// wrapped like ordinary text; highlighted rows are ANSI-truncated instead,
// because wrapping measured plain text cannot survive the escapes the theme
// paints with.
//
// Every row it returns also carries the place it names, so a click on one can
// open the file there. A removal's number is the old file's, which is the one
// row that no longer exists to land on: the block trims a shared prefix and
// suffix off both texts and numbers both sides from the same place, so the line
// a removal used to be on is the line the replacement is on, and a pure
// deletion lands on the line that took its place.
func diffRows(text string, width int, row transcriptRow) renderedRows {
	avail := width - max(ansi.StringWidth(row.first), ansi.StringWidth(row.rest))
	if avail < 1 {
		avail = 1
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	path := ""
	if len(lines) > 0 && strings.HasPrefix(lines[0], "── ") {
		path = strings.TrimPrefix(lines[0], "── ")
		lines = lines[1:]
	}

	parsed := make([]diffRow, len(lines))
	numw := 0
	for i, l := range lines {
		parsed[i] = parseDiffRow(l)
		if p := len(strconv.Itoa(parsed[i].num)); parsed[i].num > 0 && p > numw {
			numw = p
		}
	}
	if numw < 3 {
		numw = 3
	}

	var out renderedRows
	// The header wraps like plain text: it is not code, and the file name is
	// the one thing in the block that must survive any width. It names the file
	// rather than a line in it, so it stands for nothing.
	for j, r := range wrapCells("── "+path, avail) {
		indent := row.rest
		if j == 0 {
			indent = row.first
		}
		out.add(styleHint.Render(indent+r), codeRef{})
	}

	for g := 0; g < len(parsed); {
		mark := parsed[g].mark
		j := g
		for j < len(parsed) && parsed[j].mark == mark {
			j++
		}
		out.addAll(diffGroup(mark, parsed[g:j], path, avail, numw, row))
		g = j
	}
	return out
}

// diffGroup renders one run of same-marker rows. Consecutive rows are
// highlighted as one text so a string literal spanning lines keeps its
// colour across the block.
func diffGroup(mark string, rows []diffRow, path string, avail, numw int, row transcriptRow) renderedRows {
	st, bg := styleHint, lipgloss.Color("")
	switch mark {
	case "+":
		st, bg = styleAdd, cDiffAddBg
	case "-":
		st, bg = styleDel, cDiffDelBg
	}

	// The gutter: the line's position, right-aligned, dim — a number the
	// eye can anchor on without competing with the code beside it.
	gutter := func(n int, dim bool) string {
		s := fmt.Sprintf("%*d ", numw, n)
		if n <= 0 {
			s = strings.Repeat(" ", numw+1)
		}
		if dim {
			s = styleHint.Render(s)
		}
		return s
	}

	// A row with no number — the tail of a capped block — names no line, and a
	// wrapped continuation row repeats its own row's number because it is the
	// same line of the same file, drawn across two rows of the screen.
	ref := func(n int) codeRef {
		if n <= 0 {
			return codeRef{}
		}
		return codeRef{path: path, line: n}
	}

	var out renderedRows
	if row.syn == nil || syntax.Lang(path) == "" {
		// No highlighter, or a language chroma does not know: the plain
		// path — wrapped like ordinary text, code budget shrunk by the
		// gutter the numbers now take.
		codeAvail := avail - (numw + 1)
		if codeAvail < 1 {
			codeAvail = 1
		}
		for _, p := range rows {
			code := p.code
			if p.mark != "" {
				code = p.mark + " " + p.code
			}
			for j, r := range wrapCells(code, codeAvail) {
				indent := row.rest
				gut := gutter(p.num, true)
				if j > 0 {
					indent, gut = row.rest, styleHint.Render(strings.Repeat(" ", numw+1))
				}
				if j == 0 {
					indent = row.first
				}
				out.add(st.Render(indent)+gut+st.Render(r), ref(p.num))
			}
		}
		return out
	}

	code := make([]string, len(rows))
	for i, p := range rows {
		code[i] = p.code
	}
	styled := row.syn.HighlightCode(path, strings.Join(code, "\n"), func(base lipgloss.Style) lipgloss.Style {
		return base.Background(bg)
	})
	for i, p := range rows {
		line := ""
		if i < len(styled) {
			line = ansi.Truncate(styled[i], avail-(numw+1)-2, "…")
		}
		out.add(row.first+gutter(p.num, true)+st.Render(p.mark+" ")+line, ref(p.num))
	}
	return out
}

// chatIndent is the small left margin every transcript row is drawn with: a
// line of text sitting flush against the panel's border reads as cramped, and
// a couple of border columns of air between them is what a page margin is
// for. The wrap width shrinks by the same amount, so nothing gains a row.
const chatIndent = 2

func (m *uiModel) renderHistory() string {
	w := m.contentWidth() - chatIndent
	if !m.historyDirty && m.cachedWidth == w && m.cachedHistory != "" {
		return m.cachedHistory
	}
	// A replacement in applyAgentText can shrink the slice; the cache must not
	// keep entries for lines that no longer exist.
	if len(m.cachedRows) > len(m.history) {
		m.cachedRows = m.cachedRows[:len(m.history)]
	}
	// The click index is rebuilt here and only here, which is why it is built at
	// all: it is the one place that knows how many rows a history entry turned
	// into and what each of them stands for. The early return above keeps the
	// previous index, which is exactly right — the content it was built for is
	// the content the viewport is still showing.
	m.rowRefs = m.rowRefs[:0]
	var b strings.Builder
	for i := range m.history {
		l := &m.history[i]
		row := m.rowStyle(l.kind)
		var rr renderedRows
		hit := false
		if i < len(m.cachedRows) {
			c := &m.cachedRows[i]
			if c.kind == l.kind && c.text == l.text && c.width == w {
				rr, hit = c.rows, true
			}
		}
		if !hit {
			rr = rowRows(l.text, w, row)
			for i, r := range rr.rows {
				// A markdown row arrives already styled by the renderer, and a
				// prestyled one already carries its own colours; wrapping either
				// again would nest the escapes and break the width accounting.
				if row.markdown || row.prestyled {
					continue
				}
				rr.rows[i] = row.style.Render(r)
			}
			entry := cachedLine{kind: l.kind, text: l.text, width: w, rows: rr}
			if i < len(m.cachedRows) {
				m.cachedRows[i] = entry
			} else if i == len(m.cachedRows) {
				m.cachedRows = append(m.cachedRows, entry)
			} else {
				for len(m.cachedRows) < i {
					m.cachedRows = append(m.cachedRows, cachedLine{})
				}
				m.cachedRows = append(m.cachedRows, entry)
			}
		}
		// An image preview brings its own left margin, painted in the picture's
		// background: adding chatIndent on top of it would put a stripe of the
		// terminal's own background down the side of the picture, which reads as a
		// column of the image that is out of step with the rest.
		margin := strings.Repeat(" ", chatIndent)
		for j, r := range rr.rows {
			// Only full-bleed art (the image preview) skips the margin; a diff
			// block carries its own colours but belongs to the transcript's page.
			if row.prestyled && row.verbatim {
				b.WriteString(r + "\n")
			} else {
				b.WriteString(margin + r + "\n")
			}
			m.rowRefs = append(m.rowRefs, rr.at(j))
		}
	}
	m.cachedHistory = b.String()
	m.cachedWidth = w
	m.historyDirty = false
	return m.cachedHistory
}

// wrapIndent breaks text into rows no wider than width cells, prefixing the
// very first row with first and every later row with rest.
func wrapIndent(text string, width int, first, rest string) []string {
	if width < 1 {
		width = 1
	}
	// Rows are wrapped against the wider of the two indents, so neither the
	// gutter row nor a continuation row can spill past the frame.
	avail := width - max(ansi.StringWidth(first), ansi.StringWidth(rest))
	if avail < 1 {
		avail = 1
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		for _, row := range wrapCells(para, avail) {
			indent := rest
			if len(out) == 0 {
				indent = first
			}
			out = append(out, indent+row)
		}
	}
	return out
}

// wrapCells breaks one logical line into rows of at most width terminal cells.
// It prefers word boundaries and only splits inside a word when that word
// cannot fit on a row of its own. Widths are counted in cells rather than bytes
// so that Cyrillic, CJK and emoji all measure the way the terminal draws them.
func wrapCells(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	s = strings.TrimRight(s, " \t")
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	var rows []string
	cur, curW := "", 0
	flush := func() {
		rows = append(rows, cur)
		cur, curW = "", 0
	}
	for _, word := range strings.Split(s, " ") {
		wordW := ansi.StringWidth(word)
		if wordW == 0 {
			// A run of spaces is kept, leading ones included: they are often the
			// indent of a pre-formatted line, and the caller adds its own
			// gutter on top of the text rather than inside it.
			if curW < width {
				cur += " "
				curW++
			}
			continue
		}
		switch {
		case curW == 0 && wordW <= width:
			cur, curW = word, wordW
		case curW > 0 && curW+1+wordW <= width:
			cur += " " + word
			curW += 1 + wordW
		default:
			if curW > 0 {
				flush()
			}
			for wordW > width {
				head, rest := cutCells(word, width)
				rows = append(rows, head)
				word, wordW = rest, ansi.StringWidth(rest)
			}
			cur, curW = word, wordW
		}
	}
	flush()
	return rows
}

// cutCells splits s at the last grapheme boundary that fits in width cells, so
// a rune is never halved.
func cutCells(s string, width int) (head, rest string) {
	if width < 1 {
		width = 1
	}
	if ansi.StringWidth(s) <= width {
		return s, ""
	}
	return ansi.Truncate(s, width, ""), ansi.TruncateLeft(s, width, "")
}

// toolLine is the transcript's line for a tool the model asked for: the name and
// its arguments.
//
// toolArgs is its other half, so a restored session prints the same line a live
// turn did. Two callers that each built the line themselves would be a second
// answer to "what does a tool call look like" — free to drift, and with the
// transcript of a resumed session quietly showing a different shape from the one
// the user remembers.
// toolLine is one line of the transcript: what the agent called, and the one
// argument that decided what it was calling about. Two callers build it — the
// live turn and a restored session — and they go through here so a resumed
// transcript cannot show a different shape from the one the user remembers.
func toolLine(name string, args map[string]any) string {
	return toolCallSummary(name, args)
}

// toolArgs is a call's arguments as the transcript shows them. It is only a
// fallback now — toolCallSummary knows the tool and names the argument that
// decided what the call was about — and it is what an MCP tool's arguments go
// through, because those names are the server's, not dmcode's.
func toolArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	return genericArgs(args)
}

// truncate cuts s to at most n terminal cells. Counting cells instead of bytes
// matters because the strings routed through here hold Cyrillic (2 bytes per
// rune) and box-drawing runes: a byte cut lands mid-rune and the terminal
// prints replacement characters.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}
