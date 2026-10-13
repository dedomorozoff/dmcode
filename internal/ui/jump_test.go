package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/editor/editor"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// Going from a change in the transcript to that change in the file.
//
// The arithmetic that makes it work — which frame row a transcript row is drawn
// on, and what that row stands for — is exactly the kind of thing that reads
// correct and is wrong, so the tests here drive the real handlers against a frame
// that was actually built, and find the row to click by looking for it in the
// frame rather than by believing the formula that put it there.

// jumpModel is a chat with the workspace pointed at dir, sized like a terminal,
// and mouse reporting on — the state a click arrives in.
func jumpModel(t *testing.T, dir string) *uiModel {
	t.Helper()
	pinEditorFrame(t, dir)
	m := newTurnModel(t)
	m.workDir = dir
	m.workDirShort = filepath.Base(dir)
	m.width, m.height = 100, 30
	m.mouseEnabled = true
	m.layout()
	m.followVP()
	return m
}

// writeJumpFile puts a file on disk that a change block can name, and reports its
// path. The file has to exist: OpenAt refuses a path that is not there, and a
// jump to a file that does not exist is not the thing under test.
func writeJumpFile(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// frameRowContaining returns the row of a built frame whose plain text contains
// needle, and the column it starts at.
func frameRowContaining(t *testing.T, frame, needle string) (row, col int) {
	t.Helper()
	for i, line := range strings.Split(frame, "\n") {
		if c := strings.Index(ansi.Strip(line), needle); c >= 0 {
			return i, c
		}
	}
	t.Fatalf("the frame has no row containing %q:\n%s", needle, frame)
	return 0, 0
}

// addChange appends a change block the way a tool result does.
func addChange(m *uiModel, diff string) {
	m.Update(toolResMsg{name: "edit_file", output: `{"replacements":1}`, diff: diff})
}

// clickAt presses and releases the left button on one frame row. It goes through
// Update rather than the handlers, because where a click is delivered from
// changes with the workspace: bubbletea hands it to the chat before the editor
// exists and to the editor's own handling afterwards, which then routes it back.
func clickAt(m *uiModel, x, y int) {
	_, _ = m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// TestClickOnADiffRowOpensTheFileThere is the feature. It builds a frame with a
// change block in it, finds the frame row showing the third line of the file,
// clicks exactly there, and asks the workspace that ends up on screen what it
// opened.
//
// The row is found in the frame rather than computed, so a transcriptTop that is
// off by one — the header showing, the panel's border, a sidebar that changes
// both — fails here instead of jumping somewhere plausible.
func TestClickOnADiffRowOpensTheFileThere(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two", "three")
	m := jumpModel(t, dir)

	addChange(m, "── main.go\n-2: one\n+2: ONE\n+3: two")
	row, col := frameRowContaining(t, m.View().Content, "+ two")
	clickAt(m, col+1, row)

	if m.ed == nil {
		t.Fatal("a click on a change row must bring the workspace up")
	}
	if m.ed.Chat {
		t.Fatal("the workspace must be showing the file, not the chat")
	}
	if m.statusText != "main.go:3" {
		t.Fatalf("status = %q, want main.go:3", m.statusText)
	}
	if m.pendingJump != nil {
		t.Fatalf("the jump was left pending: %v", *m.pendingJump)
	}

	// What the user lands on, read off the frame the editor draws: its tab bar
	// names the file and its status bar counts the line the cursor is on.
	shown := ansi.Strip(m.View().Content)
	if !strings.Contains(shown, "main.go") {
		t.Fatalf("the workspace does not show main.go:\n%s", shown)
	}
	if !strings.Contains(shown, "Ln 3") {
		t.Fatalf("the cursor is not on line 3:\n%s", shown)
	}
}

// TestClickOnARemovedRowLandsOnItsReplacement pins the one place the numbers in
// a diff block are not the file's own: a removal's number is the old file's, and
// the line it used to be on is the line the replacement took.
func TestClickOnARemovedRowLandsOnItsReplacement(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two", "three")
	m := jumpModel(t, dir)

	addChange(m, "── main.go\n-2: one\n+2: ONE")
	row, col := frameRowContaining(t, m.View().Content, "- one")
	clickAt(m, col+1, row)

	if m.statusText != "main.go:2" {
		t.Fatalf("status = %q, want main.go:2", m.statusText)
	}
	if shown := ansi.Strip(m.View().Content); !strings.Contains(shown, "Ln 2") {
		t.Fatalf("the cursor is not on line 2:\n%s", shown)
	}
}

// TestClickOnABlockHeaderNamesNoLine pins what the header of a change block is:
// it names the file and no line in it, so it stands for nothing and a click on
// it does nothing — the rows below it are the ones with numbers.
func TestClickOnABlockHeaderNamesNoLine(t *testing.T) {
	m := &uiModel{}
	got := diffRows("── main.go\n+2: x\n+3: y", 60, m.rowStyle(kindDiff))
	if r := got.at(0); r.ok() {
		t.Fatalf("the header names a file, not a line, and must stand for nothing: %v", r)
	}
	if r := got.at(1); r.line != 2 || r.path != "main.go" {
		t.Fatalf("row 1 stands for %v, want main.go:2", r)
	}
}

// TestTheCappedTailOfABlockNamesNoLine: a block too tall to draw whole ends in
// "… (+N more lines)", which has no number shape. Reading the digits out of it —
// which is what recovering the line from the drawn row would risk doing — would
// point a click at a line of the file that has nothing to do with anything.
func TestTheCappedTailOfABlockNamesNoLine(t *testing.T) {
	var b strings.Builder
	b.WriteString("── big.txt\n")
	for i := range 80 {
		b.WriteString("+" + strconv.Itoa(i+1) + ": line\n")
	}
	got := diffRows(b.String(), 60, (&uiModel{}).rowStyle(kindDiff))
	last := got.at(len(got.rows) - 1)
	if last.ok() {
		t.Fatalf("the capped tail names no line, got %v", last)
	}
	if got.at(len(got.rows)-2).line == 0 {
		t.Fatal("the row before the tail must still carry its number")
	}
}

// TestAWrappedChangeRowOpensTheSameLine: a code line too wide for the panel is
// drawn across two screen rows, and the second one's gutter is blank — which is
// exactly why the line cannot be read back out of the drawn row. Both rows still
// stand for the same line of the file.
func TestAWrappedChangeRowOpensTheSameLine(t *testing.T) {
	m := &uiModel{}
	// A language chroma does not know keeps the plain, wrapped path.
	long := "+7: " + strings.Repeat("word ", 30)
	got := diffRows("── notes.txt\n"+long, 40, m.rowStyle(kindDiff))
	if len(got.rows) < 2 {
		t.Fatalf("the row was not wrapped, got %d rows", len(got.rows))
	}
	for i := 1; i < len(got.rows); i++ {
		if r := got.at(i); r.line != 7 || r.path != "notes.txt" {
			t.Fatalf("wrapped row %d stands for %v, want notes.txt:7", i, r)
		}
	}
}

// TestAClickOnAnythingElseStillDoesNothing is the rule that has to survive this
// feature: a click that is not on a change row copies nothing and opens nothing.
// Without it every click in the transcript would be a jump.
func TestAClickOnAnythingElseStillDoesNothing(t *testing.T) {
	dir := t.TempDir()
	m := jumpModel(t, dir)
	addChange(m, "── main.go\n+2: x")
	m.history = append(m.history, line{kindAgent, "a reply worth reading, long enough to be a row of its own"})
	m.historyDirty = true
	m.followVP()

	row, col := frameRowContaining(t, m.View().Content, "worth reading")
	clickAt(m, col+1, row)
	if m.ed != nil {
		t.Fatal("a click on a reply must not open the workspace")
	}
	if m.pendingJump != nil {
		t.Fatalf("a jump was left pending from a click on nothing: %v", *m.pendingJump)
	}

	// The sidebar has targets of its own now — one per changed file — so "a click
	// past the chat panel does nothing" is no longer true of the panel as a whole.
	// It is true of every row in it except those, and this session has changed
	// nothing, so the whole panel is still inert: a click on a heading must not
	// open the workspace.
	clickAt(m, m.chatBoxWidth()+1, row)
	if m.ed != nil {
		t.Fatal("a click on a sidebar row that names no file must not open the workspace")
	}
}

// TestAClickOnAChangedFileRowOpensThatFile is the sidebar's half of the same
// feature: a row of the CHANGES list names a file, and clicking it opens the
// file. The row is found in a real frame rather than computed, for the reason
// jump.go gives for the transcript — a sidebarTop that is off by one would
// otherwise open a plausible wrong file rather than failing.
func TestAClickOnAChangedFileRowOpensThatFile(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "alpha.txt", "one", "two", "three")
	cleanTally(t)
	m := jumpModel(t, dir)
	m.showSidebar = true
	// A tall terminal, because the panel's sections are trimmed from the bottom
	// and the hotkeys — twelve rows of them — are the first thing to go. A short
	// frame would test the trim rather than the click.
	m.height = 44
	m.layout()
	m.followVP()

	dmtools.RecordChange(filepath.Join(dir, "alpha.txt"), "one\n", "ONE\n")

	frame := m.View().Content
	row, col := frameRowContaining(t, frame, "alpha.txt")
	// The panel's own columns must be left behind: the row is found in the
	// sidebar, and a click short of it would be a click on the transcript.
	side := m.chatBoxWidth() + sidebarGap + 2
	if col < side {
		t.Fatalf("the row was found at column %d, left of the panel's edge at %d", col, side)
	}
	clickAt(m, side, row)

	if m.ed == nil {
		t.Fatal("a click on a changed-file row must bring the workspace up")
	}
	if m.ed.Chat {
		t.Fatal("the workspace must be showing the file, not the chat")
	}
	// The status line names the place it opened, which is the path the tools
	// recorded — absolute, because that is what the tally holds and what OpenAt
	// is given. What is being checked is that it is *this* file and line 1.
	if !strings.HasSuffix(m.statusText, string(filepath.Separator)+"alpha.txt:1") {
		t.Fatalf("status = %q, want it to end in alpha.txt:1", m.statusText)
	}
	if shown := ansi.Strip(m.View().Content); !strings.Contains(shown, "alpha.txt") {
		t.Fatalf("the workspace does not show alpha.txt:\n%s", shown)
	}
	m.ed.Shutdown()
}

// TestAClickOnASidebarHeadingOpensNothing: the panel's other rows — MODEL, SESSION,
// GIT, the plan — stand for nothing, and a click on one must still do nothing.
// Without this the sidebar would turn every click inside it into a jump.
func TestAClickOnASidebarHeadingOpensNothing(t *testing.T) {
	dir := t.TempDir()
	cleanTally(t)
	m := jumpModel(t, dir)
	m.showSidebar = true
	m.layout()
	m.followVP()

	frame := m.View().Content
	row, _ := frameRowContaining(t, frame, "SESSION")
	clickAt(m, m.chatBoxWidth()+sidebarGap+2, row)

	if m.ed != nil {
		t.Fatal("a click on a sidebar heading must not open the workspace")
	}
	if m.pendingJump != nil {
		t.Fatalf("a jump was left pending from a click on a heading: %v", *m.pendingJump)
	}
}

// TestTheChangeIndexSurvivesTheRenderCache is the one that would have shipped
// broken. The index is built where the transcript is rendered, and the renderer
// keeps a per-line cache so a streamed token re-wraps one line instead of the
// whole conversation — which means most frames reuse the cache. A cache that kept
// the styled rows and dropped the places they stand for would empty the index on
// exactly those frames, so the click would work once and then quietly stop
// working on every message that scrolled past.
func TestTheChangeIndexSurvivesTheRenderCache(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two")
	m := jumpModel(t, dir)
	addChange(m, "── main.go\n+2: one")

	// The first render builds the index; the next ones are cache hits, which is
	// the path that matters. Between them a message arrives, the way one does all
	// session — the re-render is exactly when the cache decides what to keep.
	for pass := range 4 {
		frame := m.View().Content
		if len(m.cachedRows) == 0 {
			t.Fatal("setup: nothing was cached")
		}
		m.historyDirty = true // force the next render to walk the cache
		m.history = append(m.history, line{kindAgent, "a reply, pass " + strconv.Itoa(pass)})
		m.followVP()
		_ = frame
		row, col := frameRowContaining(t, m.View().Content, "+ one")
		clickAt(m, col+1, row)
		if m.statusText != "main.go:2" {
			t.Fatalf("pass %d: status = %q, want main.go:2 — the cache dropped the index", pass, m.statusText)
		}
		m.ed.Chat = true // back to the chat, so the next pass starts there
	}
}

// TestAClickOnABoxDoesNotJumpToWhatIsBehindIt: with a box up, the frame on screen
// is the box. The transcript rows are still in the frame underneath it — so the
// hit test would answer with one of them — and it would be the change behind the
// thing the user pointed at.
func TestAClickOnABoxDoesNotJumpToWhatIsBehindIt(t *testing.T) {
	for _, up := range []struct {
		name string
		open func(m *uiModel)
	}{
		{"palette", func(m *uiModel) { m.palette = paletteState{open: true} }},
		{"sessions", func(m *uiModel) { m.sessionsList = sessionsState{open: true} }},
		{"command list", func(m *uiModel) { m.suggest = []suggestion{{text: "/help", desc: "show the hotkeys"}} }},
	} {
		dir := t.TempDir()
		writeJumpFile(t, dir, "main.go", "package main", "one", "two", "three")
		m := jumpModel(t, dir)
		addChange(m, "── main.go\n+2: one")
		up.open(m)

		// The row the change sits on in the plain frame. The box may well be drawn
		// over it; that is the point.
		row, col := frameRowContaining(t, func() string {
			m.palette, m.sessionsList, m.suggest = paletteState{}, sessionsState{}, nil
			return m.View().Content
		}(), "+ one")

		up.open(m)
		clickAt(m, col+1, row)
		if m.ed != nil {
			t.Fatalf("%s up: a click behind a box must not open the workspace", up.name)
		}
	}
}

// TestTheWalkStepsThroughTheChangesAndComesRound pins alt+g: repeated presses
// visit each change in turn and come back to the first, rather than handing back
// the same one for ever — which is what a walk that re-derived its position from
// the scroll offset would do, since two presses do not move the view.
func TestTheWalkStepsThroughTheChangesAndComesRound(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "a.txt", "one", "two", "three")
	writeJumpFile(t, dir, "b.txt", "alpha", "beta", "gamma")
	m := jumpModel(t, dir)
	addChange(m, "── a.txt\n+2: two")
	addChange(m, "── b.txt\n+3: gamma")
	m.View()

	for i, want := range []string{"a.txt:2", "b.txt:3", "a.txt:2", "b.txt:3"} {
		_ = m.jumpToNextChange()
		if m.statusText != want {
			t.Fatalf("press %d: status = %q, want %q", i+1, m.statusText, want)
		}
	}

	// With nothing changed there is nowhere to go, and the key says so rather
	// than opening something arbitrary.
	fresh := jumpModel(t, t.TempDir())
	fresh.View()
	_ = fresh.jumpToNextChange()
	if fresh.statusText != "nothing changed yet" {
		t.Fatalf("status = %q, want the nothing-changed note", fresh.statusText)
	}
	if fresh.ed != nil {
		t.Fatal("a walk with nowhere to go must not open the workspace")
	}
}

// TestTheWalkContinuesFromWhereAClickLeftIt: a click is a jump like any other, so
// the key after it steps on to the next change rather than back to the one
// already open in the editor.
func TestTheWalkContinuesFromWhereAClickLeftIt(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "a.txt", "one", "two", "three")
	writeJumpFile(t, dir, "b.txt", "alpha", "beta", "gamma")
	m := jumpModel(t, dir)
	addChange(m, "── a.txt\n+2: two")
	addChange(m, "── b.txt\n+3: gamma")

	row, col := frameRowContaining(t, m.View().Content, "+ two")
	clickAt(m, col+1, row)
	if m.statusText != "a.txt:2" {
		t.Fatalf("the click landed on %q, want a.txt:2", m.statusText)
	}
	_ = m.jumpToNextChange()
	if m.statusText != "b.txt:3" {
		t.Fatalf("the key after a click went to %q, want b.txt:3", m.statusText)
	}
}

// TestTheJumpKeyIsAltGAndNothingElse pins the key, and the reason it is alt+g:
// ctrl+g is the editor's git panel, which answers it before the chat ever sees
// it — so the mnemonic key would work in a bare chat and do nothing at all once
// the editor existed.
func TestTheJumpKeyIsAltGAndNothingElse(t *testing.T) {
	if got := jumpKey.Help().Key; got != "alt+g" {
		t.Fatalf("jump key = %q, want alt+g", got)
	}
	if !jumpPressed(tea.KeyPressMsg{Code: 'g', Mod: tea.ModAlt}) {
		t.Fatal("alt+g must be the jump key")
	}
	for _, msg := range []tea.KeyPressMsg{
		{Code: 'g', Mod: tea.ModCtrl},
		{Code: 'g'},
		{Code: 'q', Mod: tea.ModCtrl},
		{Code: 'e', Mod: tea.ModCtrl},
	} {
		if jumpPressed(msg) {
			t.Errorf("%v must not be the jump key", msg)
		}
	}
}

// TestAClickInsideTheWorkspaceReachesTheFile is the other half of that. Once the
// workspace exists the chat is drawn in its main area and its clicks arrive
// through the editor's own mouse handling, routed back by the host callbacks —
// and a jump taken from there asks for the mode flip from inside the editor's
// Update, whose returned copy a direct flip would be clobbered by.
func TestAClickInsideTheWorkspaceReachesTheFile(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two", "three")
	m := jumpModel(t, dir)
	_ = m.openEditor() // the workspace, with the editor in its main area
	// ctrl+q is what puts the chat back in the main area, so that is how the
	// second half of a session arrives here.
	_, _ = m.Update(editor.CloseEditorMsg{})
	if !m.ed.Chat {
		t.Fatal("setup: the chat should be showing inside the workspace")
	}

	addChange(m, "── main.go\n+2: one")
	// The frame is the main area's, so the row found in it is already relative to
	// the origin the host callbacks subtract.
	row, col := frameRowContaining(t, m.View().Content, "+ one")
	clickAt(m, col+1, row)

	if m.ed.Chat {
		t.Fatal("a click on a change row must switch the workspace to the file")
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
}

// TestTheJumpWorksWithTheSidebarOut: the header is on screen when the sidebar is
// not, and the chat panel's first row moves down by the header's height when it
// is. Both layouts have to land on the right line, because which one is showing
// is the user's choice and not a setting this feature can assume.
func TestTheJumpWorksWithTheSidebarOut(t *testing.T) {
	for _, sidebar := range []bool{true, false} {
		dir := t.TempDir()
		writeJumpFile(t, dir, "main.go", "package main", "one", "two", "three")
		m := jumpModel(t, dir)
		m.showSidebar = sidebar
		m.layout()
		m.followVP()
		addChange(m, "── main.go\n+2: one")

		row, col := frameRowContaining(t, m.View().Content, "+ one")
		clickAt(m, col+1, row)
		if m.statusText != "main.go:2" {
			t.Fatalf("sidebar=%v: status = %q, want main.go:2", sidebar, m.statusText)
		}
		if shown := ansi.Strip(m.View().Content); !strings.Contains(shown, "Ln 2") {
			t.Fatalf("sidebar=%v: the cursor is not on line 2:\n%s", sidebar, shown)
		}
		// The workspace holds a terminal and a watcher; each one started here has
		// to be torn down or the test leaves goroutines behind.
		m.ed.Shutdown()
	}
}
