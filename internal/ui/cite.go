package ui

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Clicking a place the agent named.
//
// The transcript already knows how to open a file at a line: a change block's
// gutter carries the number, and clicking one of its rows opens the file
// there. What it could not do was open a place the agent *talked about* — and
// that is where the conclusions are. "The bug is in internal/ui/ui.go:412" is
// the most useful sentence in a turn, and it was the one sentence the user
// could not act on: the number was decoration, exactly like the number on a
// diff row was before the jump feature existed.
//
// So a citation is read the way a change block is read: as a value beside the
// row, not as text inside it. The row is styled, wrapped and maybe truncated
// by the time a click arrives, so the place is found while the text is still
// text and carried beside the row it belongs to.

// citeRe finds a path, a colon and a line number.
//
// The path part is deliberately loose — any run of letters, digits, dots,
// slashes and backslashes, with an optional Windows drive letter — and the
// extension is judged separately, because the extension is what tells a place
// in the code from a port number in a URL.
var citeRe = regexp.MustCompile(`((?:[A-Za-z]:)?[A-Za-z0-9_./\\-]*[A-Za-z0-9_-]\.([A-Za-z][A-Za-z0-9]*)):([0-9]+)`)

// urlRe finds a URL, so that a path:line inside one is part of an address and
// not a place in the code. "https://host:8080/a.go:12" carries a shape the
// citation pattern cannot tell from a real reference on its own.
var urlRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S+`)

// citeExts are the extensions a citation may name.
//
// The list is the whole of the "is this a place in the code" decision, and it
// is a list rather than a rule because there is no rule: "example.com:8080"
// has an extension, and so does "pkg.go.dev:443". Only a file that is
// plausibly something a person edits is accepted, and a domain is not.
var citeExts = map[string]bool{
	"go": true, "py": true, "js": true, "jsx": true, "ts": true, "tsx": true,
	"mjs": true, "cjs": true, "rs": true, "c": true, "h": true, "cc": true,
	"cpp": true, "cxx": true, "hpp": true, "hh": true, "java": true, "kt": true,
	"kts": true, "scala": true, "rb": true, "php": true, "cs": true, "fs": true,
	"fsx": true, "swift": true, "m": true, "mm": true, "ex": true, "exs": true,
	"erl": true, "hs": true, "lua": true, "pl": true, "pm": true, "r": true,
	"jl": true, "dart": true, "zig": true, "nim": true, "ml": true, "clj": true,
	"vue": true, "svelte": true, "sql": true, "sh": true, "bash": true,
	"zsh": true, "ps1": true, "bat": true, "cmd": true, "md": true, "mdx": true,
	"txt": true, "json": true, "yaml": true, "yml": true, "toml": true,
	"ini": true, "cfg": true, "conf": true, "xml": true, "html": true,
	"htm": true, "css": true, "scss": true, "sass": true, "less": true,
	"proto": true, "graphql": true, "gql": true, "tf": true, "env": true,
	"properties": true, "gradle": true, "makefile": true, "dockerfile": true,
}

// citation is one place a text names.
type citation struct {
	// start and end are the byte range of the whole "path:line" in the text.
	start, end int
	// path is the file, as it was written.
	path string
	// line is the number after the colon.
	line int
}

// citations finds the places a text names, in the order they appear.
//
// A URL is skipped whole: an address that happens to end in ".go:12" is part
// of the address, and a place inside it is not a place in the code.
func citations(text string) []citation {
	if text == "" {
		return nil
	}
	urls := urlRe.FindAllStringIndex(text, -1)
	var out []citation
	for _, m := range citeRe.FindAllStringSubmatchIndex(text, -1) {
		if withinAny(urls, m[0]) {
			continue
		}
		if !citeExts[strings.ToLower(text[m[4]:m[5]])] {
			continue
		}
		n, err := strconv.Atoi(text[m[6]:m[7]])
		if err != nil || n < 1 {
			continue
		}
		out = append(out, citation{
			start: m[0],
			end:   m[1],
			path:  strings.TrimPrefix(text[m[2]:m[3]], "./"),
			line:  n,
		})
	}
	return out
}

// citeAt reads the first citation in a rendered row.
//
// The row is read as it is drawn, with the escape sequences taken out: the
// markdown renderer may have styled part of the citation, and a styled run
// still says the same thing as an unstyled one. What is left is the text the
// user reads, which is the only text worth answering a question about.
func citeAt(row string) (codeRef, bool) {
	plain := ansi.Strip(row)
	cs := citations(plain)
	if len(cs) == 0 {
		return codeRef{}, false
	}
	return codeRef{path: cs[0].path, line: cs[0].line}, true
}

// withinAny reports whether an index falls inside one of the spans.
func withinAny(spans [][]int, at int) bool {
	for _, s := range spans {
		if at >= s[0] && at < s[1] {
			return true
		}
	}
	return false
}

// citeRows gives each row the place it names, if any.
//
// A row may name several places; the first is the one the row stands for,
// because the first is the one the sentence is about. A row that names none
// carries nothing, which is what every row of every other kind of line does.
//
// The rows are the rendered ones, so a citation the markdown renderer
// rearranged — "**internal/ui/ui.go**:412" becomes "internal/ui/ui.go:412" on
// screen — is found in the shape the user sees rather than in the shape the
// model wrote. A citation the panel was too narrow to keep in one row is lost
// with the rest of the row: there is no way to open a place that is not on
// the screen.
func citeRows(rows []string) []codeRef {
	refs := make([]codeRef, len(rows))
	for i, r := range rows {
		if ref, ok := citeAt(r); ok {
			refs[i] = ref
		}
	}
	return refs
}

// renderCites renders a run of plain text with its citations marked.
//
// A citation is a place the user can click, and a thing that can be clicked
// but does not look like it cannot be clicked is a thing nobody clicks. The
// mark is the tool colour and a weight: the colour is the same one the panel
// uses for the machine's own words, and the weight is there because many
// terminals ship a font with no separate bold face and would show the colour
// alone. An underline was tried and dropped: lipgloss paints it a character
// at a time, which turns one citation into a hundred escape sequences.
//
// The run is cut into pieces and each is rendered on its own rather than one
// span wrapped in another. A style applied around an already-rendered string
// nests two escape sequences, and the inner reset cancels the outer one for
// everything after it — which is the mistake mdInline's own comment warns
// about, and the reason the pieces are rendered side by side instead.
func renderCites(s string, base lipgloss.Style) string {
	cs := citations(s)
	if len(cs) == 0 {
		return base.Render(s)
	}
	marked := base.Bold(true).Foreground(cTool)
	var b strings.Builder
	prev := 0
	for _, c := range cs {
		if c.start > prev {
			b.WriteString(base.Render(s[prev:c.start]))
		}
		b.WriteString(marked.Render(s[c.start:c.end]))
		prev = c.end
	}
	if prev < len(s) {
		b.WriteString(base.Render(s[prev:]))
	}
	return b.String()
}
