package ui

// ctxpick_test.go — the restart offer after a turn outgrew a local model's
// context window: offered only for the typed failure against a GGUF provider,
// the dialog restarts with the highlighted size, and the size lands in the
// args llama-server is next started with.

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/llm"
)

func ctxError() *llm.ContextExceededError {
	// The figures of the real failure: the request is 5701, the window 4096.
	return &llm.ContextExceededError{Numbers: []int{5701, 4096}}
}

func ggufModel(t *testing.T) *uiModel {
	m := modeModel(t)
	m.prov = config.Provider{
		BaseURL: "http://127.0.0.1:9/v1", Model: "qwen.gguf",
		API: config.APIChat, Label: "llama.cpp GGUF",
	}
	return m
}

func TestAFullWindowOffersARestartOnALocalModel(t *testing.T) {
	m := ggufModel(t)

	m.offerCtxRestart(ctxError())
	if !m.ctxPick.open {
		t.Fatal("a context-window failure against a local model did not open the offer")
	}
	// Twice the window, rounded up: a doubling of the numbers the body named.
	if got := m.ctxPick.sizes[0]; got != 12288 {
		t.Errorf("first offer = %d, want 12288", got)
	}
	if got := m.ctxPick.sizes[1]; got != 24576 {
		t.Errorf("second offer = %d, want 24576", got)
	}
	if !m.modalUp() {
		t.Error("the offer does not own the frame while it is open")
	}
	// The offer is announced in the transcript, under the error it answers —
	// the one record that survives the dialog closing either way.
	if text := transcriptText(m); !strings.Contains(text, "llama.cpp") {
		t.Errorf("the transcript does not say a restart is possible:\n%s", text)
	}
}

func TestTheOfferIsLocalOnly(t *testing.T) {
	m := modeModel(t)
	m.prov = config.Provider{BaseURL: "https://api.test/v1", Label: "groq", Model: "m"}
	m.offerCtxRestart(ctxError())
	if m.ctxPick.open {
		t.Fatal("a cloud provider was offered a llama-server restart")
	}

	lm := ggufModel(t)
	lm.offerCtxRestart(errors.New("something else went wrong"))
	if lm.ctxPick.open {
		t.Fatal("an untyped error was offered a context restart")
	}
}

func TestEnterRestartsWithTheHighlightedSize(t *testing.T) {
	m := ggufModel(t)
	t.Setenv("DMCODE_LLAMA_ARGS", "-ngl 99 -c 4096")

	stopped := 0
	origStop := stopGGUF
	stopGGUF = func() { stopped++ }
	t.Cleanup(func() { stopGGUF = origStop })
	stubStart(t, &fakeLoad{prov: config.Provider{
		BaseURL: "http://127.0.0.1:8/v1", Model: "qwen.gguf", API: config.APIChat, Label: "llama.cpp GGUF",
	}}, nil)

	m.offerCtxRestart(ctxError())
	_, cmd := m.ctxPickKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no restart command")
	}
	// The old server is taken down before a new one is started: StartGGUF
	// answers with the shared state while one is standing, and a restart must
	// mean a new launch.
	if stopped != 1 {
		t.Errorf("stopGGUF ran %d times on restart, want exactly 1", stopped)
	}
	// The size replaced the old one and the unrelated flags stand.
	if args := os.Getenv("DMCODE_LLAMA_ARGS"); args != "-ngl 99 -c 12288" {
		t.Errorf("DMCODE_LLAMA_ARGS = %q, want %q", args, "-ngl 99 -c 12288")
	}
	// The pick handed the load to the wait machinery.
	if m.ggufLoad == nil {
		t.Fatal("the restart did not begin a load")
	}
	if text := transcriptText(m); !strings.Contains(text, "-c 12288") {
		t.Errorf("the transcript does not name the picked size:\n%s", text)
	}
}

func TestEnterOnTheSecondSizeUsesIt(t *testing.T) {
	m := ggufModel(t)
	t.Setenv("DMCODE_LLAMA_ARGS", "")
	origStop := stopGGUF
	stopGGUF = func() {}
	t.Cleanup(func() { stopGGUF = origStop })
	stubStart(t, &fakeLoad{}, nil)

	m.offerCtxRestart(ctxError())
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	if _, _ = m.ctxPickKey(down); m.ctxPick.list.sel != 1 {
		t.Fatalf("down did not reach the second row (sel = %d)", m.ctxPick.list.sel)
	}
	if _, _ = m.ctxPickKey(tea.KeyPressMsg{Code: tea.KeyEnter}); m.ctxPick.open {
		t.Error("a restart did not close the dialog")
	}
	if args := os.Getenv("DMCODE_LLAMA_ARGS"); args != "-c "+strconv.Itoa(24576) {
		t.Errorf("DMCODE_LLAMA_ARGS = %q, want the second row's size", args)
	}
}

func TestEscKeepsTheWindowAsItIs(t *testing.T) {
	m := ggufModel(t)
	before := os.Getenv("DMCODE_LLAMA_ARGS")

	stopped := 0
	origStop := stopGGUF
	stopGGUF = func() { stopped++ }
	t.Cleanup(func() { stopGGUF = origStop })

	m.offerCtxRestart(ctxError())
	if _, cmd := m.ctxPickKey(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil {
		t.Error("esc produced a command")
	}
	if stopped != 0 {
		t.Error("esc took the server down")
	}
	if m.ctxPick.open {
		t.Error("esc left the dialog open")
	}
	if args := os.Getenv("DMCODE_LLAMA_ARGS"); args != before {
		t.Errorf("DMCODE_LLAMA_ARGS changed on esc: %q → %q", before, args)
	}
}
