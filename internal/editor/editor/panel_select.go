package editor

import (
	"strings"

	"github.com/atotto/clipboard"
)

// The system clipboard is reached through these two indirections rather than
// called directly, because it is a global, shared, destructive resource: there
// is exactly one of it, it belongs to the user, and a test that writes to it
// both leaves "hello"/"alpha" in the clipboard of whoever ran `go test` and
// races every other program on the machine for it. Every clipboard test swaps
// these instead, so the copy/cut/paste paths are driven by synthetic text and
// the real clipboard is left alone.
var (
	writeClipboardText = clipboard.WriteAll
	readClipboardText  = clipboard.ReadAll
)

// Text selection for the terminal output. It stores its content as fixed
// rows, so a selection is a (row, visible column) span; on mouse release the
// covered text goes to the system clipboard, the same behavior as selecting
// in a browser.

type selPos struct {
	row int
	col int
}

func lessSel(a, b selPos) bool {
	return a.row < b.row || (a.row == b.row && a.col < b.col)
}

// copyToClipboard stores text in the internal and system clipboards.
func (m *Model) copyToClipboard(text string) {
	if text == "" {
		return
	}
	m.clipboard = text
	if err := writeClipboardText(text); err != nil {
		m.msg = "clipboard: " + err.Error()
		return
	}
	m.msg = m.t("msg.copied")
}

func (m *Model) startTermSelection(row, col int) {
	m.termSelAnchor = selPos{row, col}
	m.termSelEnd = selPos{row, col}
	m.termSelActive = true
}

func (m *Model) extendTermSelection(row, col int) {
	m.termSelEnd = selPos{row, col}
}

// termSelRange returns the selected column range on a terminal row.
func (m Model) termSelRange(row, width int) (int, int, bool) {
	if !m.termSelActive || width < 0 {
		return 0, 0, false
	}
	a, b := m.termSelAnchor, m.termSelEnd
	if lessSel(b, a) {
		a, b = b, a
	}
	if row < a.row || row > b.row {
		return 0, 0, false
	}
	c0 := 0
	if row == a.row {
		c0 = a.col
	}
	c1 := width
	if row == b.row {
		c1 = b.col
	}
	if c0 < 0 {
		c0 = 0
	}
	if c1 > width {
		c1 = width
	}
	if c1 <= c0 {
		return 0, 0, false
	}
	return c0, c1, true
}

// termSelectionText collects the selected terminal cells, trimming the
// trailing spaces of every covered line.
func (m Model) termSelectionText() string {
	if !m.termSelActive {
		return ""
	}
	a, b := m.termSelAnchor, m.termSelEnd
	if lessSel(b, a) {
		a, b = b, a
	}
	var lines []string
	for i := a.row; i <= b.row && i < len(m.termRows); i++ {
		if i < 0 {
			continue
		}
		runes := []rune(terminalRowText(m.termRows[i].cells))
		c0, c1, ok := m.termSelRange(i, len(runes))
		if !ok {
			continue
		}
		lines = append(lines, string(runes[c0:c1]))
	}
	return strings.Join(lines, "\n")
}
