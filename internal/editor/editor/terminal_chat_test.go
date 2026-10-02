package editor

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestChatModeTerminalWorks pins the chat-mode terminal end to end: alt+t
// opens a live PTY while the workspace is in chat mode, a focused panel
// takes the typing, and the output lands in the rows the frame renders.
func TestChatModeTerminalWorks(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.width, m.height = 80, 24
	m.Chat = true

	next, cmd := m.Update(altT())
	m = next.(Model)
	defer m.killTerminal()
	if !m.termOpen || m.termSession == nil || cmd == nil {
		t.Fatal("alt+t must open a live PTY in chat mode")
	}

	// Focus the panel with a click inside the framed terminal.
	_, _ = m.Update(tea.MouseClickMsg{X: 10, Y: m.termStartRow() + 2, Button: tea.MouseLeft})
	if !m.termFocus {
		t.Fatal("a click inside the terminal must focus it in chat mode")
	}

	for _, key := range []tea.KeyPressMsg{{Text: "echo dmcode_term_ok"}, {Code: tea.KeyEnter}} {
		next, _ = m.Update(key)
		m = next.(Model)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-m.termCh:
			m.termRows = msg.rows
			if strings.Contains(strings.Join(rowTexts(m.termRows), "\n"), "dmcode_term_ok") {
				// And the framed view renders the output.
				v := m.View()
				if !strings.Contains(v.Content, "dmcode_term_ok") {
					t.Fatal("the framed terminal must render the output")
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out; rows=%v", rowTexts(m.termRows))
		}
	}
}

// TestOwnsMsg pins the lifecycle routing set: the editor consumes its own
// events in chat mode, and never the interactive keys the host owns.
func TestOwnsMsg(t *testing.T) {
	if !OwnsMsg(terminalOutputMsg{}) || !OwnsMsg(FileChangedMsg{}) || !OwnsMsg(lspDiagMsg{}) {
		t.Fatal("terminal, file and LSP events belong to the editor")
	}
	if OwnsMsg(tea.KeyPressMsg{}) || OwnsMsg(tea.MouseClickMsg{}) || OwnsMsg(tea.WindowSizeMsg{}) {
		t.Fatal("interactive keys and resizes must not be claimed")
	}
}
