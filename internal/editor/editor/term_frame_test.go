package editor

import (
	"strings"
	"testing"
)

// TestDockedTerminalKeepsTheFrameFull pins the geometry with the terminal up:
// the frame is exactly the terminal's height and the status bar is its last
// row. In chat mode the terminal's leading divider is framed rather than
// dropped — termExtraRows reserves that row and every mouse coordinate counts
// from it, so leaving it out shortens the frame by one row and lifts the
// status bar off the bottom edge.
func TestDockedTerminalKeepsTheFrameFull(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {120, 40}, {100, 20}} {
			m := New(t.TempDir())
			m.width, m.height = size[0], size[1]
			m.termOpen = true
			m.Chat = chat

			rows := strings.Split(m.View().Content, "\n")
			if len(rows) != m.height {
				t.Errorf("chat=%v %dx%d: frame is %d rows, want %d",
					chat, size[0], size[1], len(rows), m.height)
			}
			// statusBarRow is derived from m.height alone; the rendered frame
			// has to agree with it, which is the whole bug.
			if stripANSI(rows[len(rows)-1]) == "" {
				t.Errorf("chat=%v %dx%d: the last row is blank, so the status bar is not on the bottom edge",
					chat, size[0], size[1])
			}
			if start := m.termStartRow(); start >= m.statusBarRow() {
				t.Errorf("chat=%v %dx%d: terminal starts at row %d, at or below the status bar row %d",
					chat, size[0], size[1], start, m.statusBarRow())
			}
		}
	}
}
