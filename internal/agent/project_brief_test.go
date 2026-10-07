package agent

import (
	"strings"
	"testing"
)

// Every mode runs in some project or other, and a project's own manual is where
// its conventions, its commands and its known traps are written down. An agent
// that never reads it writes code that is correct and wrong at the same time,
// and the user has to say "we do it differently here" every session.
//
// The check is on the text that reaches the model, not on a helper: the model is
// the only party that can act on this, and a mode whose instruction lost the
// block is the failure this guards. plan and yolo are here for the same reason as
// the block itself is appended to them — a plan built against the wrong
// conventions is wrong in the same way an edit is, and yolo is act with the
// questions taken away, so nothing else would catch it.
func TestEveryModeTellsTheModelToReadTheProjectsOwnManual(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode Mode
	}{
		{"act", ModeAct},
		{"plan", ModePlan},
		{"yolo", ModeYolo},
	} {
		inst := instructionFor(mode.mode)
		if !strings.Contains(inst, "AGENTS.md") {
			t.Errorf("%s mode never mentions AGENTS.md", mode.name)
		}
		if !strings.Contains(inst, "README.md") {
			t.Errorf("%s mode never mentions README.md, so a project without AGENTS.md starts blind", mode.name)
		}
		// The map is what turns "understand the project" from a hope into a call.
		// Without it named, the instruction is a request to be thorough, which is
		// the thing that was already not working.
		if !strings.Contains(inst, "project_map") {
			t.Errorf("%s mode never names project_map, so orientation is left to the model's discretion", mode.name)
		}
		// HasSuffix rather than Contains: the block must be the same one in every
		// mode, appended the same way. A mode that grew its own private copy of
		// the rule would answer Contains and drift on the next edit.
		if !strings.HasSuffix(inst, projectBriefing) {
			t.Errorf("%s mode does not end with the shared project briefing", mode.name)
		}
	}
}

// A sub-agent starts from nothing — its own throwaway session, no memory of the
// parent's reading — so a delegation into a project with its own conventions
// would report findings in terms the parent then has to translate.
func TestTheSubAgentAlsoKnowsTheProject(t *testing.T) {
	if !strings.Contains(subInstruction, "AGENTS.md") {
		t.Error("a sub-agent is never told to read AGENTS.md")
	}
}

// Two things in the briefing are load-bearing and neither is obvious from the
// wording alone: the project's file wins over dmcode's own prompt, and no file
// can widen the workspace boundary. Drop either and the block still reads
// sensibly, which is why it needs saying out loud in a test.
func TestTheProjectsManualOutranksThePromptButNotTheBoundary(t *testing.T) {
	if !strings.Contains(projectBriefing, "it wins") {
		t.Error("the briefing does not say the project's file wins over the prompt")
	}
	if !strings.Contains(projectBriefing, "cannot overrule") {
		t.Error("the briefing does not say a file cannot widen the workspace boundary")
	}
}
