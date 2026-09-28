package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"google.golang.org/adk/v2/tool"

	"github.com/dedomorozoff/dmcode/internal/config"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// scrolledModel builds a model with a transcript tall enough to scroll, sized so
// the viewport has real rows to move through.
func scrolledModel(t *testing.T) *uiModel {
	t.Helper()
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.width, m.height = 100, 30
	m.layout()
	for i := 0; i < 200; i++ {
		m.history = append(m.history, line{kindAgent, strings.Repeat("filler line ", 4)})
	}
	m.historyDirty = true
	m.syncVP()
	// A live session starts pinned to the newest line, so the fixture has to do
	// the same or the first wheel-up would have nothing to scroll away from.
	m.followVP()
	return m
}

// TestWheelUpScrollsAndReleasesTheStick is the regression behind "the wheel does
// nothing": the wheel event was never produced because the view left the mouse
// mode at None, and even once it is produced the follow-the-tail stick snapped
// the view straight back to the bottom.
func TestWheelUpScrollsAndReleasesTheStick(t *testing.T) {
	m := scrolledModel(t)
	if !m.stick {
		t.Fatal("the model does not start stuck to the bottom")
	}
	if !m.vp.AtBottom() {
		t.Fatal("the viewport does not start at the bottom")
	}

	var model tea.Model = m
	model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})

	if m.vp.AtBottom() {
		t.Error("the wheel up did not move the view off the bottom")
	}
	if m.stick {
		t.Error("scrolling up left the stick set, so the next token would snap back")
	}

	// A streamed delta after the scroll must not drag the view down again.
	m.appendAgentText("a new line arrives")
	m.followVP()
	if m.vp.AtBottom() {
		t.Error("followVP pulled the view back to the bottom after the user scrolled up")
	}
}

func TestWheelDownReturnsToTheBottomAndReattaches(t *testing.T) {
	m := scrolledModel(t)
	var model tea.Model = m
	model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if !m.vp.AtBottom() || !m.stick {
		t.Errorf("scrolling back down = atBottom %v stick %v, want both true", m.vp.AtBottom(), m.stick)
	}
	m.appendAgentText("more output")
	m.followVP()
	if !m.vp.AtBottom() {
		t.Error("the view stopped following the tail after returning to the bottom")
	}
}

// TestMouseToggleGatesScrolling documents the /mouse escape hatch: with the mouse
// off the wheel is ignored, which is how a user gets native drag-select back.
func TestMouseToggleGatesScrolling(t *testing.T) {
	m := scrolledModel(t)
	m.mouseEnabled = false

	var model tea.Model = m
	model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if !m.vp.AtBottom() {
		t.Error("the wheel scrolled even though the mouse is off")
	}
}

func TestViewRequestsMouseModeOnlyWhenEnabled(t *testing.T) {
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.width, m.height = 100, 30
	m.layout()

	m.mouseEnabled = true
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode with the mouse on = %v, want CellMotion", got)
	}
	m.mouseEnabled = false
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("MouseMode with the mouse off = %v, want None", got)
	}
}

// modeModel builds a model with both instrument sets, which is what a Tab press
// swaps between.
func modeModel(t *testing.T) *uiModel {
	t.Helper()
	full, err := dmtools.MakeTools()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := dmtools.MakeReadOnlyTools()
	if err != nil {
		t.Fatal(err)
	}
	m := InitialModel(nil, nil, config.Provider{}, full, ro, dmtools.ToolNames(full))
	m.width, m.height = 100, 30
	m.layout()
	return m
}

func containsName(ts []tool.Tool, name string) bool {
	for _, tl := range ts {
		if tl.Name() == name {
			return true
		}
	}
	return false
}

// TestTabSwitchesModeAndToolSet is the core of the plan/act feature: the mode
// indicator and the reachable tool set have to move together, or the badge would
// be describing something the agent is not doing.
func TestTabSwitchesModeAndToolSet(t *testing.T) {
	m := modeModel(t)
	if m.mode != modeAct {
		t.Fatalf("the session starts in %v, want act", m.mode)
	}
	if !containsName(m.activeTools(), "write_file") {
		t.Fatal("act mode cannot write")
	}

	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if m.mode != modePlan {
		t.Fatalf("Tab left the mode at %v, want plan", m.mode)
	}
	for _, banned := range []string{"write_file", "edit_file", "run_command"} {
		if containsName(m.activeTools(), banned) {
			t.Errorf("plan mode still exposes %q", banned)
		}
	}
	// The sidebar must report the reduced set, not the full one.
	for _, n := range m.activeToolNames() {
		if n == "write_file" {
			t.Error("the sidebar still lists write_file in plan mode")
		}
	}

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.mode != modeAct {
		t.Errorf("the second Tab left the mode at %v, want act", m.mode)
	}
	if !containsName(m.activeTools(), "write_file") {
		t.Error("switching back to act did not restore the write tools")
	}
}

// TestTabKeepsAutocompleteInsideACommand protects the other half of Tab: inside a
// "/" command the suggestion is what the user is reaching for.
func TestTabKeepsAutocompleteInsideACommand(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/mod")
	m.updateSuggest()
	if len(m.suggest) == 0 {
		t.Skip("no suggestion for /mod in this build")
	}

	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if m.mode != modeAct {
		t.Errorf("Tab inside a command switched the mode to %v", m.mode)
	}
	if !strings.HasPrefix(m.input.Value(), "/model") {
		t.Errorf("Tab did not complete the command: %q", m.input.Value())
	}
}

// TestModeSwitchIsRefusedMidTurn guards the runner: swapping the tool set while a
// turn is streaming would let the model keep calling a tool just withdrawn.
func TestModeSwitchIsRefusedMidTurn(t *testing.T) {
	m := modeModel(t)
	m.busy = true
	m.toggleMode()
	if m.mode != modeAct {
		t.Errorf("the mode changed to %v while a turn was running", m.mode)
	}
	if m.statusText == "" {
		t.Error("the refusal was silent; the user was not told why")
	}
}

func TestSetModeRejectsAnUnknownName(t *testing.T) {
	m := modeModel(t)
	m.setMode("sideways")
	if m.mode != modeAct {
		t.Errorf("an unknown mode name moved the session to %v", m.mode)
	}
	if !strings.Contains(m.statusText, "/mode") {
		t.Errorf("status %q does not tell the user the accepted values", m.statusText)
	}
}

func TestModeBadgeNamesTheCurrentMode(t *testing.T) {
	m := modeModel(t)
	if got := ansi.Strip(m.modeBadge()); !strings.Contains(got, "ACT") {
		t.Errorf("badge in act mode = %q, want it to say ACT", got)
	}
	m.mode = modePlan
	if got := ansi.Strip(m.modeBadge()); !strings.Contains(got, "PLAN") {
		t.Errorf("badge in plan mode = %q, want it to say PLAN", got)
	}
}

// TestModeBadgeSurvivesANarrowStatusBar is the layout invariant the frame
// depends on: the row must not wrap, whatever the terminal width.
func TestModeBadgeSurvivesANarrowStatusBar(t *testing.T) {
	m := modeModel(t)
	m.mode = modePlan
	m.busy = true
	for _, w := range []int{20, 30, 40, 60, 80, 120, 200} {
		m.width = w
		m.layout()
		for i, r := range strings.Split(m.statusBarView(), "\n") {
			if got := ansi.StringWidth(r); got > w {
				t.Errorf("width %d: status row %d is %d cells, want at most %d", w, i, got, w)
			}
		}
	}
}

// TestChangeDirMovesTheBoundaryWithTheSession is what makes /cd trustworthy: the
// tools must be re-fenced to the new directory, not merely told about it.
func TestChangeDirMovesTheBoundaryWithTheSession(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := dmtools.SetRoot(base); err != nil {
		t.Fatal(err)
	}
	chdir(t, base)
	t.Cleanup(func() { dmtools.SetRoot(base) })

	m := modeModel(t)
	m.workDir = dmtools.Root()

	m.changeDir("other")
	if got := dmtools.Root(); filepath.Base(got) != "other" {
		t.Errorf("the tool boundary is %q, want it moved into other", got)
	}
	if filepath.Base(m.workDir) != "other" {
		t.Errorf("the sidebar reports %q, want it to report other", m.workDir)
	}
	if filepath.Base(m.workDirShort) != "other" {
		t.Errorf("the short folder name is %q, want it to report other", m.workDirShort)
	}
}

func TestChangeDirReportsAnUnreachableTarget(t *testing.T) {
	base := t.TempDir()
	if err := dmtools.SetRoot(base); err != nil {
		t.Fatal(err)
	}
	chdir(t, base)
	t.Cleanup(func() { dmtools.SetRoot(base) })

	m := modeModel(t)
	m.workDir = dmtools.Root()
	m.changeDir(filepath.Join(base, "nope"))

	if filepath.Base(m.workDir) != filepath.Base(base) {
		t.Errorf("a failed change moved the session to %q", m.workDir)
	}
	if len(m.history) == 0 || m.history[len(m.history)-1].kind != kindErr {
		t.Error("the failure was not reported in the transcript")
	}
}

func TestChangeDirWithNoArgumentReportsTheCurrentFolder(t *testing.T) {
	base := t.TempDir()
	if err := dmtools.SetRoot(base); err != nil {
		t.Fatal(err)
	}
	chdir(t, base)
	t.Cleanup(func() { dmtools.SetRoot(base) })

	m := modeModel(t)
	m.workDir = dmtools.Root()
	m.changeDir("")

	if len(m.history) == 0 {
		t.Fatal("bare /cd printed nothing")
	}
	if last := m.history[len(m.history)-1]; last.kind != kindSys || !strings.Contains(last.text, filepath.Base(base)) {
		t.Errorf("bare /cd printed %+v, want the current folder", last)
	}
}

func TestShortenPathKeepsTheTail(t *testing.T) {
	long := `C:\Users\someone\projects\dmcode`
	got := shortenPath(long, 20)
	if ansi.StringWidth(got) > 20 {
		t.Errorf("shortenPath = %q (%d cells), want at most 20", got, ansi.StringWidth(got))
	}
	if !strings.HasSuffix(got, "dmcode") {
		t.Errorf("shortenPath = %q, want it to keep the identifying tail", got)
	}
	// A path that already fits must come back untouched.
	if got := shortenPath("dmcode", 20); got != "dmcode" {
		t.Errorf("shortenPath on a short path = %q, want it unchanged", got)
	}
}

// TestSidebarFitsWithAFullPath is the layout consequence of showing the real
// directory: a long Windows path must not break the frame.
func TestSidebarFitsWithAFullPath(t *testing.T) {
	m := modeModel(t)
	m.workDir = `C:\Users\somebody\a\deeply\nested\project\directory\with\a\long\name`
	m.workDirShort = filepath.Base(m.workDir)
	for _, h := range []int{40, 24, 15, 10} {
		m.width, m.height = 120, h
		m.layout()
		for i, r := range strings.Split(m.sidebarView(m.height), "\n") {
			if got := ansi.StringWidth(r); got > m.width {
				t.Errorf("height %d: sidebar row %d is %d cells, want at most %d", h, i, got, m.width)
			}
		}
	}
}
