package editor

import (
	"strings"
	"testing"
)

// TestChatModeFramesThePanels pins the workspace look: in chat mode the tree
// rail and the terminal draw a box border (the same look as the chat panel),
// while the editor keeps its bare divider. The row count is the same either
// way — the border replaces the divider row and the panel's last row — so
// none of the mouse geometry moves.
func TestChatModeFramesThePanels(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "main.go", "package main\n")
	m := New(dir)
	m.width, m.height = 80, 24

	plain := m.frameRows([]string{"one", "two", "three"}, 10)
	if got := stripANSI(plain[0]); got != "┌────────┐" {
		t.Fatalf("top border = %q", got)
	}
	if got := stripANSI(plain[1]); got != "│two     │" {
		t.Fatalf("framed row = %q", got)
	}
	if got := stripANSI(plain[2]); got != "└────────┘" {
		t.Fatalf("bottom border = %q", got)
	}
	if wide := m.frameRows([]string{"x-1", "x-2", "x-3", "x-4", "x-5"}, 10); len(wide) != 5 {
		t.Fatalf("framing must keep the row count, got %d", len(wide))
	}

	m.treeVisible = true
	m.termOpen = true
	m.Chat = true
	v := m.View()
	rows := strings.Split(v.Content, "\n")
	if !strings.Contains(v.Content, "┌") || !strings.Contains(v.Content, "│") {
		t.Fatal("chat mode must frame the panels")
	}
	if n := strings.Count(stripANSI(rows[0]), "┌"); n != 0 {
		t.Fatal("the tab-bar row stays blank in chat mode")
	}
	// The editor mode keeps the bare divider over the terminal.
	m.Chat = false
	v = m.View()
	if strings.Contains(v.Content, "┌") {
		t.Fatal("the editor keeps its own look without the frames")
	}
	_ = rows
}

// TestGitHintLineHiddenInChatMode pins the removal of the git command/hint
// line in the chat workspace: the panel works without it, and the freed row
// goes back to the main area.
func TestGitHintLineHiddenInChatMode(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.width, m.height = 80, 24
	m.gitOpen = true
	m.gitFocus = true
	m.gitMode = gitModeStatus

	if m.contextBottomRow() == "" {
		t.Fatal("the editor keeps the git status line")
	}

	m.Chat = true
	if m.contextBottomRow() != "" {
		t.Fatalf("chat mode must drop the git hint line, got %q", stripANSI(m.contextBottomRow()))
	}
}

// TestGitPanelKeepsTheFrameFull pins the geometry: dropping the git hint
// line in chat mode must give its row back to the main area, not leave the
// status bar floating a row above the bottom edge.
func TestGitPanelKeepsTheFrameFull(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.width, m.height = 80, 24
	m.Chat = true
	m.gitOpen = true
	m.gitFocus = true
	m.gitMode = gitModeStatus
	m.refreshGitFiles()

	rows := strings.Split(m.View().Content, "\n")
	if len(rows) != m.height {
		t.Fatalf("frame has %d rows, want %d", len(rows), m.height)
	}
	if got := stripANSI(rows[len(rows)-1]); !strings.Contains(got, "project:") {
		t.Fatalf("last row = %q, want the status bar", got)
	}
}
