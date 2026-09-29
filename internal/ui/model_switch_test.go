package ui

import (
	"testing"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// TestModelSwitchKeepsTheSession pins the promise the transcript makes when a
// model is switched: the session id survives the runner rebuild, so the
// conversation context the user was promised is the one the next turn uses.
func TestModelSwitchKeepsTheSession(t *testing.T) {
	m := newTurnModel(t)
	before := m.sessionID

	msg := modelSwitchedMsg{name: "other/model", pool: []config.Provider{{Model: "other/model"}}}
	if _, _ = m.Update(msg); m.sessionID != before {
		t.Fatalf("session id changed from %q to %q on a model switch", before, m.sessionID)
	}
	if m.prov.Model != "other/model" {
		t.Fatalf("model is %q, want other/model", m.prov.Model)
	}
}
