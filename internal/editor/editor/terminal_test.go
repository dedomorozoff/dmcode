package editor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/hinshun/vt10x"
)

func altT() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt} }

// testShell pins a shell the platform is guaranteed to carry. The tests must
// not inherit $SHELL: a sandbox or CI image can point it at a binary it does
// not have, and then "alt+t opens a terminal" would fail for a reason that has
// nothing to do with the editor.
func testShell(t *testing.T) {
	t.Helper()
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
	}
	t.Setenv("DMCODE_SHELL", shell)
}

// requireTerminalSession skips — rather than fails — when the platform will
// not give dmcode a pseudo-terminal at all, and fails when the toggle never
// asked for one. The only reason ensureShell leaves termSession nil is that
// ptyterm.Start failed, and it records that with terminalFailPrefix: a
// container without /dev/ptmx is not a broken alt+t, and what these tests
// assert is the toggle. ptyterm's own test draws the same line.
func requireTerminalSession(t *testing.T, m Model) {
	t.Helper()
	if m.termSession != nil {
		return
	}
	if reason, ok := strings.CutPrefix(m.msg, terminalFailPrefix); ok {
		t.Skipf("a live PTY is unavailable here: %s", reason)
	}
	t.Fatalf("the terminal did not open (msg=%q)", m.msg)
}

func TestTerminalShellSelection(t *testing.T) {
	m := New()
	t.Setenv("DMCODE_SHELL", "C:\\fake\\shell.exe")
	if got := m.shellCommand(); got != "C:\\fake\\shell.exe" {
		t.Fatalf("DMCODE_SHELL must win, got %q", got)
	}
	t.Setenv("DMCODE_SHELL", "")
	got := m.shellCommand()
	if got == "" || strings.Contains(got, "/bin/") && runtime.GOOS == "windows" {
		t.Fatalf("platform default shell must be usable, got %q", got)
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[32mgreen\x1b[0m plain"); got != "green plain" {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalScreenParsesANSI(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 3))
	_, _ = term.Write([]byte("one\x1b[2;3Htwo\x1b[31mred\x1b[0m"))
	rows := captureTerminalRows(term)
	if got := rows[0].text; got != "one" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := rows[1].text; got != "  twored" {
		t.Fatalf("cursor addressing failed: %q", got)
	}
}

func TestTerminalKeyEncoding(t *testing.T) {
	tests := []struct {
		msg  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, "\r"},
		{tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{tea.KeyPressMsg{Text: "я"}, "я"},
		{tea.KeyPressMsg{Text: "x", Mod: tea.ModAlt}, "\x1bx"},
	}
	for _, tt := range tests {
		if got := string(terminalKeyBytes(tt.msg)); got != tt.want {
			t.Fatalf("key %v = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestTerminalToggleAndCommand(t *testing.T) {
	m := New()
	testShell(t)
	m.width, m.height = 80, 24
	next, cmd := m.Update(altT())
	m = next.(Model)
	defer m.killTerminal()
	requireTerminalSession(t, m)
	if !m.termOpen || cmd == nil {
		t.Fatal("Alt+T must open a live PTY and hand back its read commands")
	}
	if h := m.viewHeight(); h >= 24 {
		t.Fatalf("editor did not shrink: %d", h)
	}
	for _, key := range []tea.KeyPressMsg{{Text: "echo dmed_term_ok"}, {Code: tea.KeyEnter}} {
		m = press(m, key)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-m.termCh:
			m.termRows = msg.rows
			if strings.Contains(strings.Join(rowTexts(m.termRows), "\n"), "dmed_term_ok") {
				return
			}
		case <-deadline:
			t.Fatalf("timed out; rows=%v", rowTexts(m.termRows))
		}
	}
}

func rowTexts(rows []terminalRow) []string {
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].text
	}
	return out
}

// TestTerminalStartFailureIsReported: a shell that cannot be started leaves
// the reason on the status line rather than a panel that silently does
// nothing. It is also the message the PTY-dependent tests read to skip
// themselves, so clearing it here would turn "this machine has no PTY" into a
// mystery failure there.
func TestTerminalStartFailureIsReported(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	t.Setenv("DMCODE_SHELL", filepath.Join(t.TempDir(), "no-such-shell"))
	next, cmd := m.Update(altT())
	m = next.(Model)
	defer m.killTerminal()

	if m.termSession != nil || m.termOpen || cmd != nil {
		t.Fatalf("a missing shell must not open a panel (open=%v session=%v cmd=%v)",
			m.termOpen, m.termSession != nil, cmd != nil)
	}
	if !strings.HasPrefix(m.msg, terminalFailPrefix) {
		t.Fatalf("msg = %q, want the reason prefixed with %q", m.msg, terminalFailPrefix)
	}
}

func TestTerminalPanelRows(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.termOpen = true
	rows := plainRows(strings.Join(m.terminalPanel(), "\n"))
	if len(rows) != m.termPanelHeight() {
		t.Fatalf("got %d rows", len(rows))
	}
}
