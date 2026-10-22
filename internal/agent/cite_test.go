package agent

import (
	"strings"
	"testing"
)

// A citation is a place the user can click, and the model is the only party
// that can write one. Every mode therefore has to ask for it: a mode that
// does not produces a report whose conclusions the user cannot open, which is
// the same failure as a panel that shows a number nobody can act on.
//
// The check is on the text that reaches the model, for the reason the project
// briefing's own test gives: the model is the only one who can act on it, and
// an instruction that lost the rule still reads perfectly well.
func TestEveryModeAsksForCitations(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode Mode
	}{
		{"act", ModeAct},
		{"plan", ModePlan},
		{"yolo", ModeYolo},
	} {
		inst := instructionFor(mode.mode)
		if !strings.Contains(inst, "path:line") {
			t.Errorf("%s mode never asks for a path:line citation", mode.name)
		}
		// The shape has to be spelled out, not merely implied: "cite the line"
		// is satisfied by "in ui.go, around the reader", which is a place
		// nobody can open.
		if !strings.Contains(inst, "internal/ui/ui.go:412") {
			t.Errorf("%s mode does not show the shape of a citation", mode.name)
		}
	}
}

// A sub-agent's report is read by the parent, and the parent quotes it. A
// place the sub-agent named in the wrong shape is a place the parent cannot
// pass on.
func TestTheSubAgentCitesPlacesToo(t *testing.T) {
	if !strings.Contains(subInstruction, "path:line") {
		t.Error("a sub-agent is never asked to cite a place")
	}
}
