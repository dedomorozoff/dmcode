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
