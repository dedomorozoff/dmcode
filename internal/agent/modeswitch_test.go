package agent

import "testing"

// noop is the invocation's end, for the cases where ending it is not the point.
func noop() {}

// TestSwitchModeRefusesTheWayBack: only the user returns a session to plan
// mode, with Tab. A model that could do it itself would plan and unplan the
// same change for ever.
func TestSwitchModeRefusesTheWayBack(t *testing.T) {
	var asked bool
	h := &modeSwitcher{current: ModePlan, request: func(Mode, string) { asked = true }}

	got, err := h.decide(noop, switchArgs{Mode: "plan"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got.Accepted {
		t.Error("the agent was allowed to put the session back into plan mode")
	}
	if got.Reason == "" {
		t.Error("the refusal gives no reason, so the model cannot tell whether to ask again")
	}
	if asked {
		t.Error("a refused switch still reached the UI")
	}
	if h.switched {
		t.Error("a refused switch was recorded as pending")
	}
}

// TestSwitchModeIsOnlyReachableInPlanMode: in act mode the tool is not built at
// all, so this is the belt to those braces — the tool must agree even if it
// were, and must not ask the UI to rebuild the runner for the mode it is in.
func TestSwitchModeIsOnlyReachableInPlanMode(t *testing.T) {
	var asked bool
	h := &modeSwitcher{current: ModeAct, request: func(Mode, string) { asked = true }}

	got, err := h.decide(noop, switchArgs{Mode: "act"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !got.Accepted || !got.Applied {
		t.Errorf("answer = %+v, want it reported as already in act mode", got)
	}
	if asked {
		t.Error("a switch to the current mode still asked the UI to rebuild the runner")
	}
}

// TestSwitchModeEndsTheTurnAndQueuesTheSwitch: this is the whole mechanism. The
// tool cannot swap the instrument set under a running turn, so it ends the
// invocation and the UI applies the switch afterwards.
func TestSwitchModeEndsTheTurnAndQueuesTheSwitch(t *testing.T) {
	var (
		asked   bool
		ended   bool
		gotMode Mode
	)
	h := &modeSwitcher{current: ModePlan, request: func(m Mode, _ string) {
		asked, gotMode = true, m
	}}

	res, err := h.decide(func() { ended = true }, switchArgs{Mode: "act"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !res.Accepted {
		t.Errorf("answer = %+v, want the switch accepted", res)
	}
	if !asked || gotMode != ModeAct {
		t.Errorf("the UI was asked for %v (asked=%v), want act", gotMode, asked)
	}
	if !ended {
		t.Error("the invocation was not ended — the rest of the turn would run against the plan tool set")
	}
}

// TestSecondSwitchInOneTurnIsNotQueuedTwice: a second request would rebuild the
// agent twice, and would tell the model the first one failed when it did not.
func TestSecondSwitchInOneTurnIsNotQueuedTwice(t *testing.T) {
	calls := 0
	h := &modeSwitcher{current: ModePlan, request: func(Mode, string) { calls++ }}

	h.decide(noop, switchArgs{Mode: "act"})
	res, _ := h.decide(noop, switchArgs{Mode: "act"})

	if calls != 1 {
		t.Errorf("the UI was asked %d times, want 1", calls)
	}
	if res.Reason == "" {
		t.Error("the second call gives no reason, so the model cannot tell it is already queued")
	}
}

// TestUnknownModeIsRefusedWithTheList: a bare "no" leaves the model guessing.
func TestUnknownModeIsRefusedWithTheList(t *testing.T) {
	h := &modeSwitcher{current: ModePlan, request: func(Mode, string) {}}

	for _, mode := range []string{"", "  ", "edit", "ACT2"} {
		got, err := h.decide(noop, switchArgs{Mode: mode})
		if err != nil {
			t.Fatalf("decide(%q): %v", mode, err)
		}
		if got.Accepted {
			t.Errorf("mode %q was accepted", mode)
		}
		if got.Reason == "" {
			t.Errorf("mode %q was refused with no reason", mode)
		}
	}
}

// TestNoHookMeansNoSwitch: a session with no UI attached cannot switch, and says
// so rather than claiming a switch that will not happen.
func TestNoHookMeansNoSwitch(t *testing.T) {
	h := &modeSwitcher{current: ModePlan}
	got, err := h.decide(noop, switchArgs{Mode: "act"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Accepted {
		t.Error("a switch was accepted with no UI attached")
	}
	if got.Reason == "" {
		t.Error("no reason given, so the model would wait for a switch that never comes")
	}
}

// TestSwitchModeIsNamed: the sidebar lists what the agent can reach, so a tool
// built under the wrong name is a tool the user cannot see being offered.
func TestSwitchModeIsNamed(t *testing.T) {
	tl, err := SwitchModeTool(ModePlan, nil)
	if err != nil {
		t.Fatalf("SwitchModeTool: %v", err)
	}
	if tl.Name() != SwitchModeName {
		t.Errorf("tool name = %q, want %q", tl.Name(), SwitchModeName)
	}
}
