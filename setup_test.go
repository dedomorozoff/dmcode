package main

import (
	"os"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// setupKeys drives the model the way the real event loop does, through the
// tea.Model interface, so these tests exercise the same dispatch path a
// terminal takes rather than calling wizard internals directly.
type setupKeys struct{ m *uiModel }

func (d setupKeys) send(msg tea.KeyPressMsg) setupKeys {
	var model tea.Model = d.m
	model, _ = model.Update(msg)
	return d
}

func (d setupKeys) type_(text string) setupKeys {
	for _, r := range text {
		d = d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return d
}

func (d setupKeys) enter() setupKeys { return d.send(tea.KeyPressMsg{Code: tea.KeyEnter}) }
func (d setupKeys) down() setupKeys  { return d.send(tea.KeyPressMsg{Code: tea.KeyDown}) }

// newSetupModel builds the model exactly as main does. A hand-rolled uiModel
// will not do: textinput needs a real cursor or Focus panics and typing is
// dropped. The window size matters too, because the overlay's inner width is
// what the key mask is measured against.
func newSetupModel(t *testing.T) *uiModel {
	t.Helper()
	m := initialModel(nil, nil, provider{label: "old", model: "old-model", api: apiChat}, nil, nil)
	m.history = nil
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	return m
}

// inTempDir runs the body in a scratch directory, since /setup writes .env into
// the working directory and a test must never touch the real one.
func inTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSetupAppliesKeylessProviderEndToEnd types /setup, picks the free option
// and presses enter, then checks what actually landed on disk and in the
// running model. This is the path a user with no API key at all takes.
func TestSetupAppliesKeylessProviderEndToEnd(t *testing.T) {
	dir := inTempDir(t)
	// A real key the user already had must survive the switch to a free one.
	if err := os.WriteFile(".env", []byte("OPENCODE_API_KEY=real-secret\n# note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newSetupModel(t)
	d := setupKeys{m}.type_("/setup").enter()
	if !m.setup.open || m.setup.stage != setupPick {
		t.Fatalf("/setup did not open: open=%v stage=%d", m.setup.open, m.setup.stage)
	}

	d = d.enter() // the first option is keyless, so this applies it at once
	if m.setup.open {
		t.Errorf("wizard stayed open after a keyless choice: stage=%d", m.setup.stage)
	}

	data, err := os.ReadFile(".env")
	if err != nil {
		t.Fatalf(".env was not written: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"OPENCODE_API_KEY=real-secret", // the paid key survives
		"# note",                       // and so do the user's comments
		"OPENAI_BASE_URL=https://text.pollinations.ai/openai",
		"DMCODE_MODEL=openai-fast",
		"DMCODE_API=chat",
	} {
		if !strings.Contains(text, want) {
			t.Errorf(".env is missing %q:\n%s", want, text)
		}
	}
	// The whole point of keyless: no placeholder may be invented for the user.
	if strings.Contains(text, "OPENAI_API_KEY") {
		t.Errorf("a key was written for a keyless provider:\n%s", text)
	}

	// The change has to take effect now, not after a restart.
	want := setupOptions()[0]
	if m.prov.baseURL != want.baseURL || m.prov.model != want.model || m.prov.api != want.api {
		t.Errorf("running provider is %q/%q/%v, want %q/%q/%v",
			m.prov.baseURL, m.prov.model, m.prov.api, want.baseURL, want.model, want.api)
	}
	if os.Getenv("OPENAI_BASE_URL") != want.baseURL {
		t.Error("the new endpoint was not exported to the process")
	}

	// .env holds a secret, so it must not become world-readable. Windows
	// reports 0666 for every writable file and keeps permissions in an ACL, so
	// there is nothing for a mode check to see there.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dir + "/.env")
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf(".env has mode %o, want 600", perm)
		}
	}
}

// TestSetupKeyStageRejectsEmptyKey covers the keyed providers: an empty key must
// leave the wizard open instead of writing a provider that cannot authenticate.
func TestSetupKeyStageRejectsEmptyKey(t *testing.T) {
	inTempDir(t)
	m := newSetupModel(t)
	d := setupKeys{m}.type_("/setup").enter().down().down().enter() // OpenRouter
	if m.setup.stage != setupKey {
		t.Fatalf("a keyed provider skipped the key stage (stage=%d)", m.setup.stage)
	}

	d = d.enter() // submit nothing
	if !m.setup.open {
		t.Fatal("an empty key was accepted")
	}
	if _, err := os.Stat(".env"); err == nil {
		t.Error("an empty key still wrote .env")
	}

	d = d.type_("sk-test-123").enter()
	if m.setup.open {
		t.Error("wizard stayed open after a valid key")
	}
	data, _ := os.ReadFile(".env")
	if !strings.Contains(string(data), "OPENAI_API_KEY=sk-test-123") {
		t.Errorf("the entered key was not written:\n%s", data)
	}
}

// TestSetupKeyIsNeverRendered is a security test: the prompt says the input is
// hidden, so the overlay must not contain the secret on a screen share, and the
// transcript must not keep it either.
func TestSetupKeyIsNeverRendered(t *testing.T) {
	inTempDir(t)
	m := newSetupModel(t)
	d := setupKeys{m}.type_("/setup").enter().down().down().enter()
	if m.setup.stage != setupKey {
		t.Fatalf("expected the key stage, got %d", m.setup.stage)
	}
	const secret = "sk-super-secret"
	d = d.type_(secret)

	view := m.setupBox()
	if strings.Contains(view, secret) {
		t.Errorf("the key is visible in the overlay:\n%s", view)
	}
	if !strings.Contains(view, "•") {
		t.Errorf("nothing is shown in place of the key:\n%s", view)
	}

	// Now apply it and look at everything the user can scroll back through.
	d = d.enter()
	for _, l := range m.history {
		if strings.Contains(l.text, secret) {
			t.Errorf("the key leaked into the transcript: %q", l.text)
		}
	}
	// The overlay is gone, so the box is not a place to look any more either.
	if strings.Contains(m.View().Content, secret) {
		t.Error("the key is still on screen after the wizard closed")
	}
}

// Every option has to be readable in the overlay at the sizes people actually
// use. A long label that gets cut off is an option that does not exist.
func TestSetupOverlayShowsEveryOptionAndFits(t *testing.T) {
	m := initialModel(nil, nil, provider{label: "old", model: "old-model", api: apiChat}, nil, nil)
	m.history = nil

	for _, size := range [][2]int{{100, 34}, {80, 24}, {72, 22}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		setupKeys{m}.type_("/setup").enter()
		view := m.View().Content

		for _, o := range setupOptions() {
			// Labels wrap, so check the first word that cannot be split.
			head := strings.Fields(o.label)[0]
			if !strings.Contains(view, head) {
				t.Errorf("%dx%d: %q is missing from the overlay:\n%s", size[0], size[1], head, view)
			}
		}
		for i, l := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
			if w := ansi.StringWidth(l); w > m.width {
				t.Errorf("%dx%d: line %d is %d wide, want at most %d", size[0], size[1], i, w, m.width)
			}
		}
		if rows := strings.Count(view, "\n") + 1; rows > m.height {
			t.Errorf("%dx%d: the overlay is %d rows, want at most %d", size[0], size[1], rows, m.height)
		}
		m.setup.reset()
	}
}

// maskedKey must stay inside the panel no matter how much gets pasted.
func TestMaskedKeyFitsPanel(t *testing.T) {
	if got := maskedKey("", 20); got != "" {
		t.Errorf("an empty key rendered %q", got)
	}
	if got := maskedKey("abc", 20); len([]rune(got)) != 3 {
		t.Errorf("mask width = %d, want 3", len([]rune(got)))
	}
	long := maskedKey(strings.Repeat("x", 500), 12)
	if len([]rune(long)) > 11 {
		t.Errorf("a long key produced a %d-wide mask, want at most 11", len([]rune(long)))
	}
}

// Escape must work from every stage, including a half-typed key: a user who
// picked the wrong provider cannot be trapped in a prompt.
func TestSetupEscapeLeavesEveryStage(t *testing.T) {
	inTempDir(t)
	m := newSetupModel(t)
	d := setupKeys{m}.type_("/setup").enter().down().down().enter()
	if m.setup.stage != setupKey {
		t.Fatalf("expected the key stage, got %d", m.setup.stage)
	}
	d = d.type_("half-typed").send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.setup.open {
		t.Error("esc did not leave the key stage")
	}
	if _, err := os.Stat(".env"); err == nil {
		t.Error("escaping still wrote .env")
	}
}
