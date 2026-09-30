package ui

import (
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
)

// TestAgentSwitchLandsBetweenTurns: the request is recorded, not applied — the
// runner is mid-turn and cannot be rebuilt underneath it.
func TestAgentSwitchLandsBetweenTurns(t *testing.T) {
	m := newSessionModel(t)
	m.mode = modePlan

	m.setModeRequest(dmagent.ModeAct, "")

	if !m.pendingMode.asked {
		t.Fatal("the request was not recorded")
	}
	if m.mode != modePlan {
		t.Errorf("the mode changed to %v mid-turn", m.mode)
	}
}

// TestPendingSwitchIsForgottenWhenThereIsNothingToApply: an unasked switch must
// not fire on the next turn.
func TestPendingSwitchIsForgottenWhenThereIsNothingToApply(t *testing.T) {
	m := newSessionModel(t)
	m.mode = modePlan

	if cmd := m.applyPendingMode(); cmd != nil {
		t.Error("a switch was applied with nothing pending")
	}
	if m.mode != modePlan {
		t.Errorf("the mode changed to %v with nothing pending", m.mode)
	}
}

// TestPendingSwitchToTheCurrentModeIsDropped: an ask for the mode the session is
// already in must not rebuild the agent for nothing.
func TestPendingSwitchToTheCurrentModeIsDropped(t *testing.T) {
	m := newSessionModel(t)
	m.mode = modeAct
	m.setModeRequest(dmagent.ModeAct, "")

	if cmd := m.applyPendingMode(); cmd != nil {
		t.Error("the agent was rebuilt for the mode it was already in")
	}
	if m.pendingMode.asked {
		t.Error("the request was not cleared")
	}
}

// TestSwitchModeIsAbsentFromTheActToolSet: the model must not be able to ask
// its way back into plan mode, or it would plan and unplan the same change for
// ever. Tab is the user's, and only the user's.
func TestSwitchModeIsAbsentFromTheActToolSet(t *testing.T) {
	m := newSessionModel(t)
	// The plan-mode set is what newRunner builds for plan mode; act mode uses
	// m.tools, which main assembles without the switch.
	m.tools = []tool.Tool{namedTool(t, "read_file")}
	m.readOnlyTools = append(append([]tool.Tool{}, m.tools...),
		namedTool(t, dmagent.SwitchModeName))
	m.pool = nil

	got := m.activeToolNames()
	for _, name := range got {
		if name == dmagent.SwitchModeName {
			t.Errorf("the act-mode set contains %q", name)
		}
	}
}

// TestSwitchModeIsPresentInThePlanToolSet: the counterpart — a plan-mode agent
// that cannot ask to act is an agent that can only wait for the user.
func TestSwitchModeIsPresentInThePlanToolSet(t *testing.T) {
	m := newSessionModel(t)
	m.tools = []tool.Tool{namedTool(t, "read_file")}
	m.readOnlyTools = append(append([]tool.Tool{}, m.tools...),
		namedTool(t, dmagent.SwitchModeName))
	m.mode = modePlan

	var found bool
	for _, name := range m.activeToolNames() {
		if name == dmagent.SwitchModeName {
			found = true
		}
	}
	if !found {
		t.Errorf("the plan-mode set is missing %q: %v", dmagent.SwitchModeName, m.activeToolNames())
	}
}

// namedTool builds a no-op tool with a given name, so a test can describe a tool
// set without building the real instruments.
func namedTool(t *testing.T, name string) tool.Tool {
	t.Helper()
	tl, err := functiontool.New(functiontool.Config{Name: name}, func(agent.Context, struct{}) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("build %q: %v", name, err)
	}
	return tl
}
