package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hinshun/vt10x"
)

// TestTermSelectionCopy pins Shift+drag selection over the terminal output.
func TestTermSelectionCopy(t *testing.T) {
	m := Model{}
	m.termRows = []terminalRow{
		{cells: glyphRow("first line")},
		{cells: glyphRow("second line")},
	}
	m.startTermSelection(0, 6)
	m.extendTermSelection(1, 6)
	if got, want := m.termSelectionText(), "line\nsecond"; got != want {
		t.Fatalf("termSelectionText() = %q, want %q", got, want)
	}
}

// glyphRow builds terminal cells from a plain string.
func glyphRow(s string) []vt10x.Glyph {
	cells := make([]vt10x.Glyph, 0, len(s))
	for _, r := range s {
		cells = append(cells, vt10x.Glyph{Char: r})
	}
	return cells
}

// TestShiftClickSelectsInTerminal verifies Shift+click starts a terminal
// selection instead of forwarding the mouse event to the inner application.
func TestShiftClickSelectsInTerminal(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.termOpen = true
	m.termFocus = true

	m.handleMouseClick(tea.MouseClickMsg{X: 4, Y: m.termStartRow() + 2, Button: tea.MouseLeft, Mod: tea.ModShift})
	if !m.termSelActive || !m.dragTerm {
		t.Fatalf("shift+click must start a terminal selection")
	}
}
