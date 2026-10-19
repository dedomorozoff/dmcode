package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A citation in the agent's reply is a place the user can click. These tests
// pin what counts as one and what does not, because the rule that decides it
// is the difference between a click that opens the file and a click that
// opens nothing.

// TestACitationIsFound pins the shape the agent is asked to write: a path, a
// colon and a line number.
func TestACitationIsFound(t *testing.T) {
	for _, in := range []struct {
		row  string
		path string
		line int
	}{
		{"the bug is in internal/ui/ui.go:412", "internal/ui/ui.go", 412},
		{"see ./internal/tools/tools.go:1514", "internal/tools/tools.go", 1514},
		{`C:\Users\me\dmcode\main.go:42`, `C:\Users\me\dmcode\main.go`, 42},
		{"README.md:12", "README.md", 12},
		{"internal/ui/ui.go:1", "internal/ui/ui.go", 1},
	} {
		ref, ok := citeAt(in.row)
		if !ok {
			t.Errorf("citeAt(%q) found no citation", in.row)
			continue
		}
		if ref.path != in.path || ref.line != in.line {
			t.Errorf("citeAt(%q) = %q:%d, want %q:%d", in.row, ref.path, ref.line, in.path, in.line)
		}
	}
}

// TestAnUnlikelyRowIsNotACitation pins the other side of the rule: the pattern
// has to tell a place in the code from the many things that look like one.
func TestAnUnlikelyRowIsNotACitation(t *testing.T) {
	for _, row := range []string{
		"",
		"the build took 12 seconds",
		"example.com:8080 refused the connection",
		"pkg.go.dev:443 answered",
		"TODO: rewrite this",
		"version 1.2:3 is old",
		"http://localhost:3000",
		"https://example.com:8080/a.go:12",
	} {
		if ref, ok := citeAt(row); ok {
			t.Errorf("citeAt(%q) = %q:%d, want no citation", row, ref.path, ref.line)
		}
	}
}

// TestACitationSurvivesMarkdownStyling: the model may write the path in bold
// or in code, and the renderer styles it. What the user reads is the citation,
// so that is what has to be found.
func TestACitationSurvivesMarkdownStyling(t *testing.T) {
	rows := renderMarkdown("the bug is in **internal/ui/ui.go**:412", 80, styleAgent)
	if len(rows) == 0 {
		t.Fatal("the renderer produced no rows")
	}
	ref, ok := citeAt(rows[0])
	if !ok {
		t.Fatalf("no citation in the rendered row %q", rows[0])
	}
	if ref.path != "internal/ui/ui.go" || ref.line != 412 {
		t.Errorf("citation = %q:%d, want internal/ui/ui.go:412", ref.path, ref.line)
	}
}

// TestARepeatedCitationIsClickableEverywhere: the same file is often named
// twice in a reply, and a rule that let only the first mention be a place
// would make the second one a dead end.
func TestARepeatedCitationIsClickableEverywhere(t *testing.T) {
	rows := []string{"first: internal/ui/ui.go:412", "second: internal/ui/ui.go:412"}
	refs := citeRows(rows)
	for i, ref := range refs {
		if !ref.ok() {
			t.Errorf("row %d carries no citation", i)
			continue
		}
		if ref.path != "internal/ui/ui.go" || ref.line != 412 {
			t.Errorf("row %d = %q:%d, want internal/ui/ui.go:412", i, ref.path, ref.line)
		}
	}
}

// TestCiteRowsIsIndexAligned: the refs are read by position — the row index
// answers "which screen row is this" — so a row that skipped its entry would
// shift every ref below it.
func TestCiteRowsIsIndexAligned(t *testing.T) {
	rows := []string{"nothing here", "internal/ui/ui.go:412", "nor here"}
	refs := citeRows(rows)
	if len(refs) != len(rows) {
		t.Fatalf("refs = %d, want %d", len(refs), len(rows))
	}
	if refs[0].ok() {
		t.Errorf("the first row carries a citation: %q", refs[0].String())
	}
	if !refs[1].ok() {
		t.Error("the second row carries no citation")
	}
	if refs[2].ok() {
		t.Errorf("the third row carries a citation: %q", refs[2].String())
	}
}

// TestOnlyAgentProseCarriesCitations: a tool's own output is full of paths,
// and a line number in it is a fact about the output, not a place the user
// asked to go to. The transcript marks a change block by its own kind, and a
// citation is marked the same way.
func TestOnlyAgentProseCarriesCitations(t *testing.T) {
	m := &uiModel{}
	agent := m.rowStyle(kindAgent)
	tool := m.rowStyle(kindToolRes)

	agentRows := rowRows("the bug is in internal/ui/ui.go:412", 80, agent)
	if !agentRows.at(0).ok() {
		t.Error("the agent's reply carries no citation")
	}
	toolRows := rowRows("internal/ui/ui.go:412", 80, tool)
	if toolRows.at(0).ok() {
		t.Errorf("a tool result carries a citation: %q", toolRows.at(0).String())
	}
}

// TestAClickOnACitationOpensTheFile is the whole feature, measured the way a
// user would: the reply names a place, the click lands on the row that names
// it, and the editor opens that file at that line.
func TestAClickOnACitationOpensTheFile(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two", "three")
	m := jumpModel(t, dir)

	m.history = []line{{kindAgent, "the bug is in main.go:2"}}
	m.historyDirty = true
	m.followVP()

	row, col := frameRowContaining(t, m.View().Content, "the bug is in main.go:2")
	clickAt(m, col+1, row)

	if m.ed == nil {
		t.Fatal("a click on a citation must bring the workspace up")
	}
	if m.ed.Chat {
		t.Fatal("the workspace must be showing the file, not the chat")
	}
	if m.statusText != "main.go:2" {
		t.Fatalf("status = %q, want main.go:2", m.statusText)
	}
	if m.pendingJump != nil {
		t.Fatalf("the jump was left pending: %v", *m.pendingJump)
	}
	if shown := ansi.Strip(m.View().Content); !strings.Contains(shown, "Ln 2") {
		t.Fatalf("the cursor is not on line 2:\n%s", shown)
	}
	m.ed.Shutdown()
}

// TestACitationIsMarked: a place the user can click but that does not look
// like one is a place nobody clicks. The mark covers the citation and nothing
// else.
//
// The assertion is on the pieces rather than on the escape sequences: what a
// terminal makes of a style depends on its profile, so a test that pinned the
// bytes would be asking about the machine instead of the code.
func TestACitationIsMarked(t *testing.T) {
	plain := "the bug is in internal/ui/ui.go:412, and nowhere else"
	before, after, found := strings.Cut(plain, "internal/ui/ui.go:412")
	if !found {
		t.Fatal("the fixture lost its citation")
	}

	marked := renderCites(plain, styleAgent)
	if ansi.Strip(marked) != plain {
		t.Errorf("the marked run reads %q, want %q", ansi.Strip(marked), plain)
	}
	if marked == styleAgent.Render(plain) {
		t.Error("the citation is not marked: the run is the plain one")
	}
	if !strings.HasPrefix(marked, styleAgent.Render(before)) {
		t.Errorf("the text before the citation was repainted:\n%q", marked)
	}
	if !strings.HasSuffix(marked, styleAgent.Render(after)) {
		t.Errorf("the text after the citation was repainted:\n%q", marked)
	}
}

// TestTheMarkDoesNotChangeWhatIsRead: the styling is for the eye, and what the
// model wrote is what the user has to be able to copy.
func TestTheMarkDoesNotChangeWhatIsRead(t *testing.T) {
	src := "the bug is in internal/ui/ui.go:412"
	rows := renderMarkdown(src, 80, styleAgent)
	if got := ansi.Strip(rows[0]); got != src {
		t.Errorf("the row reads %q, want %q", got, src)
	}
}

// TestAClickOnAReplyWithoutACitationStillDoesNothing is the rule that has to
// survive this feature: a click on a line that names no place opens nothing.
func TestAClickOnAReplyWithoutACitationStillDoesNothing(t *testing.T) {
	dir := t.TempDir()
	m := jumpModel(t, dir)
	m.history = []line{{kindAgent, "a reply worth reading, long enough to be a row of its own"}}
	m.historyDirty = true
	m.followVP()

	row, col := frameRowContaining(t, m.View().Content, "worth reading")
	clickAt(m, col+1, row)
	if m.ed != nil {
		t.Fatal("a click on a reply that names no place must not open the workspace")
	}
}

// TestTheCitationIndexSurvivesTheRenderCache is the one that would have
// shipped broken. The index of places is built where the transcript is
// rendered, and the renderer keeps a per-line cache so a streamed token
// re-wraps one line instead of the whole conversation — which means most
// frames reuse the cache. A cache that kept the rows and dropped the places
// they stand for would empty the index on exactly those frames, so the click
// would work once and then quietly stop working on every message that
// scrolled past.
func TestTheCitationIndexSurvivesTheRenderCache(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two")
	m := jumpModel(t, dir)
	m.history = []line{{kindAgent, "the bug is in main.go:2"}}
	m.historyDirty = true
	m.followVP()

	for pass := range 4 {
		_ = m.View().Content
		if len(m.cachedRows) == 0 {
			t.Fatal("setup: nothing was cached")
		}
		m.historyDirty = true // force the next render to walk the cache
		m.history = append(m.history, line{kindAgent, "a reply, pass " + strconv.Itoa(pass)})
		m.followVP()

		row, col := frameRowContaining(t, m.View().Content, "the bug is in main.go:2")
		clickAt(m, col+1, row)
		if m.statusText != "main.go:2" {
			t.Fatalf("pass %d: status = %q, want main.go:2 — the cache dropped the index", pass, m.statusText)
		}
		m.ed.Chat = true // back to the chat, so the next pass starts there
	}
	m.ed.Shutdown()
}
