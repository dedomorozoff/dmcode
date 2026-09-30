package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// ggufOptionIndex is where the GGUF option sits in the wizard list.
func ggufOptionIndex() int {
	for i, o := range config.SetupOptions() {
		if o.GGUF {
			return i
		}
	}
	return -1
}

// setupTempModel builds a model in a scratch directory that holds a models
// subdirectory with one .gguf file and one file the browser must not offer.
func setupTempModel(t *testing.T) (*uiModel, string, string) {
	t.Helper()
	dir := inTempDir(t)
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	gguf := filepath.Join(models, "qwen.gguf")
	if err := os.WriteFile(gguf, []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "ignore.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newSetupModel(t)
	return m, dir, gguf
}

// TestSetupGGUFPickerSelectsAFile drives the dialog the way a user does:
// enter on the empty path prompt opens it, enter descends into a directory,
// enter on the .gguf file picks it and starts the setup.
func TestSetupGGUFPickerSelectsAFile(t *testing.T) {
	m, dir, ggufPath := setupTempModel(t)

	orig := launchGGUF
	launchGGUF = func() (config.Provider, error) {
		return config.Provider{BaseURL: "http://127.0.0.1:9/v1", Model: "qwen.gguf", API: config.APIChat, Label: "llama.cpp GGUF"}, nil
	}
	t.Cleanup(func() { launchGGUF = orig })

	d := setupKeys{m: m}.type_("/setup").enter().walkTo(ggufOptionIndex()).enter()
	if m.setup.stage != setupGGUF {
		t.Fatalf("stage = %d, want the gguf path prompt (%d)", m.setup.stage, setupGGUF)
	}
	d = d.enter() // empty path: the browser opens in the current directory
	if !m.setup.gguf.open {
		t.Fatal("enter on an empty path did not open the file browser")
	}
	view := m.View().Content
	if !strings.Contains(view, "models"+string(filepath.Separator)) {
		t.Errorf("the browser does not list the subdirectory:\n%s", view)
	}

	d = d.down().enter() // descend into models
	if m.setup.gguf.dir != filepath.Join(dir, "models") {
		t.Fatalf("the browser is at %q, want %q", m.setup.gguf.dir, filepath.Join(dir, "models"))
	}
	view = m.View().Content
	if !strings.Contains(view, "qwen.gguf") {
		t.Errorf("the browser does not list the model file:\n%s", view)
	}
	if strings.Contains(view, "ignore.txt") {
		t.Errorf("the browser offered a non-gguf file:\n%s", view)
	}

	d = d.down().enter() // pick the file
	if m.setup.open || m.setup.gguf.open {
		t.Fatalf("a pick did not close the dialog (wizard=%v browser=%v)", m.setup.open, m.setup.gguf.open)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf(".env was not written: %v", err)
	}
	if !strings.Contains(string(data), "DMCODE_GGUF="+ggufPath) {
		t.Errorf(".env is missing the picked path:\n%s", data)
	}

	if run := d.cmd; run == nil {
		t.Fatal("picking a file produced no launch command")
	} else {
		_, _ = m.Update(run())
	}
	if m.prov.Model != "qwen.gguf" {
		t.Errorf("the picked model did not go live: %+v", m.prov)
	}
}

// The browser's esc must back out to the path prompt, not close /setup, and
// whatever the user had typed must still be there.
func TestSetupGGUFPickerEscKeepsThePrompt(t *testing.T) {
	m, _, _ := setupTempModel(t)

	d := setupKeys{m: m}.type_("/setup").enter().walkTo(ggufOptionIndex()).enter().type_("some\\path")
	d = d.send(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if !m.setup.gguf.open {
		t.Fatal("ctrl+o did not open the file browser")
	}
	if !m.setup.open || m.setup.stage != setupGGUF {
		t.Fatal("the browser replaced the wizard instead of overlaying it")
	}
	d = d.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.setup.gguf.open {
		t.Error("esc did not close the file browser")
	}
	if !m.setup.open || m.setup.stage != setupGGUF {
		t.Errorf("esc tore down the wizard (open=%v stage=%d), want the path prompt back", m.setup.open, m.setup.stage)
	}
	if m.setup.buf != "some\\path" {
		t.Errorf("the typed path was lost: %q", m.setup.buf)
	}
}

// TestSetupGGUFWalksToThePathPrompt drives /setup to the GGUF option and
// checks the whole flow: the path prompt appears, the wizard closes once the
// path is entered, and .env gains DMCODE_GGUF without any key variable. The
// launcher is a stub, because a real model load has no place in a unit test.
func TestSetupGGUFWalksToThePathPrompt(t *testing.T) {
	dir := inTempDir(t)
	m := newSetupModel(t)

	orig := launchGGUF
	launchGGUF = func() (config.Provider, error) {
		return config.Provider{BaseURL: "http://127.0.0.1:9/v1", Model: "qwen.gguf", API: config.APIChat, Label: "llama.cpp GGUF"}, nil
	}
	t.Cleanup(func() { launchGGUF = orig })

	d := setupKeys{m: m}.type_("/setup").enter().walkTo(ggufOptionIndex()).enter()
	if m.setup.stage != setupGGUF {
		t.Fatalf("choosing the GGUF option went to stage %d, want the path prompt (%d)", m.setup.stage, setupGGUF)
	}
	view := m.View().Content
	if !strings.Contains(view, ".gguf") {
		t.Errorf("the path prompt does not mention .gguf:\n%s", view)
	}

	d = d.type_("models\\qwen.gguf").enter()
	if m.setup.open {
		t.Fatal("the wizard stayed open after the .gguf path was entered")
	}

	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf(".env was not written: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "DMCODE_GGUF=models\\qwen.gguf") {
		t.Errorf(".env is missing the .gguf path:\n%s", text)
	}
	if strings.Contains(text, "API_KEY") {
		t.Errorf("a key was written for the GGUF option:\n%s", text)
	}

	// The launch must not run on the event loop: startGGUFSetup returns it as
	// a tea.Cmd, which the running program executes on its own goroutine.
	run := d.cmd
	if run == nil {
		t.Fatal("starting a GGUF model produced no command")
	}
	_, _ = m.Update(run())
	if m.prov.Model != "qwen.gguf" || m.prov.BaseURL != "http://127.0.0.1:9/v1" {
		t.Errorf("a ready GGUF model did not go live: %+v", m.prov)
	}
	for _, l := range m.history {
		if strings.Contains(l.text, "qwen.gguf") {
			return
		}
	}
	t.Error("the transcript never announced the GGUF model")
}

// A failed launch keeps the saved configuration and says so, instead of
// leaving the user with a provider that does not exist and no explanation.
func TestSetupGGUFReportsAFailedLaunch(t *testing.T) {
	inTempDir(t)
	m := newSetupModel(t)

	orig := launchGGUF
	launchGGUF = func() (config.Provider, error) {
		return config.Provider{}, errors.New("llama-server not found")
	}
	t.Cleanup(func() { launchGGUF = orig })

	d := setupKeys{m: m}.type_("/setup").enter().walkTo(ggufOptionIndex()).enter().type_("models\\qwen.gguf").enter()
	if run := d.cmd; run != nil {
		m.Update(run())
	}
	if m.prov.Label == "llama.cpp GGUF" {
		t.Error("a failed launch still replaced the provider")
	}
	var saidFailure, saidKept bool
	for _, l := range m.history {
		if strings.Contains(l.text, "llama-server not found") {
			saidFailure = true
		}
		if strings.Contains(l.text, "DMCODE_GGUF") {
			saidKept = true
		}
	}
	if !saidFailure {
		t.Error("the transcript never explained the failure")
	}
	if !saidKept {
		t.Error("the failure does not say the configuration was kept")
	}
}
