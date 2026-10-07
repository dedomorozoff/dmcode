package editor

import (
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestSidebarRailWidth(t *testing.T) {
	root := t.TempDir()
	writeTemp(t, root, "a.txt", "hello\n")
	m := New(filepath.Join(root, "a.txt"))
	m.width, m.height = 120, 24
	m.cfg.UI.TreeWidth = 25

	m.treeVisible = true
	m.rebuildTree()
	rows := m.treePanel(10)
	for i, r := range rows {
		if got := lipgloss.Width(r); got != m.sidebarWidth() {
			t.Fatalf("tree row %d: width %d != sidebarWidth %d", i, got, m.sidebarWidth())
		}
	}

	m.width, m.height = 120, 24
	content := m.View().Content
	for i, row := range plainRows(content) {
		if got := len([]rune(row)); got != m.width {
			t.Fatalf("full row %d: width %d != m.width %d: %q", i, got, m.width, row)
		}
	}
}

func TestCursorScreenPosMatchesDrawnCursor(t *testing.T) {
	root := t.TempDir()
	writeTemp(t, root, "a.txt", "hello world\n")
	m := New(filepath.Join(root, "a.txt"))
	m.width, m.height = 80, 24
	m.cfg.UI.TreeWidth = 30
	m.treeVisible = true
	m.rebuildTree()

	m.tabs[0].buf.SetCursor(0, 6)

	sx, sy := m.cursorScreenPos()
	if sy != 1 {
		t.Fatalf("expected screen row 1 (tab bar offset), got %d", sy)
	}
	gw := m.gutterWidthForTab(&m.tabs[0])
	if sx != m.leftRailWidth()+gw+6 {
		t.Fatalf("cursorScreenPos x=%d, want leftRail(%d)+gutter(%d)+col(6)",
			sx, m.leftRailWidth(), gw)
	}
}
