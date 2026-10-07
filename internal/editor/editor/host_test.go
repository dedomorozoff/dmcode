package editor

import (
	"path/filepath"
	"testing"
)

// TestOpenChangedTabsOpensAndDedupes pins what entering the editor from the
// chat does with the agent's edits: every changed file gets a tab, an already
// open one is focused instead of opened twice, and a nil host is a no-op.
//
// The standing empty buffer does not survive the first file: it is not drawn, so
// keeping it would push every visible tab's number up by one for a tab nobody can
// see. Two files opened, and two tabs — the empty buffer was the third the
// editor started from.
func TestOpenChangedTabsOpensAndDedupes(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(dir)

	m.OpenChangedTabs() // nil host: must not panic
	if len(m.tabs) != 1 || !m.tabs[0].scratch() {
		t.Fatalf("nil host must open nothing, tabs = %d", len(m.tabs))
	}

	files := []string{a, a, filepath.Join(dir, "b.txt")}
	m.Host = &Host{ChangedFiles: func() []string { return files }}
	m.OpenChangedTabs()

	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2 (a.txt once, b.txt once, the empty buffer dropped)", len(m.tabs))
	}
	if got := m.cur().path; got != files[len(files)-1] {
		t.Fatalf("active tab = %q, want the last changed file", got)
	}
}

// TestCloseAllTabsDropsCleanKeepsDirty pins what a new session does to the
// workspace: the tabs the agent's edits accumulated are dropped, a dirty
// buffer — the user's own unsaved typing — survives, and an emptied editor
// falls back to the bare untitled tab it started from.
func TestCloseAllTabsDropsCleanKeepsDirty(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	b := writeTemp(t, dir, "b.txt", "beta\n")
	m := New(dir, a, b)
	if len(m.tabs) != 2 {
		t.Fatalf("setup: tabs = %d, want 2", len(m.tabs))
	}

	m.CloseAllTabs()
	if len(m.tabs) != 1 || !m.tabs[0].scratch() {
		t.Fatalf("clean tabs must be dropped, tabs = %d (path %q)", len(m.tabs), m.tabs[0].path)
	}

	// A dirty tab is the user's typing: it stays through the reset.
	m = New(dir, a, b)
	m.setActiveTab(0)
	m.cur().buf.Insert('!')
	m.CloseAllTabs()
	if len(m.tabs) != 1 || m.tabs[0].path != a {
		t.Fatalf("a dirty tab must survive, tabs = %d (path %q)", len(m.tabs), m.tabs[0].path)
	}
	if !m.cur().buf.Dirty() {
		t.Fatal("the kept tab must still be dirty")
	}
}

// TestEmbeddedNewDoesNotRestoreTheSession pins the fresh start: a standalone
// editor picks up the tabs the last run saved, an embedded one — dmcode's
// workspace — opens bare, because a new program run is a new conversation and
// the host decides what is open.
func TestEmbeddedNewDoesNotRestoreTheSession(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "doc.txt", "one\n")

	standalone := New(dir, f)
	standalone.root = dir
	standalone.saveSession()

	// Standalone: the saved tab comes back.
	m := New(dir)
	if len(m.tabs) != 1 || m.tabs[0].path != f {
		t.Fatalf("standalone restore broken: tabs = %d", len(m.tabs))
	}

	// Embedded: a bare editor, no tabs from the last run.
	e := NewEmbedded(dir)
	if len(e.tabs) != 1 || e.tabs[0].path != "" {
		t.Fatalf("embedded start must be bare, tabs = %d (path %q)", len(e.tabs), e.tabs[0].path)
	}
	if !e.Embed {
		t.Fatal("NewEmbedded must mark the model embedded")
	}
}
