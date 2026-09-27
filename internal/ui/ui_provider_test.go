package ui

import (
	"strings"
	"testing"

	"dmcode/internal/config"
)

// The hint text used to point at a command that did not exist. Whatever it
// names has to be reachable, or a blocked user has nowhere to go.
func TestFreeHintNamesRealCommand(t *testing.T) {
	// Pull out the /command token rather than guessing at word counts.
	var named []string
	for _, f := range strings.Fields(config.FreeProviderHint) {
		if strings.HasPrefix(f, "/") {
			named = append(named, strings.Trim(f, "/.,;"))
		}
	}
	if len(named) == 0 {
		t.Fatalf("freeProviderHint names no command at all: %q", config.FreeProviderHint)
	}

	names := map[string]bool{}
	for _, c := range (&uiModel{}).commands() {
		names[c.name] = true
	}
	help := hintCommandsLine()
	for _, cmd := range named {
		if !names[cmd] {
			t.Errorf("freeProviderHint points at /%s, which is not a command; known: %v", cmd, names)
		}
		if !strings.Contains(help, "/"+cmd) {
			t.Errorf("/%s is missing from the /help listing: %q", cmd, help)
		}
	}
}

// hintCommandsLine is the command list /help prints.
func hintCommandsLine() string {
	var m uiModel
	for _, c := range m.commands() {
		if c.name != "help" {
			continue
		}
		c.run(&m)
		for _, l := range m.history {
			if l.kind == kindSys && strings.Contains(l.text, "/quit") {
				return l.text
			}
		}
	}
	return ""
}

// setupLabel is shown in the sidebar, so it must not leak a full URL with a
// secret-bearing path, and must not be empty.
func TestSetupLabel(t *testing.T) {
	for _, o := range config.SetupOptions() {
		l := setupLabel(o)
		if l == "" {
			t.Errorf("%q produced an empty label", o.Label)
		}
		if strings.Contains(l, "://") {
			t.Errorf("%q produced %q, which still carries the scheme", o.Label, l)
		}
	}
	if got := setupLabel(config.SetupOptions()[0]); got != "text.pollinations.ai" {
		t.Errorf("Pollinations label = %q, want %q", got, "text.pollinations.ai")
	}
}

// The check is only meaningful on the chat wire: probing a /v1/responses
// provider with a /chat/completions request would report a false failure.
func TestToolCheckSkipsResponsesWire(t *testing.T) {
	m := &uiModel{prov: config.Provider{
		BaseURL: "https://example.invalid/v1",
		Model:   "some-model",
		API:     config.APIResponses,
	}}
	if cmd := m.toolCheckCmd(); cmd != nil {
		t.Error("a responses-wire provider must not be probed by the chat client")
	}

	m.prov.API = config.APIChat
	if cmd := m.toolCheckCmd(); cmd == nil {
		t.Error("a chat-wire provider must be probed")
	}
}

// A failed check is worth showing; a passed or undecided one is noise.
func TestHandleToolCheckReporting(t *testing.T) {
	base := func() *uiModel {
		return &uiModel{prov: config.Provider{Label: "Pollinations (без ключа)", Model: "openai-fast", API: config.APIChat}}
	}

	m := base()
	m.handleToolCheck(toolCheckMsg{ok: true, conclusive: true, Model: m.prov.Model, Label: m.prov.Label})
	if len(m.history) != 0 {
		t.Errorf("a passing check produced %d transcript lines", len(m.history))
	}

	m = base()
	m.handleToolCheck(toolCheckMsg{ok: false, conclusive: false, Model: m.prov.Model, Label: m.prov.Label})
	if len(m.history) != 0 {
		t.Errorf("an inconclusive check produced %d transcript lines", len(m.history))
	}

	m = base()
	m.handleToolCheck(toolCheckMsg{ok: false, conclusive: true, Model: m.prov.Model, Label: m.prov.Label})
	if len(m.history) != 1 {
		t.Fatalf("a failed check produced %d lines, want 1", len(m.history))
	}
	if !strings.Contains(m.history[0].text, "/setup") {
		t.Errorf("the advisory does not offer a way out: %q", m.history[0].text)
	}

	// A result for a model the user has since switched away from is dropped.
	m = base()
	m.handleToolCheck(toolCheckMsg{ok: false, conclusive: true, Model: "old-model", Label: m.prov.Label})
	if len(m.history) != 0 {
		t.Error("a stale check result was reported against the current model")
	}
}
