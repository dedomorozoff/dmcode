package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/editor/i18n"
)

func TestHelpToggle(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if m.helpOpen {
		t.Fatal("help must start closed")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	if !m.helpOpen {
		t.Fatal("f1 must open help")
	}
	v := m.View()
	// Only the top window is visible on a 24-row terminal; "Ctrl+W" sits below
	// the fold and is checked after scrolling in TestHelpScrolls. The way back
	// to the chat is the first entry, so it is always on screen.
	for _, want := range []string{"dmcode — keys", "Ctrl+S", "Ctrl+Q", "Ctrl+F", "Ctrl+H", "Ctrl+O"} {
		if !strings.Contains(v.Content, want) {
			t.Fatalf("help view missing %q", want)
		}
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.helpOpen {
		t.Fatal("esc must close help")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	if m.helpOpen {
		t.Fatal("f1 again must close help")
	}
}

func TestHelpSwallowsTyping(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	typeStr(m, "hello world")
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatalf("typing while help open must not edit buffer, got %q", m.tabs[0].buf.Text())
	}
}

func TestEmbeddedCtrlETogglesToTheChat(t *testing.T) {
	// Standalone: ctrl+e keeps toggling help, next to F1.
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if !m.helpOpen {
		t.Fatal("standalone ctrl+e must open help")
	}

	// Embedded: ctrl+e asks the host to flip back to the chat, and the help
	// panel folds on the way out instead of resurfacing behind the transcript.
	e := New()
	e.width, e.height = 80, 24
	e.Embed = true
	e = press(e, tea.KeyPressMsg{Code: tea.KeyF1})
	if !e.helpOpen {
		t.Fatal("setup: f1 must open help")
	}
	nm, cmd := e.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if msg, ok := cmd().(ToggleEditorMsg); !ok {
		t.Fatalf("embedded ctrl+e yielded %T, want ToggleEditorMsg", msg)
	}
	em, ok := nm.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want editor.Model", nm)
	}
	if em.helpOpen {
		t.Fatal("embedded ctrl+e must fold the help on the way out")
	}
}

func TestStatusBarShowsHint(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if v := m.View(); !strings.Contains(v.Content, "F1 help") {
		t.Fatal("status bar must hint F1 help by default")
	}
}

func TestNulByteIsIgnored(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Text: string('\x00')})
	if m.helpOpen {
		t.Fatal("bare NUL (some stacks send it for bare Ctrl) must not toggle help")
	}
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatal("NUL must not insert into buffer")
	}
}

func TestHelpRussianLocale(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.cfg.UI.Lang = "ru"
	m.tr = i18n.New(i18n.Resolve("ru"))
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	v := m.View()
	for _, want := range []string{"клавиши", "сохранить активную вкладку", "быстрый поиск файлов"} {
		if !strings.Contains(v.Content, want) {
			t.Fatalf("ru help view missing %q", want)
		}
	}
}

// TestHelpEntriesAreTranslatedAndUnique pins the two ways help quietly rots:
// an i18n key that was never added renders as the key itself, and a row pasted
// twice shows up as a duplicate nobody notices. Both are invisible in a diff of
// the help panel and obvious to a user reading it.
func TestHelpEntriesAreTranslatedAndUnique(t *testing.T) {
	m := New()
	seen := make(map[string]bool, len(helpEntries))
	for _, e := range helpEntries {
		if e.keys == "" && e.desc == "" {
			continue // a section break
		}
		if e.keys == "" || e.desc == "" {
			t.Errorf("half-empty help entry %+v", e)
			continue
		}
		if seen[e.desc] {
			t.Errorf("duplicate help entry %q (%s)", e.desc, e.keys)
		}
		seen[e.desc] = true
		// The key must exist in the English catalog; an unknown one renders as
		// the raw key, which is how a help entry ends up reading "help.foo".
		if got := m.t(e.desc); got == e.desc {
			t.Errorf("help key %q is not translated", e.desc)
		}
	}
}

// TestHelpSaysHowToLeaveTheEditor: the editor is a mode of someone else's
// screen, so the way out has to be in the help, and above the fold — on a short
// terminal the lower half of the list is only there after a scroll.
func TestHelpSaysHowToLeaveTheEditor(t *testing.T) {
	// Near the top: only Ctrl+S may precede it, since it is the key a user
	// presses first and it needs no explanation.
	idx := -1
	for i, e := range helpEntries {
		if e.desc == "help.to_chat" {
			idx = i
			break
		}
	}
	if idx < 0 || idx > 2 {
		t.Fatalf("the way back to the chat is at row %d of the help, want it in the first three", idx)
	}
	m := New()
	m.width, m.height = 80, 24
	m.helpOpen = true
	if v := stripANSI(m.View().Content); !strings.Contains(v, "Ctrl+Q") {
		t.Error("the help must show the way back to the chat without scrolling")
	}
}

func TestHelpScrolls(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	max := m.helpMaxScroll()
	if max == 0 {
		t.Fatal("help must overflow a 24-row terminal")
	}

	m = press(m, tea.KeyPressMsg{Text: "j"})
	if m.helpScroll != 1 {
		t.Fatalf("j must scroll down one row, offset=%d", m.helpScroll)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.helpScroll != 2 {
		t.Fatalf("pgdn must scroll, offset=%d", m.helpScroll)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.helpScroll != 1 {
		t.Fatalf("pgup must scroll up, offset=%d", m.helpScroll)
	}
	m = press(m, tea.KeyPressMsg{Text: "k"})
	if m.helpScroll != 0 {
		t.Fatalf("k must scroll up to top, offset=%d", m.helpScroll)
	}

	// G jumps to the end; further scrolling clamps there. The bottom entries
	// ("Ctrl+W / Ctrl+X" close tab) become visible at the end of the list.
	m = press(m, tea.KeyPressMsg{Text: "G"})
	if m.helpScroll != max {
		t.Fatalf("G must jump to the end, offset=%d max=%d", m.helpScroll, max)
	}
	if v := m.View(); !strings.Contains(v.Content, "Ctrl+W") {
		t.Fatal("bottom of help must show after scrolling to the end")
	}
	m = press(m, tea.KeyPressMsg{Text: "j"})
	if m.helpScroll != max {
		t.Fatalf("j past the end must clamp, offset=%d", m.helpScroll)
	}

	// The mouse wheel scrolls the panel too (routed through Update like the
	// real event flow).
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 40, Y: 5})
	m = next.(Model)
	if m.helpScroll != max-1 {
		t.Fatalf("wheel up must scroll back, offset=%d", m.helpScroll)
	}
	next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 40, Y: 5})
	m = next.(Model)
	if m.helpScroll != max {
		t.Fatalf("wheel down must restore the end, offset=%d", m.helpScroll)
	}

	// Esc closes and reopening starts at the top again.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	if m.helpScroll != 0 {
		t.Fatalf("reopening must reset scroll, offset=%d", m.helpScroll)
	}
}
