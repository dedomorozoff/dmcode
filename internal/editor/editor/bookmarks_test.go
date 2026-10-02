package editor

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBookmarkToggleAndJump(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "book.txt", "one\ntwo\nthree\nfour\nfive\n")
	m := New(f)
	m.width, m.height = 80, 24
	abs, _ := filepath.Abs(f)

	// Toggle bookmarks at lines 1 (0-based), 3, 4.
	m.cur().buf.SetCursor(1, 0)
	m = press(m, tea.KeyPressMsg{Code: 'm', Mod: tea.ModAlt})
	if !m.bookmarks[abs][2] {
		t.Fatal("bookmark not set at line 2")
	}
	m.cur().buf.SetCursor(3, 0)
	m = press(m, tea.KeyPressMsg{Code: 'm', Mod: tea.ModAlt})
	m.cur().buf.SetCursor(4, 0)
	m = press(m, tea.KeyPressMsg{Code: 'm', Mod: tea.ModAlt})
	if len(m.bookmarks[abs]) != 3 {
		t.Fatalf("want 3 bookmarks, got %v", m.bookmarks[abs])
	}

	// Jump next from line 0 → 2, then 4, then wrap to 2.
	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 1 {
		t.Fatalf("next bookmark from line 0: got line %d, want 1", got)
	}
	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 3 {
		t.Fatalf("next bookmark: got line %d, want 3", got)
	}
	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 4 {
		t.Fatalf("next bookmark: got line %d, want 4", got)
	}
	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 1 {
		t.Fatalf("next bookmark wrap: got line %d, want 1", got)
	}

	// Jump previous from line 0 wraps to 4.
	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt | tea.ModShift})
	if got := m.cur().buf.CurLine(); got != 4 {
		t.Fatalf("prev bookmark wrap: got line %d, want 4", got)
	}

	// Toggle off one bookmark.
	m = press(m, tea.KeyPressMsg{Code: 'm', Mod: tea.ModAlt})
	if m.bookmarks[abs][5] {
		t.Fatal("bookmark at line 5 should be removed")
	}
	if len(m.bookmarks[abs]) != 2 {
		t.Fatalf("want 2 bookmarks after removal, got %v", m.bookmarks[abs])
	}
}

func TestBookmarkJumpNone(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "nb.txt", "one\ntwo\n")
	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	if m.msg != "no bookmarks" {
		t.Fatalf("msg = %q, want 'no bookmarks'", m.msg)
	}
}

func TestGutterClickTogglesBookmark(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "g.txt", "one\ntwo\nthree\n")
	m := New(f)
	m.width, m.height = 80, 24
	abs, _ := filepath.Abs(f)

	gw := m.gutterWidthForTab(&m.tabs[0])
	leftW := m.leftRailWidth()

	// Middle click (the wheel button) toggles a bookmark.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: leftW + gw/2, Y: 1, Button: tea.MouseMiddle})
	if !m.bookmarks[abs][1] {
		t.Fatal("middle gutter click must set a bookmark at line 1")
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: leftW + gw/2, Y: 1, Button: tea.MouseMiddle})
	if m.bookmarks[abs][1] {
		t.Fatal("second middle gutter click must remove the bookmark")
	}
}

func TestGutterRendersBookmarkMark(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "bm.txt", "one\ntwo\nthree\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.toggleBookmarkAt(1)
	v := m.View()
	if !strings.Contains(v.Content, "◆") {
		t.Fatalf("gutter should show bookmark mark:\n%s", v.Content)
	}

	m.toggleBookmarkAt(1)
	v = m.View()
	if strings.Contains(v.Content, "◆") {
		t.Fatalf("gutter should not show bookmark mark after removal:\n%s", v.Content)
	}
}
