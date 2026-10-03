package editor

import (
	"path/filepath"
	"testing"
)

// TestOpenChangedTabsOpensAndDedupes pins what entering the editor from the
// chat does with the agent's edits: every changed file gets a tab, an already
// open one is focused instead of opened twice, and a nil host is a no-op.
func TestOpenChangedTabsOpensAndDedupes(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(dir)

	m.OpenChangedTabs() // nil host: must not panic
	if len(m.tabs) != 1 || m.tabs[0].path != "" {
		t.Fatalf("nil host must open nothing, tabs = %d", len(m.tabs))
	}

	files := []string{a, a, filepath.Join(dir, "b.txt")}
	m.Host = &Host{ChangedFiles: func() []string { return files }}
	m.OpenChangedTabs()

	if len(m.tabs) != 3 {
		t.Fatalf("tabs = %d, want 3 (a.txt once, b.txt once, the untitled stays)", len(m.tabs))
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
	if len(m.tabs) != 1 || m.tabs[0].path != "" {
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
