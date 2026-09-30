package ui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// setupKeys drives the model the way the real event loop does, through the
// tea.Model interface, so these tests exercise the same dispatch path a
// terminal takes rather than calling wizard internals directly.
type setupKeys struct {
	m   *uiModel
	cmd tea.Cmd
}

func (d setupKeys) send(msg tea.KeyPressMsg) setupKeys {
	var model tea.Model = d.m
	model, d.cmd = model.Update(msg)
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
func (d setupKeys) up() setupKeys    { return d.send(tea.KeyPressMsg{Code: tea.KeyUp}) }

// walkTo drives the cursor from the top of the wizard list down to index i, the
// way a user arrows through it. Going via the top rather than sending i+1 downs
// keeps the test honest if the list order ever changes: it asserts the row that
// is reached is the one the option list says sits at that index.
func (d setupKeys) walkTo(i int) setupKeys {
	for d.m.setup.selected > 0 {
		d = d.up()
	}
	for d.m.setup.selected < i {
		d = d.down()
	}
	return d
}

// newSetupModel builds the model exactly as main does. A hand-rolled uiModel
// will not do: textinput needs a real cursor or Focus panics and typing is
// dropped. The window size matters too, because the overlay's inner width is
// what the key mask is measured against.
func newSetupModel(t *testing.T) *uiModel {
	t.Helper()
	m := InitialModel(nil, nil, config.Provider{Label: "old", Model: "old-model", API: config.APIChat}, nil, nil, nil)
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
	d := setupKeys{m: m}.type_("/setup").enter()
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
	want := config.SetupOptions()[0]
	if m.prov.BaseURL != want.BaseURL || m.prov.Model != want.Model || m.prov.API != want.API {
		t.Errorf("running provider is %q/%q/%v, want %q/%q/%v",
			m.prov.BaseURL, m.prov.Model, m.prov.API, want.BaseURL, want.Model, want.API)
	}
	if os.Getenv("OPENAI_BASE_URL") != want.BaseURL {
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

// TestEverySetupOptionIsReachableAndPersists walks the whole wizard list with the
// arrow keys and checks that each entry can actually be selected and written to
// .env. A provider that exists in config.SetupOptions but cannot be reached from
// the keyboard is listed in /setup and unusable, which is the one failure this
// guards: the option list grew by six entries in one go, and a row pushed past
// the bottom of a short terminal would be exactly that bug, silently.
//
// The clean verdict is fed in directly rather than running the probe, so no
// request leaves the machine.
func TestEverySetupOptionIsReachableAndPersists(t *testing.T) {
	opts := config.SetupOptions()
	for i, want := range opts {
		t.Run(strings.Fields(want.Label)[0], func(t *testing.T) {
			dir := inTempDir(t)
			m := newSetupModel(t)
			d := setupKeys{m: m}.type_("/setup").enter().walkTo(i)

			if m.setup.selected != i {
				t.Fatalf("arrowing to row %d landed on %d (%s)", i, m.setup.selected,
					opts[m.setup.selected].Label)
			}

			// The row has to be the option the list says sits there, and it has to
			// be on screen: the panel is height-capped and reports how many rows it
			// hid, so a list that outgrows a short terminal shows a "↓ more" marker
			// instead of its bottom entries. Checked while the wizard is still
			// open, because m.setup.opt is only filled in on enter and wiped again
			// as soon as the choice is committed.
			view := m.View().Content
			if !strings.Contains(view, strings.SplitN(want.Label, " (", 2)[0]) {
				t.Errorf("row %d (%s) is not drawn:\n%s", i, want.Label, view)
			}

			// Keyless options apply at once; keyed ones ask for a secret first.
			// A GGUF pick is keyless too, but it first asks for the file to run.
			if want.GGUF {
				d = d.enter().type_("models\\qwen.gguf").enter()
			} else if want.Keyless {
				d = d.enter()
			} else {
				d = d.enter().type_("sk-test-123").enter()
				if m.setup.stage != setupKey {
					t.Fatalf("choosing %q skipped the key stage (stage=%d)", want.Label, m.setup.stage)
				}
				if got := m.setup.opt.Label; got != want.Label {
					t.Fatalf("row %d holds %q, want %q", i, got, want.Label)
				}
				_, _ = m.Update(setupCheckMsg{
					vars: config.SetupVars(m.setup.opt, "sk-test-123"),
					opt:  m.setup.opt,
				})
			}
			if m.setup.open {
				t.Fatalf("wizard stayed open after choosing %q", want.Label)
			}

			// What the wizard claims to offer has to be what it wrote.
			data, err := os.ReadFile(filepath.Join(dir, ".env"))
			if err != nil {
				t.Fatalf("%q wrote no .env: %v", want.Label, err)
			}
			text := string(data)
			if want.BaseURL != "" && !strings.Contains(text, "OPENAI_BASE_URL="+want.BaseURL) {
				t.Errorf("%q: .env is missing its base URL:\n%s", want.Label, text)
			}
			if want.Model != "" && !strings.Contains(text, "DMCODE_MODEL="+want.Model) {
				t.Errorf("%q: .env is missing its model:\n%s", want.Label, text)
			}
			if want.Keyless && strings.Contains(text, "API_KEY") {
				t.Errorf("%q is keyless but wrote a key variable:\n%s", want.Label, text)
			}
			if !want.Keyless && want.EnvKey != "" && !strings.Contains(text, want.EnvKey+"=sk-test-123") {
				t.Errorf("%q: .env is missing %s:\n%s", want.Label, want.EnvKey, text)
			}
			if want.GGUF && !strings.Contains(text, "DMCODE_GGUF=models\\qwen.gguf") {
				t.Errorf("%q: .env is missing the .gguf path:\n%s", want.Label, text)
			}
			// The running session has to move too, not just the file on disk.
			if want.BaseURL != "" && m.prov.BaseURL != want.BaseURL {
				t.Errorf("%q: the live provider is %q, want %q", want.Label, m.prov.BaseURL, want.BaseURL)
			}
			if m.prov.Wire() != want.API {
				t.Errorf("%q: the live wire is %q, want %q", want.Label, m.prov.Wire(), want.API)
			}
		})
	}
}

// TestSetupKeyStageRejectsEmptyKey covers the keyed providers: an empty key must
// leave the wizard open instead of writing a provider that cannot authenticate.
func TestSetupKeyStageRejectsEmptyKey(t *testing.T) {
	inTempDir(t)
	m := newSetupModel(t)
	d := setupKeys{m: m}.type_("/setup").enter().down().down().enter() // OpenRouter
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
	// Entering a key now starts a network check rather than a write, so the
	// provider is only saved once the check comes back clean. The command is not
	// executed here: it would hit openrouter.ai for real. Instead the clean
	// verdict is fed in directly, which is what the loop would deliver.
	_, _ = m.Update(setupCheckMsg{
		vars: config.SetupVars(m.setup.opt, "sk-test-123"),
		opt:  m.setup.opt,
	})
	if m.setup.open {
		t.Error("wizard stayed open after a valid key")
	}
	data, _ := os.ReadFile(".env")
	if !strings.Contains(string(data), "OPENAI_API_KEY=sk-test-123") {
		t.Errorf("the entered key was not written:\n%s", data)
	}
}

// A keyed provider must be verified before .env is touched, so a wrong key
// cannot replace a working one.
func TestSetupVerifiesKeyBeforeWriting(t *testing.T) {
	inTempDir(t)
	existing := "OPENAI_BASE_URL=https://opencode.ai/zen/v1\nOPENAI_API_KEY=oc_sk_working\n"
	if err := os.WriteFile(".env", []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newSetupModel(t)
	setupKeys{m: m}.type_("/setup").enter().down().down().enter().type_("oc_sk_for_openrouter").enter()

	// The rejection arrives: nothing may have been written yet.
	_, _ = m.Update(setupCheckMsg{
		vars: config.SetupVars(m.setup.opt, "oc_sk_for_openrouter"),
		opt:  m.setup.opt,
		err:  errors.New("401 Unauthorized: Missing Authentication header"),
	})

	data, err := os.ReadFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Errorf("a rejected key overwrote the working configuration:\n%s", data)
	}
	if !m.setup.open {
		t.Error("the wizard closed on a rejected key instead of offering a retry")
	}
}

// TestSetupKeyIsNeverRendered is a security test: the prompt says the input is
// hidden, so the overlay must not contain the secret on a screen share, and the
// transcript must not keep it either.
func TestSetupKeyIsNeverRendered(t *testing.T) {
	inTempDir(t)
	m := newSetupModel(t)
	d := setupKeys{m: m}.type_("/setup").enter().down().down().enter()
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
	m := InitialModel(nil, nil, config.Provider{Label: "old", Model: "old-model", API: config.APIChat}, nil, nil, nil)
	m.history = nil

	for _, size := range [][2]int{{100, 34}, {80, 24}, {72, 22}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		setupKeys{m: m}.type_("/setup").enter()
		view := m.View().Content

		for _, o := range config.SetupOptions() {
			// Labels wrap, so check the first word that cannot be split.
			head := strings.Fields(o.Label)[0]
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
	d := setupKeys{m: m}.type_("/setup").enter().down().down().enter()
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
