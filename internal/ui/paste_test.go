package ui

import (
	"testing"
)

// Pasting text into the prompt is the one paste that has to work in every
// session, and it is the one that broke: Init builds the editor workspace on
// the very first frame, so every real session has m.ed != nil, and Update's
// editor block forwards tea.PasteMsg to the editor before the chat's own
// PasteMsg case and the textinput behind it are ever reached. Host.Paste then
// answered only the picture question — on a text-only clipboard
// clipimg.Read returns ErrNoImage, the message is nil, the command is nil — so
// pasting three lines into the prompt did nothing, silently.
//
// Every other paste test here builds its model by hand, which leaves m.ed nil
// and takes the bare-chat route the tests were written against. These two build
// the workspace the way Init does, so they fail if the forwarding is ever
// short-circuited again.

// TestPasteIntoThePromptReachesTheTextinput: the terminal's own paste, which is
// the route on every terminal that binds ctrl+v itself.
func TestPasteIntoThePromptReachesTheTextinput(t *testing.T) {
	m := pastedModel(t)

	runPaste(t, m, "hello\nworld")

	// The prompt is one line, so the textinput collapses the newlines. What
	// matters is that the words arrived at all.
	if got, want := m.input.Value(), "hello world"; got != want {
		t.Errorf("input = %q, want %q — the paste never reached the prompt", got, want)
	}
}

// TestCtrlVPastesTextWithTheWorkspaceBuilt: the keystroke route, which reads
// the clipboard itself and answers with a PasteMsg. It walked into the same
// dead end, so it is pinned separately — the two routes fail for the same
// reason and a fix to one is not a fix to the other.
func TestCtrlVPastesTextWithTheWorkspaceBuilt(t *testing.T) {
	m := pastedModel(t)

	old := readClipboardText
	readClipboardText = func() (string, error) { return "copied line", nil }
	t.Cleanup(func() { readClipboardText = old })

	if msg := pressCtrlV(t, m); msg != nil {
		m.Update(msg)
	}

	if got, want := m.input.Value(), "copied line"; got != want {
		t.Errorf("input = %q, want %q", got, want)
	}
}

// TestAnOverlayStillGetsThePaste: the half that worked before, kept so the fix
// cannot be paid for with the overlay route.
func TestAnOverlayStillGetsThePaste(t *testing.T) {
	m := pastedModel(t)
	m.setup = setupState{open: true, stage: setupKey}

	runPaste(t, m, "sk-pasted")

	if m.setup.buf != "sk-pasted" {
		t.Errorf("setup.buf = %q, want %q — the paste missed the overlay", m.setup.buf, "sk-pasted")
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("input = %q, want empty — the overlay's text leaked into the prompt", got)
	}
}

// pastedModel is a session as Init leaves it: the editor workspace is built,
// the chat is on top of it, and the picture-capable wire is selected.
func pastedModel(t *testing.T) *uiModel {
	t.Helper()
	m := imageModel(t)
	m.workDir = editorWorkDir(t)
	if cmd := m.ensureEditor(); cmd != nil {
		m.Update(cmd())
	}
	if m.ed == nil {
		t.Fatal("ensureEditor must build the workspace, or this test proves nothing")
	}
	if !m.ed.Chat {
		t.Fatal("a fresh session must be in chat mode; a paste in editor mode is the editor's")
	}
	return m
}
