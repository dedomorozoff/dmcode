package editor

import (
	"path/filepath"
	"testing"
)

// TestOpenAtFocusesTheFileAndTheLine pins what a click on a change block in the
// chat asks of the editor: the file the block names is open and focused, and
// the cursor is on the line the block's gutter said — which is why the argument
// is 1-based, the way the gutter prints it.
func TestOpenAtFocusesTheFileAndTheLine(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "one\ntwo\nthree\nfour\n")
	b := writeTemp(t, dir, "b.txt", "alpha\n")
	m := New(dir, b)

	if !m.OpenAt("a.txt", 3) {
		t.Fatal("OpenAt refused a file that exists")
	}
	if got := m.cur().path; got != a {
		t.Fatalf("active tab = %q, want %q", got, a)
	}
	if line := m.cur().buf.CurLine(); line != 2 {
		t.Fatalf("cursor on line %d (0-based), want 2 for gutter line 3", line)
	}

	// A second jump to the same file focuses it rather than opening it twice:
	// the walk in the chat visits a file as often as it has changes in it.
	if !m.OpenAt("a.txt", 1) {
		t.Fatal("OpenAt refused a file the workspace already has open")
	}
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2 — a repeat jump must not open another", len(m.tabs))
	}
	if line := m.cur().buf.CurLine(); line != 0 {
		t.Fatalf("cursor on line %d, want 0", line)
	}
}

// TestOpenAtClampsAPastTheEnd pins the choice the doc comment on OpenAt makes:
// a file edited by hand since the block was drawn is shorter than the block
// claims, and landing on its last line beats refusing to move.
func TestOpenAtClampsAPastTheEnd(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "one\ntwo\n")
	m := New(dir)

	m.OpenAt("a.txt", 500)
	if line := m.cur().buf.CurLine(); line != 1 {
		t.Fatalf("cursor on line %d, want the last line (1)", line)
	}
	if m.cur().path != a {
		t.Fatalf("active tab = %q, want %q", m.cur().path, a)
	}
}

// TestOpenAtRefusesWhatIsNotThere pins the other half: a path that does not
// exist is refused rather than opened, because opening it would leave an empty
// tab and a "new file" message for something the user asked to look at.
func TestOpenAtRefusesWhatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	for _, p := range []string{"nope.txt", filepath.Join(dir, "nope.txt"), dir, "", "   "} {
		if m.OpenAt(p, 1) {
			t.Errorf("OpenAt(%q) reported a file that is not there", p)
		}
	}
	if len(m.tabs) != 1 || m.tabs[0].path != "" {
		t.Fatalf("a refused path must not open a tab, tabs = %d", len(m.tabs))
	}
}

// TestOpenAtIsAbsoluteAndRelativeAlike pins the path form the chat's change
// blocks carry: a header names the file the way the agent spelled it to the
// tool, which is usually relative to the workspace root, but an absolute one
// has to reach the same tab.
func TestOpenAtIsAbsoluteAndRelativeAlike(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "one\ntwo\n")
	m := New(dir)

	m.OpenAt(filepath.Join(dir, "a.txt"), 2)
	if m.cur().path != a {
		t.Fatalf("absolute path opened %q, want %q", m.cur().path, a)
	}
}
