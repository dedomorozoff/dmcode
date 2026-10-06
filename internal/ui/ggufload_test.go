package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/discover"
)

// loadingModel is a model that is loading and will not finish for the length of
// the test, with a progress line to show.
//
// It is built the way a real session is rather than from framedModel: that one
// is a mid-turn snapshot with a zero textinput, so a keypress never reaches the
// chat's own handling and half of what follows would be testing nothing.
func loadingModel(t *testing.T) *uiModel {
	t.Helper()
	m := InitialModel(nil, nil, config.Provider{
		Label: "llama.cpp GGUF", Model: "qwen.gguf", API: config.APIChat,
	}, nil, nil, nil)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.ggufLoad = &fakeLoad{
		prov: config.Provider{BaseURL: "http://127.0.0.1:9/v1", Model: "qwen.gguf", API: config.APIChat, Label: "llama.cpp GGUF"},
		tail: "load_tensors:  42%",
	}
	m.ggufLoadStart = time.Now().Add(-40 * time.Second)
	return m
}

func transcriptText(m *uiModel) string {
	var b strings.Builder
	for _, l := range m.history {
		b.WriteString(l.text)
		b.WriteString("\n")
	}
	return b.String()
}

// The load is now behind the interface, which means the interface has to say so.
// A model that is not answering yet looks exactly like a broken session unless
// the screen says otherwise, and the badge is the one line always on screen.
func TestALoadingModelIsVisibleAndNotMistakenForABrokenSession(t *testing.T) {
	m := loadingModel(t)

	badge := m.stateBadge()
	if !strings.Contains(badge, "LOADING") {
		t.Errorf("the badge says %q while a model is loading", badge)
	}
	// WORKING would be a lie in the other direction: it means the agent has your
	// message, and nobody sent one.
	if strings.Contains(badge, "WORKING") {
		t.Errorf("a loading model reports itself as working: %q", badge)
	}

	note := m.progressNote()
	for _, want := range []string{"40s", "load_tensors"} {
		if !strings.Contains(note, want) {
			t.Errorf("the progress note does not carry %q: %q", want, note)
		}
	}
	// The elapsed seconds are the difference between "loading" and "hung".
	if !strings.Contains(note, "40s") {
		t.Errorf("the note does not count the wait: %q", note)
	}
}

// A message typed during the load has to go back where it was. Sending it would
// produce a connection error the user cannot act on — the endpoint is not broken,
// it is not up — and dropping it loses a sentence they already typed.
func TestAMessageSentDuringTheLoadIsKeptAndNotSent(t *testing.T) {
	m := loadingModel(t)
	m.input.SetValue("what does this project do?")

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := m.input.Value(); got != "what does this project do?" {
		t.Errorf("the prompt was taken out of the input: %q", got)
	}
	if m.busy {
		t.Error("a refused message still started a turn")
	}
	if !strings.Contains(transcriptText(m), "still loading") {
		t.Errorf("the refusal is not explained:\n%s", transcriptText(m))
	}
}

// The commands are not messages to the model, so a session waiting on a load
// must still answer them. The guard for the message above sits after the command
// dispatch for exactly this reason, and a guard placed before it would take
// /help and /quit away for the length of a load.
func TestCommandsStillWorkWhileTheModelLoads(t *testing.T) {
	m := loadingModel(t)
	m.input.SetValue("/help")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(m.suggest) != 0 {
		t.Error("/help opened the command list instead of printing help")
	}
	if !strings.Contains(transcriptText(m), "ctrl+p") {
		t.Errorf("/help printed nothing while a model was loading:\n%s", transcriptText(m))
	}
	if m.input.Value() != "" {
		t.Errorf("the command was not consumed: %q", m.input.Value())
	}
}

// A load that cannot be abandoned is a wait the user has to sit through, and
// sitting through it is what moving the load behind the interface was meant to
// stop. There is no turn to stop while a model loads, so esc is unambiguous.
func TestEscGivesUpOnTheLoad(t *testing.T) {
	m := loadingModel(t)
	var stopped bool
	orig := stopGGUF
	stopGGUF = func() { stopped = true }
	t.Cleanup(func() { stopGGUF = orig })

	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.ggufLoad != nil {
		t.Error("the session is still waiting on a model the user gave up on")
	}
	if !stopped {
		t.Error("the load was forgotten without shutting the server down")
	}
	if !strings.Contains(transcriptText(m), "stopped loading") {
		t.Errorf("giving up was not said out loud:\n%s", transcriptText(m))
	}
}

// A model still loading must not stop the user stopping a turn, and a turn in
// flight must not stop them giving up on a load. Esc means the turn first,
// because that is what a user pressing it during a turn means.
func TestEscStopsTheTurnBeforeTheLoad(t *testing.T) {
	m := loadingModel(t)
	m.busy = true
	stopped := false
	m.cancelTurn = func() { stopped = true }

	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if !stopped || m.busy {
		t.Error("esc did not stop the running turn")
	}
	if m.ggufLoad == nil {
		t.Error("esc stopped the load instead of the turn")
	}
}

// The provider swap has to reach the pool as well as the current provider. A
// runner rebuilt from a stale pool — which is what Tab, shift+tab and /cd all
// do — would put the session back on the endpoint the user just switched away
// from, so the swap would hold for one turn and then be quietly undone.
func TestTheModelSwapReachesThePoolToo(t *testing.T) {
	m := loadingModel(t)
	before := m.exactWindow
	m.pool = []config.Provider{
		{BaseURL: "http://127.0.0.1:9/v1", Model: "file-stem"},
		{BaseURL: "https://api.example.com/v1", Model: "some-cloud-model"},
	}
	up := m.ggufLoad.(*fakeLoad)
	// A model the window table recognises, so the re-read has a value to take:
	// an unrecognised name resolves to zero on both sides of the comparison and
	// the assertion below would pass without the line ever running.
	up.prov = config.Provider{
		BaseURL: "http://127.0.0.1:9/v1", Model: "gemma-3-4b", API: config.APIChat, Label: "llama.cpp GGUF",
	}

	m.Update(ggufReadyMsg{prov: up.prov})

	if len(m.pool) == 0 || m.pool[0].Model != "gemma-3-4b" {
		t.Fatalf("the pool still describes the old model: %+v", m.pool)
	}
	// The old entry for the same URL has to be gone, not left as a reserve: a
	// failover to a port serving nothing reads as a network fault.
	for _, p := range m.pool {
		if p.Model == "file-stem" {
			t.Error("the pool keeps the pre-load entry as a failover target")
		}
	}
	if len(m.pool) != 2 {
		t.Errorf("the reserve endpoint was dropped: %+v", m.pool)
	}
	// And the window the compaction threshold and the meter are computed from has
	// to follow, or both keep describing the model that just went away.
	if after := config.ContextWindow("gemma-3-4b"); after == 0 || after == before || m.exactWindow != after {
		t.Errorf("the context window was not re-read: was %d, now %d, want %d", before, m.exactWindow, after)
	}
}

// A cancelled load did not fail, and reporting it as one would be the tool
// arguing with the user about a wait they ended on purpose.
func TestACancelledLoadIsNotReportedAsAFailure(t *testing.T) {
	m := loadingModel(t)
	m.Update(ggufReadyMsg{err: discover.ErrCancelled})

	text := transcriptText(m)
	if strings.Contains(text, "did not start") {
		t.Errorf("a cancelled load was reported as a failure:\n%s", text)
	}
	if m.statusText == "failed" {
		t.Errorf("the status bar says failed after a cancellation: %q", m.statusText)
	}
	if m.ggufLoad != nil {
		t.Error("the session is still waiting on a load that was cancelled")
	}
}

// A load that failed is worth the whole transcript treatment, because the user
// has to know that the model they configured is not there and where to look.
func TestAFailedLoadNamesTheLogAndKeepsTheConfiguration(t *testing.T) {
	m := loadingModel(t)
	m.Update(ggufReadyMsg{err: errors.New("llama-server did not answer within 1m0s; the server's output is in /home/u/.dmcode/llama-server.log")})

	text := transcriptText(m)
	if !strings.Contains(text, "llama-server.log") {
		t.Errorf("the failure does not point at the log:\n%s", text)
	}
	if !strings.Contains(text, "DMCODE_GGUF") {
		t.Errorf("the failure does not say the configuration was kept:\n%s", text)
	}
	if m.prov.Model == "qwen3-coder" {
		t.Error("a failed load still changed the provider")
	}
}
