package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeClipboard swaps the clipboard indirections for the duration of the test.
//
// text is what a *read* returns — the clipboard ctrl+v will see. The returned
// pointer is what a *write* records, empty until something is copied, so a test
// can assert the system side as well as m.clipboard.
//
// Without this a test of copy, cut or paste writes its own fixture text —
// "hello", "alpha" — into the one clipboard the user has, which is why running
// the suite used to leave that text behind. ctrl+v has the mirror problem: it
// reads the real clipboard, so what the buffer received depended on whatever
// the user had copied last. The real functions are restored on cleanup, so
// tests stay independent of each other.
func fakeClipboard(t *testing.T, text string) *string {
	t.Helper()
	oldWrite, oldRead := writeClipboardText, readClipboardText
	t.Cleanup(func() { writeClipboardText, readClipboardText = oldWrite, oldRead })

	written := ""
	writeClipboardText = func(s string) error {
		written = s
		return nil
	}
	readClipboardText = func() (string, error) { return text, nil }
	return &written
}

// TestClipboardTestsNeverTouchTheRealClipboard is the guard on the guard: the
// copy and cut paths must go through the indirections, not call clipboard
// directly. A direct call compiles, passes every test and quietly overwrites
// what the user copied — which is how "hello" kept reappearing in the
// clipboard after a `go test ./...`.
func TestClipboardTestsNeverTouchTheRealClipboard(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "guard.txt", "alpha\n")
	written := fakeClipboard(t, "")

	m := New(f)
	m.width, m.height = 80, 24
	m.cur().buf.StartSelection()
	m.cur().buf.LineEndWithSelect()
	m = press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	if m.clipboard != "alpha" {
		t.Fatalf("internal clipboard = %q, want %q", m.clipboard, "alpha")
	}
	if *written != "alpha" {
		t.Fatalf("the copy never reached the write seam (recorded %q) — something is calling clipboard.WriteAll directly", *written)
	}
}
