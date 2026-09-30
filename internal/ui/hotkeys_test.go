package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// ctrl builds a control-key press. A bare rune is not enough: the terminal
// sends ctrl+l as the control character, and a test that set Text to "ctrl+l"
// would exercise a key the model never produces.
func ctrl(letter rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: letter, Mod: tea.ModCtrl}
}

// press sends a key through the whole Update, which is the only way to be sure
// a binding is not shadowed by an overlay or swallowed by the input.
func press(t *testing.T, m *uiModel, k tea.KeyMsg) *uiModel {
	t.Helper()
	next, _ := m.Update(k)
	got, ok := next.(*uiModel)
	if !ok {
		t.Fatalf("Update turned the model into %T", next)
	}
	return got
}

// TestCtrlLClearsTheTranscript: ctrl+l is the terminal's own clear-screen, so
// it has to do what /clear does and not something adjacent to it.
func TestCtrlLClearsTheTranscript(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "привет")
	if len(m.history) == 0 {
		t.Fatal("the transcript is empty before the key; nothing to clear")
	}

	m = press(t, m, ctrl('l'))

	if len(m.history) != 0 {
		t.Errorf("ctrl+l left %d lines on screen: %+v", len(m.history), m.history)
	}
}

// TestCtrlLClearsTheScreenNotTheConversation is the distinction that matters.
// "Clear the screen" is about what is on it; wiping the stored conversation
// behind it would be /new's job, and conflating them loses a transcript the
// user meant to keep.
func TestCtrlLClearsTheScreenNotTheConversation(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "работа, которую надо сохранить")
	old := m.sessionID

	m = press(t, m, ctrl('l'))

	if m.sessionID != old {
		t.Errorf("ctrl+l changed the session id %q -> %q; it must not touch the conversation", old, m.sessionID)
	}
	tr, err := m.sessions.TranscriptOf(sessionApp, sessionUser, old, 10)
	if err != nil {
		t.Fatalf("ctrl+l destroyed the stored conversation: %v", err)
	}
	if len(tr.Turns) != 1 {
		t.Errorf("the stored conversation has %d turns after ctrl+l, want 1", len(tr.Turns))
	}
}

// TestCtrlLMatchesTheSlashCommand: two entry points to one behaviour have to
// stay one behaviour. The palette's copy had already drifted once, losing
// followVP.
func TestCtrlLMatchesTheSlashCommand(t *testing.T) {
	viaKey := newSessionModel(t)
	sendTurn(t, viaKey, "что-то")
	viaKey = press(t, viaKey, ctrl('l'))

	viaCommand := newSessionModel(t)
	sendTurn(t, viaCommand, "что-то")
	viaCommand.input.SetValue("/clear")
	viaCommand = press(t, viaCommand, key("enter"))

	if len(viaKey.history) != len(viaCommand.history) {
		t.Errorf("ctrl+l left %d lines, /clear left %d",
			len(viaKey.history), len(viaCommand.history))
	}
	if viaKey.stick != viaCommand.stick {
		t.Errorf("ctrl+l and /clear disagree about sticking to the bottom: %v vs %v",
			viaKey.stick, viaCommand.stick)
	}
	if viaKey.vp.YOffset() != viaCommand.vp.YOffset() {
		t.Errorf("ctrl+l scrolled to %d, /clear to %d",
			viaKey.vp.YOffset(), viaCommand.vp.YOffset())
	}
}

// TestCtrlNStartsANewSessionAndKeepsTheOldOne: the same one-way-door guarantee
// /new makes. ctrl+n is a bare key with no confirmation and no undo, so the
// old session has to survive it.
func TestCtrlNStartsANewSessionAndKeepsTheOldOne(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "старая работа")
	old := m.sessionID

	m = press(t, m, ctrl('n'))

	if m.sessionID == old {
		t.Fatal("ctrl+n did not start a new session")
	}
	tr, err := m.sessions.TranscriptOf(sessionApp, sessionUser, old, 10)
	if err != nil {
		t.Fatalf("ctrl+n destroyed the previous session: %v", err)
	}
	if len(tr.Turns) != 1 {
		t.Errorf("the previous session has %d turns, want 1", len(tr.Turns))
	}
}

// TestCtrlNIsRefusedMidTurn is the guard that matters most. The runner is
// mid-conversation on this session id; pointing it at a new one underneath
// would strand the turn on a session nothing is watching.
func TestCtrlNIsRefusedMidTurn(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "работа")
	old := m.sessionID
	m.busy = true

	m = press(t, m, ctrl('n'))

	if m.sessionID != old {
		t.Errorf("ctrl+n changed the session mid-turn: %q -> %q", old, m.sessionID)
	}
	if !m.busy {
		t.Error("ctrl+n reported the turn as finished; it must not touch the running turn")
	}
}

// TestCtrlLClearsWhileBusy: unlike /new, clearing the screen is harmless
// mid-turn — the conversation is untouched and the turn keeps running.
func TestCtrlLClearsWhileBusy(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "работа")
	m.busy = true

	m = press(t, m, ctrl('l'))

	if len(m.history) != 0 {
		t.Errorf("ctrl+l left %d lines while busy", len(m.history))
	}
	if !m.busy {
		t.Error("ctrl+l stopped the running turn; it is only a screen clear")
	}
}

// TestNewShortcutsDoNotFireUnderAnOverlay: an overlay owns the keyboard while
// it is open. A user reaching for ctrl+n with the session list up must not
// start a session behind it.
func TestNewShortcutsDoNotFireUnderAnOverlay(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(m *uiModel)
		busy func(m *uiModel) bool
	}{
		{"palette", func(m *uiModel) { m.palette = paletteState{open: true} },
			func(m *uiModel) bool { return m.palette.open }},
		{"picker", func(m *uiModel) { m.picker = modelPicker{open: true} },
			func(m *uiModel) bool { return m.picker.open }},
		{"sessions", func(m *uiModel) { m.sessionsList.open = true },
			func(m *uiModel) bool { return m.sessionsList.open }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newSessionModel(t)
			sendTurn(t, m, "работа")
			old := m.sessionID

			tc.open(m)
			m = press(t, m, ctrl('n'))

			if m.sessionID != old {
				t.Errorf("ctrl+n reached the model through the %s overlay: %q -> %q",
					tc.name, old, m.sessionID)
			}
			if !tc.busy(m) {
				t.Errorf("the %s overlay closed on ctrl+n, which it should have ignored", tc.name)
			}

			// Once the overlay is gone the same key has to work, or the test
			// above would pass simply because the binding does not exist.
			m = press(t, m, key("esc"))
			m = press(t, m, ctrl('n'))
			if m.sessionID == old {
				t.Errorf("ctrl+n does not work once the %s overlay is closed", tc.name)
			}
		})
	}
}

// TestNewShortcutsAreAdvertised: a binding nobody is told about is a binding
// nobody finds. The sidebar and the status bar are deliberately kept free of
// hotkeys — both have already overflowed on a narrow terminal — so the palette
// and /help are where these have to be.
func TestNewShortcutsAreAdvertised(t *testing.T) {
	m := newSessionModel(t)

	byName := map[string]string{}
	for _, c := range m.commands() {
		byName[c.name] = c.desc
	}
	if d := byName["clear"]; !strings.Contains(d, "ctrl+l") {
		t.Errorf("the palette does not mention ctrl+l for clear: %q", d)
	}
	if d := byName["new"]; !strings.Contains(d, "ctrl+n") {
		t.Errorf("the palette does not mention ctrl+n for new: %q", d)
	}

	m.input.SetValue("/help")
	m = press(t, m, key("enter"))
	for _, l := range m.history {
		if strings.Contains(l.text, "ctrl+l") && strings.Contains(l.text, "ctrl+n") {
			return
		}
	}
	t.Error("/help never mentions both new shortcuts")
}

// TestNewShortcutsAreTranslated keeps the two new labels from being the only
// English left in a Russian interface.
func TestNewShortcutsAreTranslated(t *testing.T) {
	restore := i18n.Current()
	t.Cleanup(func() { i18n.Set(restore) })
	i18n.Set(i18n.Russian)

	for _, s := range []string{
		"clear the screen (ctrl+l)",
		"start a new session, the old one is kept (ctrl+n)",
		"ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply · ctrl+l — clear · ctrl+n — new session",
	} {
		if got := i18n.T(s); got == s {
			t.Errorf("no Russian translation for %q", s)
		}
	}
}
