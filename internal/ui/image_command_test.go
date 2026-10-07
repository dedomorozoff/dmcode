package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// /image from the prompt, the way a user reaches it.
//
// Every other image test calls attachImageCmd or takeImagePaths directly, which
// cannot see whether the command is still *reachable* — and it was reachable and
// useless at the same time. Both failures below were present before any of the
// work on the command list and the session door; they are here because nothing
// drove the command end to end.

// TestTheImageCommandAttachesFromThePrompt drives the whole path: the text goes
// in, enter goes in, and a pending attachment with a drawn preview comes out.
func TestTheImageCommandAttachesFromThePrompt(t *testing.T) {
	m := imageModel(t)
	p := writePNG(t, t.TempDir(), "shot.png", 64, 48)

	m.input.SetValue("/image " + p)
	enterKey(m)

	if len(m.pending) != 1 {
		t.Fatalf("/image attached %d pictures, want 1; status %q", len(m.pending), m.statusText)
	}
	if m.pending[0].Name != filepath.Base(p) {
		t.Errorf("attached %q, want %q", m.pending[0].Name, filepath.Base(p))
	}
	// The command attaches; it does not start a turn, and it does not eat the
	// prompt the user is still writing.
	if m.busy {
		t.Error("/image started a turn")
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		t.Errorf("the prompt was consumed: %q", m.input.Value())
	}
}

// TestAQuotedPathIsAPath covers the form the prompt itself uses: a terminal
// quotes a dropped path that contains a space, and a user copies that form back.
// The quotes are part of the gesture, not part of the name — a token still
// carrying them is not a path anything will resolve, and the error that comes back
// names the quote rather than the file.
func TestAQuotedPathIsAPath(t *testing.T) {
	m := imageModel(t)
	p := writePNG(t, t.TempDir(), "shot.png", 40, 40)

	m.input.SetValue(`/image "` + p + `"`)
	enterKey(m)

	if len(m.pending) != 1 {
		t.Fatalf("a quoted path attached %d pictures, want 1; status %q", len(m.pending), m.statusText)
	}
}

// TestTheImageCommandIsCompletedByTheListNotRun is the one that made the command
// look broken.
//
// Typing "/im" and pressing enter used to *run* /image with no path: a usage line
// appeared, the prompt was emptied, and the path the user typed next was sent to
// the model as a question about a filename. The list had just offered that row as
// the completion of what they typed, so the two answers disagreed about what enter
// meant.
func TestTheImageCommandIsCompletedByTheListNotRun(t *testing.T) {
	m := imageModel(t)
	p := writePNG(t, t.TempDir(), "shot.png", 40, 40)

	m.input.SetValue("/im")
	m.updateSuggest()
	if len(m.suggest) == 0 || m.suggest[0].text != "/image" {
		t.Fatalf("/im offers %v, want /image first", suggestRowsOf(m))
	}
	enterKey(m)

	// The row is in the prompt, with room for the argument, and nothing ran.
	if got := m.input.Value(); got != "/image " {
		t.Fatalf("after enter the prompt is %q, want %q — the argument has to go somewhere", got, "/image ")
	}
	if len(m.pending) != 0 {
		t.Fatalf("completing the command attached %d pictures, want none", len(m.pending))
	}
	if m.busy {
		t.Fatal("completing the command started a turn")
	}
	if m.statusText != "" {
		t.Errorf("completing the command said %q, want nothing", m.statusText)
	}

	// And the path typed after it attaches, which is the flow that used to fail.
	m.input.SetValue(m.input.Value() + p)
	enterKey(m)
	if len(m.pending) != 1 {
		t.Fatalf("the path after the completion attached %d pictures, want 1; status %q", len(m.pending), m.statusText)
	}
	if m.busy {
		t.Fatal("the path after the completion was sent as a prompt instead")
	}
}

// TestOnlyCommandsThatNeedAnArgumentAreCompleted: every other row keeps running on
// enter, which is what the list has always done. /proxy opens a dialog, /cd prints
// where you are and /new starts a session — completing those would take away the
// bare form that works.
func TestOnlyCommandsThatNeedAnArgumentAreCompleted(t *testing.T) {
	for _, tc := range []struct {
		typed     string
		wantIn    string // "" means the command should have run instead
		wantRunIn bool
	}{
		{typed: "/help", wantRunIn: true},
		{typed: "/proxy", wantRunIn: true},
		{typed: "/cd", wantRunIn: true},
		{typed: "/new", wantRunIn: true},
		{typed: "/mode", wantRunIn: true},
		{typed: "/todo", wantRunIn: true},
		{typed: "/resume", wantIn: "/resume "},
		{typed: "/image", wantIn: "/image "},
	} {
		m := imageModel(t)
		m.input.SetValue(tc.typed)
		m.updateSuggest()
		if len(m.suggest) == 0 {
			t.Fatalf("%q offers no rows to take", tc.typed)
		}
		m.suggestSel = 0
		enterKey(m)

		if tc.wantRunIn {
			if got := m.input.Value(); strings.TrimSpace(got) != "" && got != tc.typed {
				t.Errorf("%q: the prompt is %q after enter, want the command to have run", tc.typed, got)
			}
			continue
		}
		if got := m.input.Value(); got != tc.wantIn {
			t.Errorf("%q: the prompt is %q after enter, want %q", tc.typed, got, tc.wantIn)
		}
	}
}

// TestABareImageCommandExplainsItself: typed in full with no path there is nothing
// to attach, and the command must say so rather than looking like it did nothing.
func TestABareImageCommandExplainsItself(t *testing.T) {
	m := imageModel(t)
	m.input.SetValue("/image")
	// No list row this time: the argument is absent, so this is the command itself
	// rather than a completion of it.
	m.suggest = nil
	enterKey(m)

	if len(m.pending) != 0 {
		t.Fatalf("a bare /image attached %d pictures, want none", len(m.pending))
	}
	if !strings.Contains(m.statusText, "/image") {
		t.Errorf("status = %q, want the usage line naming the command", m.statusText)
	}
}

// TestTheImageCommandIsInTheList: the "/" list is how a command is found, so a
// command that works but is not offered is invisible — and its absence looks
// exactly like a broken command.
func TestTheImageCommandIsInTheList(t *testing.T) {
	m := imageModel(t)
	if got := suggestTexts(t, m, "/image"); len(got) == 0 || got[0] != "/image" {
		t.Errorf("typing /image offers %v, want /image first", got)
	}
	if got := suggestTexts(t, m, "/im"); len(got) == 0 || got[0] != "/image" {
		t.Errorf("typing /im offers %v, want /image first — it is the shorter of the two", got)
	}
}

func enterKey(m *uiModel) { m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) }

func suggestRowsOf(m *uiModel) []string {
	out := make([]string, 0, len(m.suggest))
	for _, s := range m.suggest {
		out = append(out, s.text)
	}
	return out
}
