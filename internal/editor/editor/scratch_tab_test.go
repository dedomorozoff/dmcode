package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The standing empty buffer, and why it is not a file on screen.
//
// The editor always has a tab, so there is always a buffer to type into and a
// cur() that cannot be nil — an invariant that renderPaneRows and a hundred
// other places rely on. What it must not be is a *document*: a tab bar reading
// "1:[untitled]" over an empty gutter claims a file that does not exist, and
// pressing ctrl+e on a session where the agent had changed nothing showed
// exactly one. So the tab is derived rather than tracked, and everything here
// is about that one decision.

// scratchModel is a workspace with a project root and nothing open.
func scratchModel(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	m := New(dir)
	m.width, m.height = 90, 24
	return m, dir
}

// TestAnEmptyEditorSaysNothingIsOpen is the change itself: no [untitled] tab, no
// gutter of line numbers beside an empty screen, and a line that names the keys
// that fill it.
func TestAnEmptyEditorSaysNothingIsOpen(t *testing.T) {
	m, _ := scratchModel(t)
	if len(m.tabs) != 1 || !m.tabs[0].scratch() {
		t.Fatalf("setup: want one empty buffer, got %d tabs", len(m.tabs))
	}

	frame := stripANSI(m.View().Content)
	if strings.Contains(frame, "[untitled]") {
		t.Fatalf("an empty buffer must not be presented as a file:\n%s", frame)
	}
	if !strings.Contains(frame, m.t("editor.nothing_open")) {
		t.Fatalf("the editor must say no file is open:\n%s", frame)
	}
	if strings.Contains(m.tabBar(), "[untitled]") {
		t.Fatalf("the tab bar must not carry an empty tab: %q", stripANSI(m.tabBar()))
	}
	// "Ln 1, Col 1" is a cursor that is nowhere, and the gutter's column of 1s
	// numbers lines that do not exist.
	if bar := stripANSI(m.statusBar()); strings.Contains(bar, "Ln 1") {
		t.Errorf("the status bar must not report a position in no file: %q", bar)
	}
	if bar := stripANSI(m.paneStatusBar(0)); strings.Contains(bar, "Ln") || strings.Contains(bar, "untitled") {
		t.Errorf("the pane bar must not report a file: %q", bar)
	}
}

// TestTypingTurnsTheEmptyBufferIntoAFile is the other half, and the reason the
// predicate is derived: the user asked for a buffer by typing into it, so it has
// to appear under its own name, be numbered, and be saveable like any other.
func TestTypingTurnsTheEmptyBufferIntoAFile(t *testing.T) {
	m, _ := scratchModel(t)
	m = typeStr(m, "hi")

	if m.tabs[0].scratch() {
		t.Fatal("a buffer with typing in it is no longer the standing empty one")
	}
	if !strings.Contains(stripANSI(m.tabBar()), "[untitled]") {
		t.Fatalf("a tab with typing in it must be visible: %q", stripANSI(m.tabBar()))
	}
	frame := stripANSI(m.View().Content)
	if !strings.Contains(frame, "hi") {
		t.Fatalf("the text must be on screen with its gutter:\n%s", frame)
	}
	if strings.Contains(frame, m.t("editor.nothing_open")) {
		t.Fatalf("a buffer with typing in it must not still say nothing is open:\n%s", frame)
	}
}

// TestOpeningAFileDropsTheEmptyBuffer pins why it is dropped rather than merely
// hidden: kept in the slice it would be an invisible tab 1, and every tab the
// user can see would be numbered from 2.
func TestOpeningAFileDropsTheEmptyBuffer(t *testing.T) {
	m, dir := scratchModel(t)
	a := writeTemp(t, dir, "a.txt", "alpha\n")

	m.focusOrOpen(a)
	if len(m.tabs) != 1 || m.tabs[0].path != a {
		t.Fatalf("opening a file must leave exactly it open, tabs = %d", len(m.tabs))
	}
	bar := stripANSI(m.tabBar())
	if !strings.Contains(bar, "1:") || strings.Contains(bar, "2:") {
		t.Fatalf("the first tab must be numbered 1: %q", bar)
	}
}

// TestUnsavedTypingSurvivesAFileOpening is the exception: an empty buffer is
// dropped, one with the user's typing in it is not. Losing it would lose work.
func TestUnsavedTypingSurvivesAFileOpening(t *testing.T) {
	m, dir := scratchModel(t)
	m = typeStr(m, "draft")
	a := writeTemp(t, dir, "a.txt", "alpha\n")

	m.focusOrOpen(a)
	if len(m.tabs) != 2 {
		t.Fatalf("an unsaved buffer must stay open, tabs = %d", len(m.tabs))
	}
	if got := m.cur().path; got != a {
		t.Fatalf("the newly opened file must be focused, got %q", got)
	}
	m.setActiveTab(0)
	if !strings.Contains(string(m.cur().buf.LineAt(0)), "draft") {
		t.Fatal("the unsaved text must still be there")
	}
}

// TestClosingTheOnlyEmptyBufferDoesNotQuit pins the trap that comes with the tab
// existing at all. "Close the last tab" means quit, and with nothing on screen to
// close the key would be an exit the user cannot see — ctrl+q and F1 are the
// exits, and both are advertised.
func TestClosingTheOnlyEmptyBufferDoesNotQuit(t *testing.T) {
	m, _ := scratchModel(t)
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}); cmd != nil {
		t.Fatal("ctrl+w with no file open must not quit the editor")
	}
	if len(m.tabs) != 1 {
		t.Fatalf("ctrl+w must not close the standing buffer, tabs = %d", len(m.tabs))
	}

	// With a real file as the only tab the old rule still holds: closing it is
	// the last tab going, which is what quits.
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "alpha\n")
	real := New(dir, f)
	real.width, real.height = 90, 24
	if _, cmd := real.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+w on the last real tab must still quit")
	}
}
