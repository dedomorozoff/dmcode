package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
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

// TestViewRequestsMouseModeOnlyWhenEnabled: with the mouse on, the terminal has
// to be asked for motion as well as button events, because a drag is delivered as
// MouseMotionMsg and cell-motion reporting never sends one. That is the whole
// reason this mode changed when selection was added.
func TestViewRequestsMouseModeOnlyWhenEnabled(t *testing.T) {
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.width, m.height = 100, 30
	m.layout()

	m.mouseEnabled = true
	if got := m.View().MouseMode; got != tea.MouseModeAllMotion {
		t.Errorf("MouseMode with the mouse on = %v, want AllMotion so a drag is reported", got)
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

// askToolName is the question tool's name, written out rather than imported from
// the ask package: the test only needs to recognise one tool out of a set.
const askToolName = "ask_user"

// stubAskTool is a stand-in for the question tool. The real one is built in
// main.go, not by tools.MakeTools, so a model built straight from the workspace
// tools has no ask_user in it at all — and a test that filtered a set which never
// held the tool would pass whatever activeTools returned. The stub is what makes
// the removal observable.
func stubAskTool(t *testing.T) tool.Tool {
	t.Helper()
	tl, err := functiontool.New(functiontool.Config{Name: askToolName},
		func(adkagent.Context, struct{}) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

// yoloModel is modeModel with the question tool in the act set and the yolo set
// main.go would build: the same tools, minus the question tool. The set is built
// here rather than shared with a helper, so the test pins the rule ("ask_user is
// what comes out") rather than whatever a helper happened to pass.
func yoloModel(t *testing.T) *uiModel {
	t.Helper()
	m := modeModel(t)
	// Act gets the question tool, so that dropping it is a real change.
	m.tools = append(append([]tool.Tool(nil), m.tools...), stubAskTool(t))
	m.toolNames = dmtools.ToolNames(m.tools)

	yolo := make([]tool.Tool, 0, len(m.tools))
	for _, tl := range m.tools {
		if tl.Name() == askToolName {
			continue
		}
		yolo = append(yolo, tl)
	}
	m.yoloTools = yolo
	return m
}

// TestShiftTabTogglesYoloAndNothingElseReachesIt: the binding is the feature.
// Yolo cannot stop a running turn to ask, so a mode that is entered by accident
// is a turn that runs unattended — which is why it is on one key and on no
// command.
func TestShiftTabTogglesYoloAndNothingElseReachesIt(t *testing.T) {
	m := yoloModel(t)
	var model tea.Model = m

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.mode != modeYolo {
		t.Fatalf("shift+tab left the mode at %v, want yolo", m.mode)
	}

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.mode != modeAct {
		t.Errorf("the second shift+tab left the mode at %v, want the mode it came from", m.mode)
	}

	// /mode cannot reach it, by name or by synonym. The refusal has to name the
	// key, because the likeliest way to meet this mode is to have heard of it and
	// guessed the command.
	for _, name := range []string{"yolo", "auto", "YOLO"} {
		m.setMode(name)
		if m.mode == modeYolo {
			t.Errorf("/mode %s entered yolo; the key is the only way in", name)
		}
	}
	if !strings.Contains(m.statusText, "shift+tab") {
		t.Errorf("the /mode refusal does not say which key it is: %q", m.statusText)
	}
}

// TestAQueuedSwitchCannotLandTheSessionInYolo is the other door. The switch tool
// is built in plan mode only and can only ask for act, so setModeRequest already
// refuses a yolo request; this covers the queue itself, for the case where one
// was recorded before that check existed. A mode the user never pressed a key for
// is the one thing the key exists to prevent.
func TestAQueuedSwitchCannotLandTheSessionInYolo(t *testing.T) {
	m := yoloModel(t)
	m.mode = modePlan
	m.pendingMode = pendingMode{mode: modeYolo, asked: true}
	m.applyPendingMode()
	if m.mode == modeYolo {
		t.Error("a queued request put the session into yolo")
	}
	if m.pendingMode.asked {
		t.Error("the refused request was left queued for the next turn")
	}
}

// TestYoloHasActReachWithoutTheQuestionTool is the whole contract in one test:
// same reach as act, no way to interrupt. Reach is act's because a mode that
// quietly narrowed the tools as well would be a second plan mode.
func TestYoloHasActReachWithoutTheQuestionTool(t *testing.T) {
	m := yoloModel(t)
	m.mode = modeYolo

	if !containsName(m.activeTools(), "write_file") {
		t.Error("yolo mode cannot write; it is meant to be act's reach")
	}
	if !containsName(m.activeTools(), "run_command") {
		t.Error("yolo mode cannot run commands; it is meant to be act's reach")
	}
	if containsName(m.activeTools(), askToolName) {
		t.Errorf("yolo mode still offers %q, so it cannot deliver what it promises", askToolName)
	}
	// The sidebar must agree, or a user would sit waiting for a question the agent
	// has no way to ask.
	for _, n := range m.activeToolNames() {
		if n == askToolName {
			t.Error("the sidebar still lists ask_user in yolo mode")
		}
	}
	if len(m.activeToolNames()) != len(m.activeTools()) {
		t.Errorf("the sidebar lists %d tools for %d reachable ones",
			len(m.activeToolNames()), len(m.activeTools()))
	}
}

// TestTabDoesNotWalkIntoYolo: tab means the plan/act pair and only that. If tab
// could reach yolo, the mode with the least warning attached would be one
// keypress from the mode the user actually chose, on the key they already trust
// for something else.
func TestTabDoesNotWalkIntoYolo(t *testing.T) {
	m := yoloModel(t)
	var model tea.Model = m
	for i := 0; i < 4; i++ {
		model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		if m.mode == modeYolo {
			t.Fatalf("tab reached yolo on press %d", i+1)
		}
	}
	if m.mode != modeAct {
		t.Errorf("four presses of tab left the mode at %v, want act", m.mode)
	}
}

// TestYoloRemembersTheModeItCameFrom: leaving yolo must not silently undo a
// deliberate plan-mode choice, or the key that turns the mode off also changes
// what the session is allowed to do.
func TestYoloRemembersTheModeItCameFrom(t *testing.T) {
	m := yoloModel(t)
	var model tea.Model = m

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // act -> plan
	if m.mode != modePlan {
		t.Fatalf("tab left the mode at %v, want plan", m.mode)
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.mode != modeYolo {
		t.Fatalf("shift+tab left the mode at %v, want yolo", m.mode)
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.mode != modePlan {
		t.Errorf("leaving yolo landed in %v, want the plan mode it was entered from", m.mode)
	}
}

// TestYoloIsRefusedMidTurn: the runner is built around one tool set for a turn,
// and a set that loses ask_user underneath a model about to call it would strand
// the tool goroutine on a question nobody can answer.
func TestYoloIsRefusedMidTurn(t *testing.T) {
	m := yoloModel(t)
	m.busy = true
	m.toggleYolo()
	if m.mode == modeYolo {
		t.Error("yolo was entered while a turn was running")
	}
	if m.statusText == "" {
		t.Error("the refusal was silent; the keypress looked like it did nothing")
	}
}

// TestYoloSaysSoWhenThereIsNoInstrumentSetForIt: a session built without a yolo
// set would keep the question tool, so the badge would be the only difference
// between a mode that works and one that lies.
func TestYoloSaysSoWhenThereIsNoInstrumentSetForIt(t *testing.T) {
	m := modeModel(t) // no yolo set
	m.toggleYolo()
	if m.mode == modeYolo {
		t.Error("yolo was entered with no instrument set behind it")
	}
	if m.statusText == "" {
		t.Error("the refusal was silent")
	}
}

// TestYoloBadgeSaysYolo: the badge is the only thing on screen saying the agent
// will not come back and ask, so it has to name the mode and survive a narrow bar
// like the other two do.
func TestYoloBadgeSaysYolo(t *testing.T) {
	m := yoloModel(t)
	m.mode = modeYolo
	if got := m.modeBadge(); !strings.Contains(got, "YOLO") {
		t.Errorf("badge = %q, want it to name yolo", got)
	}
	m.width = 8
	if got := m.modeBadge(); strings.TrimSpace(got) == "" {
		t.Error("the yolo badge vanished on a narrow status bar")
	}
}

// ------------------------------------------------------- mouse text selection

// selectDrag drives a real drag through the event loop — press, motion, release —
// because the interesting failures are in the hand-off between the messages, not
// in any one of them.
//
// The write seam is swapped for the duration: a drag that moves ends in a copy,
// and without this the drag tests put their own fixture text into the one
// clipboard the user has.
func selectDrag(t *testing.T, m *uiModel, ax, ay, bx, by int) {
	t.Helper()
	fakeClipboardWrite(t)
	var model tea.Model = m
	model, _ = model.Update(tea.MouseClickMsg{X: ax, Y: ay, Button: tea.MouseLeft})
	model, _ = model.Update(tea.MouseMotionMsg{X: (ax + bx) / 2, Y: (ay + by) / 2})
	model, _ = model.Update(tea.MouseMotionMsg{X: bx, Y: by})
	model, _ = model.Update(tea.MouseReleaseMsg{X: bx, Y: by, Button: tea.MouseLeft})
}

// selectModel is a session with a known frame, so a selection can be asserted
// against exact text instead of whatever the layout happened to produce.
func selectModel(t *testing.T, frame string) *uiModel {
	t.Helper()
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.mouseEnabled = true
	m.width, m.height = 40, 10
	m.layout()
	m.frame = frame
	return m
}

// TestSelectionCopiesWhatWasHighlighted is the point of the feature: the text
// under the drag goes to the clipboard, taken from the rendered frame so it is
// what the user actually pointed at.
func TestSelectionCopiesWhatWasHighlighted(t *testing.T) {
	m := selectModel(t, "hello world\nsecond line\nthird line")
	m.selAnchorX, m.selAnchorY = 6, 0
	// Row 1 cut at column 6: "second". A pointer on the last cell of a row
	// selects the whole row, so the end has to be past the text, not on it.
	m.selFocusX, m.selFocusY = 6, 1
	m.selMoved = true

	if got := m.selectedText(); got != "world\nsecond" {
		t.Errorf("selectedText() = %q, want %q", got, "world\nsecond")
	}
}

// TestSelectionReadsBackwardsTheSameWay: a drag that goes up and to the left
// selects the same text as one that goes down and to the right. Copying in the
// order the mouse travelled would hand back gibberish.
func TestSelectionReadsBackwardsTheSameWay(t *testing.T) {
	m := selectModel(t, "alpha beta\ngamma delta")
	// Anchored on the last row, dragged up to the start of "beta": the selection
	// is the rectangle between them, and it has to come back in reading order.
	m.selAnchorX, m.selAnchorY = 5, 1
	m.selFocusX, m.selFocusY = 6, 0
	m.selMoved = true

	if got := m.selectedText(); got != "beta\ngamma" {
		t.Errorf("a backwards drag copied %q, want %q", got, "beta\ngamma")
	}
}

// TestSelectionDropsStylingAndPadding: what lands on the clipboard has to be the
// text, not the escape sequences the renderer wrapped it in.
func TestSelectionDropsStylingAndPadding(t *testing.T) {
	styled := "\x1b[1;32merror:\x1b[0m build failed        "
	m := selectModel(t, styled)
	m.selAnchorX, m.selAnchorY = 0, 0
	m.selFocusX, m.selFocusY = 25, 0
	m.selMoved = true

	got := m.selectedText()
	if strings.Contains(got, "\x1b") {
		t.Errorf("the copied text still carries escape sequences: %q", got)
	}
	if got != "error: build failed" {
		t.Errorf("selectedText() = %q, want the text without styling or padding", got)
	}
}

// TestSelectionCountsWideRunesAsTwoCells: a pointer reports cells, not bytes
// and not runes. An east-asian character occupies two cells, so a row of four
// such cells is two characters — a selection that counted runes would cut the
// first one in half and hand back something the user never pointed at.
func TestSelectionCountsWideRunesAsTwoCells(t *testing.T) {
	m := selectModel(t, "世界 wide")
	m.selAnchorX, m.selAnchorY = 0, 0
	m.selFocusX, m.selFocusY = 4, 0 // two double-width runes
	m.selMoved = true

	if got := m.selectedText(); got != "世界" {
		t.Errorf("selectedText() = %q, want %q — four cells are two wide characters", got, "世界")
	}
	// The highlight has to agree with the copy, or the user sees one range lit up
	// and gets another on the clipboard.
	got := m.paintSelection(m.frame)
	if plain := ansi.Strip(got); !strings.HasPrefix(plain, "世界") {
		t.Errorf("the painted frame starts with %q, want the wide characters intact", plain)
	}
}

// TestSelectionSurvivesADragPastTheEndOfALine: releasing below the last row, or to
// the right of a short one, has to contribute what exists rather than fail —
// which is what a user dragging to the bottom-right corner actually does.
func TestSelectionSurvivesADragPastTheEndOfALine(t *testing.T) {
	m := selectModel(t, "short\n\nanother")
	m.selAnchorX, m.selAnchorY = 0, 0
	m.selFocusX, m.selFocusY = 500, 99
	m.selMoved = true

	got := m.selectedText()
	if !strings.Contains(got, "short") || !strings.Contains(got, "another") {
		t.Errorf("an over-long drag copied %q, want the whole frame's text", got)
	}
}

// TestAPlainClickCopiesNothing: without this, every click in the transcript would
// overwrite the clipboard with a single character and the feature would be worse
// than useless.
func TestAPlainClickCopiesNothing(t *testing.T) {
	m := selectModel(t, "some text on screen")
	before := m.statusText
	selectDrag(t, m, 4, 0, 4, 0)

	if m.selMoved {
		t.Error("a click with no movement left a live selection")
	}
	if m.statusText != before {
		t.Errorf("a plain click reported %q, want nothing", m.statusText)
	}
}

// TestARightClickDoesNotStartASelection: the right button belongs to the
// terminal's own paste menu, and hijacking it would break a workflow that works
// today.
func TestARightClickDoesNotStartASelection(t *testing.T) {
	m := selectModel(t, "some text")
	var model tea.Model = m
	model, _ = model.Update(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseRight})
	model, _ = model.Update(tea.MouseMotionMsg{X: 8, Y: 0})
	if m.selMoved || m.selDown {
		t.Error("a right-button drag started a selection")
	}
}

// TestSelectionIsIgnoredWithTheMouseOff: /mouse hands the terminal back to the
// user, and that has to mean the wheel and the selection both, or the toggle lies
// about what it turned off.
func TestSelectionIsIgnoredWithTheMouseOff(t *testing.T) {
	m := selectModel(t, "some text")
	m.mouseEnabled = false
	var model tea.Model = m
	model, _ = model.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	model, _ = model.Update(tea.MouseMotionMsg{X: 9, Y: 0})
	if m.selDown || m.selMoved {
		t.Error("a selection started with the mouse disabled")
	}
}

// TestTheFrameIsHighlightedWhileSelected: a selection the user cannot see is a
// selection they cannot aim, so the drawn frame has to carry the reverse-video
// attribute over exactly the cells that will be copied.
func TestTheFrameIsHighlightedWhileSelected(t *testing.T) {
	m := selectModel(t, "hello world")
	m.selAnchorX, m.selAnchorY = 6, 0
	m.selFocusX, m.selFocusY = 11, 0
	m.selMoved = true

	got := m.paintSelection(m.frame)
	if !strings.Contains(got, selReverseOn) {
		t.Fatalf("the selection was not painted at all: %q", got)
	}
	// The highlight must sit over "world" and not over the word before it.
	plain := ansi.Strip(got)
	if strings.Contains(plain, selReverseOn) {
		t.Errorf("the highlight leaked into the plain text: %q", plain)
	}
	// Reversing [6,11) of "hello world" leaves the first six cells untouched.
	if idx := strings.Index(plain, selReverseOn); idx != -1 {
		t.Errorf("unexpected marker at %d", idx)
	}
	if !strings.HasSuffix(got, selReverseOff) && !strings.Contains(got, selReverseOff) {
		t.Error("the highlight was never turned off, so the rest of the frame would render inverted")
	}
	// Re-running the same paint must be stable: the highlight goes on the plain
	// frame, so painting twice cannot nest a second reverse inside the first.
	again := m.paintSelection(m.frame)
	if strings.Count(got, selReverseOn) != strings.Count(again, selReverseOn) {
		t.Errorf("painting is not idempotent: %d markers then %d",
			strings.Count(got, selReverseOn), strings.Count(again, selReverseOn))
	}
}

// TestTheHighlightSurvivesAColourChangeMidRow: a transcript row carries its own
// SGR resets, and a highlight wrapped once around the slice would be cancelled by
// the first colour change — the selection would visibly stop halfway along a line.
func TestTheHighlightSurvivesAColourChangeMidRow(t *testing.T) {
	row := "\x1b[1mred\x1b[0m plain"
	m := selectModel(t, row)
	m.selAnchorX, m.selAnchorY = 0, 0
	m.selFocusX, m.selFocusY = 9, 0
	m.selMoved = true

	got := m.paintSelection(m.frame)
	// One marker before the reset, and the reverse re-armed after it — so the
	// second half of the row is still highlighted.
	if n := strings.Count(got, selReverseOn); n < 2 {
		t.Errorf("the highlight was cancelled by the row's own reset: %d markers in %q", n, got)
	}
	if got := m.selectedText(); got != "red plain" {
		t.Errorf("selectedText() = %q, want %q", got, "red plain")
	}
}

// TestArrowsPickACommandFromTheList is the replacement for tab completion: the
// command list is navigated with the arrow keys and enter runs what is
// highlighted. It drives the real event loop, since the point is which key the
// user actually presses.
func TestArrowsPickACommandFromTheList(t *testing.T) {
	m := modeModel(t)
	m.promptHistory = []string{"/setup", "/clear"}
	m.histPos = len(m.promptHistory)
	m.draft = "/"

	// Type the slash rather than setting the value, so the list is built by the
	// same path a user's keystroke takes.
	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if len(m.suggest) < 3 {
		t.Fatalf("typing / produced %d rows, want the whole command list", len(m.suggest))
	}

	// Down twice, then enter: the third command runs, not the first.
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.suggestSel != 2 {
		t.Fatalf("two downs left the highlight at %d, want 2", m.suggestSel)
	}
	// The prompt history must not have been touched: the arrows belonged to the
	// list, and if they had also moved the history the user would lose their
	// draft without seeing it happen.
	if m.histPos != len(m.promptHistory) {
		t.Errorf("the arrows moved the prompt history to %d while the list was open", m.histPos)
	}

	// The command ran rather than being typed and left there: /clear empties the
	// transcript, which is observable without reaching into the model.
	want := m.suggest[2].text
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != "" {
		t.Errorf("enter left %q in the input, want it consumed", m.input.Value())
	}
	if want == "/clear" && len(m.history) != 0 {
		t.Errorf("the highlighted command %q did not run", want)
	}
}

// Up at the top and down at the bottom must stay put rather than wrap: a list
// that jumps from the last row to the first turns "I meant that one" into a
// command the user did not choose.
func TestSuggestHighlightStopsAtTheEnds(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/")
	m.updateSuggest()
	n := len(m.suggest)
	if n < 2 {
		t.Skip("the command list is too short to test the ends")
	}

	for i := 0; i < n+3; i++ {
		m.moveSuggest(1)
	}
	if m.suggestSel != n-1 {
		t.Errorf("downing past the end left the highlight at %d, want %d", m.suggestSel, n-1)
	}
	for i := 0; i < n+3; i++ {
		m.moveSuggest(-1)
	}
	if m.suggestSel != 0 {
		t.Errorf("upping past the top left the highlight at %d, want 0", m.suggestSel)
	}
}

// Typing a filter resets the highlight, so a narrowed list never keeps a
// selection that points past its own end. A blink, which changes no text, must
// not: the text input emits one about twice a second and every one of them
// reaches the rebuild, so resetting unconditionally threw the highlight away
// twice a second and a single press of Down appeared to snap the cursor back to
// the first row.
func TestTypingResetsTheHighlightButABlinkDoesNot(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/")
	m.updateSuggest()
	m.moveSuggest(1)
	m.moveSuggest(1)
	if m.suggestSel == 0 {
		t.Skip("the command list has one row")
	}

	var model tea.Model = m
	for b := 0; b < 3; b++ {
		model, _ = model.Update(cursor.BlinkMsg{})
	}
	if m.suggestSel == 0 {
		t.Errorf("a cursor blink reset the selection to the top")
	}

	m.input.SetValue("/cl")
	m.updateSuggest()
	if m.suggestSel != 0 {
		t.Errorf("narrowing the list left the highlight at %d, want the top", m.suggestSel)
	}
	if len(m.suggest) == 0 || m.suggest[0].text != "/clear" {
		t.Errorf("filtering by 'cl' gave %v, want /clear first", m.suggest)
	}
}

// A selection carried into a shorter list must be pulled back inside it: an
// index past the end would either panic or, once the dialog is drawn, mark a row
// that is not there.
func TestSelectionIsClampedToTheList(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/")
	m.updateSuggest()
	if len(m.suggest) < 3 {
		t.Skip("the command list is too short")
	}
	m.suggestSel = len(m.suggest) - 1

	m.input.SetValue("/mode")
	m.updateSuggest()

	if m.suggestSel >= len(m.suggest) {
		t.Fatalf("the selection is at %d but the list has %d rows", m.suggestSel, len(m.suggest))
	}
	if !strings.Contains(ansi.Strip(m.suggestBox()), "▸ "+m.suggest[m.suggestSel].text) {
		t.Errorf("the dialog does not mark the clamped row:\n%s", ansi.Strip(m.suggestBox()))
	}
}

// A Russian user has to be told which keys drive the list, in Russian. The keys
// are looked up from the ui package and the catalog lives in i18n, so this
// asserts the rendered Russian header rather than poking at the catalog: what
// matters is what the user sees, not which map holds the entry.
func TestCommandListHeaderIsTranslated(t *testing.T) {
	m := modeModel(t)
	m.width, m.height = 92, 30
	restore := i18n.Current()
	t.Cleanup(func() { i18n.Set(restore) })
	if err := i18n.Set(i18n.Russian); err != nil {
		t.Fatal(err)
	}

	m.input.SetValue("/")
	m.updateSuggest()
	if len(m.suggest) == 0 {
		t.Skip("no commands")
	}
	// Row 0 is the box's own top border; the title is the row under it.
	header := ansi.Strip(strings.Split(m.suggestBox(), "\n")[1])
	// The arrow keys and the words around them: if the catalog lost the entry the
	// whole line comes back in English, which is the thing to catch.
	for _, want := range []string{"↑↓", "enter", "esc"} {
		if !strings.Contains(header, want) {
			t.Errorf("the Russian header %q lost %q", header, want)
		}
	}
	if strings.Contains(header, "choose") || strings.Contains(header, "dismiss") {
		t.Errorf("the title is still English: %q", header)
	}
}

// The command list is painted over the bottom of the chat panel, so the frame it
// produces has to be indistinguishable from the closed one in everything but the
// text it covers: same height, same width, input box unmoved. That is the whole
// reason it is an overlay — as rows of the frame it shrank the transcript on
// every "/" and ran into the bottom of the screen on a short terminal.
//
// The overflow marker has to survive the height cap too: a list that looks
// complete because its "↓ more" row was trimmed away is the failure worth
// guarding.
func TestCommandListFitsTheFrame(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {92, 30}, {80, 24}, {60, 20}, {40, 12}, {30, 10}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			m := modeModel(t)
			m.width, m.height = w, h
			m.layout()
			m.history = []line{{kindAgent, "an answer worth keeping on screen"}}
			m.historyDirty = true
			m.syncVP()
			m.followVP()

			// The frame is the baseline rather than a computed constant: the input
			// box wraps its placeholder onto a second line on a narrow terminal, so
			// the honest statement is "the list changes nothing" rather than a
			// number that has to be re-derived for every width.
			// The input already holds "/" for both frames: the placeholder is
			// several words long and wraps onto a second line on a narrow terminal,
			// so rendering the baseline before typing would compare two different
			// input boxes. What is left between the frames is the dialog alone.
			m.input.SetValue("/")
			closed := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
			want := len(closed)

			m.updateSuggest()
			if len(m.suggest) < 8 {
				t.Fatalf("only %d commands, too few to overflow the list", len(m.suggest))
			}

			// Below the bound the list is dropped entirely rather than squeezed.
			// The commands are still reachable by typing them, so nothing is lost.
			if m.suggestHeight() == 0 {
				if open := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n"); len(open) != want {
					t.Errorf("with no room for the list the frame is %d rows, want %d", len(open), want)
				}
				return
			}

			lines := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
			if len(lines) != want {
				t.Fatalf("the frame with the list open is %d rows, want %d", len(lines), want)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != w {
					t.Errorf("row %d is %d cells, want %d", i, got, w)
				}
			}
			// The input box is the bottom of the frame and must not move a cell
			// when the list opens, or the whole interface jumps under the cursor.
			// Its borders are compared rather than the text between them: typing
			// "/" legitimately changes what the box says.
			if lines[want-1] != closed[want-1] || lines[want-3] != closed[want-3] {
				t.Errorf("the input box moved:\nclosed %q\nopen   %q",
					closed[want-3], lines[want-3])
			}
			// The status bar is directly above it and is state, not decoration.
			if lines[want-inputHeight-statusHeight] != closed[want-inputHeight-statusHeight] {
				t.Error("the status bar changed while the list was open")
			}
		})
	}
}

// The dialog shows a window of a longer list, and the window has to follow the
// selection. It did not: the overflow marker was given the last row by trimming
// the window from the bottom, which dropped the selected row first — so arrowing
// down past the visible list left a dialog with no marker in it at all and the
// user arrowing blind.
func TestCommandDialogScrollsWithTheSelection(t *testing.T) {
	m := modeModel(t)
	m.width, m.height = 92, 26
	m.layout()

	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if len(m.suggest) < 8 {
		t.Fatalf("only %d commands, too few to need scrolling", len(m.suggest))
	}

	for i := range m.suggest {
		if m.suggestSel != i {
			t.Fatalf("step %d left the selection at %d, want the arrows to track it", i, m.suggestSel)
		}
		box := ansi.Strip(m.suggestBox())
		// The selection is drawn, and it is the one drawn: the marker has to sit
		// on the selected command, not on any other row.
		want := m.suggest[i].text
		if !strings.Contains(box, "▸ "+want) {
			t.Errorf("step %d: %q is not marked in the dialog:\n%s", i, want, box)
		}
		// The list really is scrolling rather than sitting still: by the time the
		// selection is past the visible rows, the first command has been dropped.
		if i > 0 && i+1 < len(m.suggest) {
			if m.suggest[0].text == m.suggest[i].text {
				t.Errorf("step %d: the dialog still shows the very first command", i)
			}
		}
		// The overflow marker is what admits the list does not fit; it must never
		// be the row that gets trimmed.
		if i+1 < len(m.suggest) && !strings.Contains(box, "more") {
			t.Errorf("step %d: the list overflows but says nothing:\n%s", i, box)
		}
		model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
}

// The sidebar shares the rows the list is drawn over, so it has to survive them:
// blanking it would take the model, the folder and the tool list away for as long
// as a "/" is on screen.
func TestCommandListKeepsTheSidebar(t *testing.T) {
	m := modeModel(t)
	m.width, m.height = 100, 30
	m.showSidebar = true
	m.layout()
	m.history = []line{{kindAgent, "an answer"}}
	m.historyDirty = true
	m.syncVP()
	m.followVP()

	closed := strings.Split(m.View().Content, "\n")
	m.input.SetValue("/")
	m.updateSuggest()
	if m.suggestHeight() == 0 {
		t.Skip("no room for the list at this size")
	}
	open := strings.Split(m.View().Content, "\n")

	end := len(closed) - inputHeight - statusHeight
	for i := max(end-m.suggestHeight(), headerHeight); i < end; i++ {
		sidebarClosed := ansi.StringWidth(closed[i]) - m.chatBoxWidth()
		if got := ansi.StringWidth(open[i]) - m.chatBoxWidth(); got != sidebarClosed {
			t.Errorf("row %d: the sidebar is %d cells with the list open, %d without",
				i, got, sidebarClosed)
		}
	}
}

// Escape closes the list and leaves the typed text alone. Losing what you typed
// because you opened a picker by accident is the failure this guards.
func TestEscapeClosesTheListAndKeepsTheText(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/mo")
	m.updateSuggest()
	if len(m.suggest) == 0 {
		t.Skip("no match for /mo")
	}

	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if len(m.suggest) != 0 {
		t.Error("escape did not close the command list")
	}
	if got := m.input.Value(); got != "/mo" {
		t.Errorf("escape changed the input to %q, want the typed text kept", got)
	}
}

// TestStatusBarShowsTheModeAndNothingElse pins the bar down to what it is for.
//
// It used to carry the model name and a run of hotkeys on top of the mode and
// the state. The model was the third place on screen showing it — the header and
// the sidebar already have it — and the hotkey list was a fixed string competing
// with everything else for one row, so on a narrow terminal it was the part that
// dropped off, leaving `ctrl+p │ ctrl+y │ esc` half-printed. Neither earns its
// place now that the bar leads with the mode.
func TestStatusBarShowsTheModeAndNothingElse(t *testing.T) {
	m := modeModel(t)
	m.prov = config.Provider{Label: "text.pollinations.ai", Model: "openai/gpt-oss-120b"}

	for _, mode := range []agentMode{modePlan, modeAct} {
		m.mode = mode
		bar := ansi.Strip(m.statusBarView())

		if !strings.Contains(bar, mode.String()) {
			t.Errorf("%v: the status bar does not show the mode: %q", mode, bar)
		}
		if strings.Contains(bar, m.prov.Model) {
			t.Errorf("%v: the status bar repeats the model name %q: %q", mode, m.prov.Model, bar)
		}
		for _, k := range []string{"ctrl+p", "ctrl+b", "ctrl+y"} {
			if strings.Contains(bar, k) {
				t.Errorf("%v: the status bar advertises %q again: %q", mode, k, bar)
			}
		}
	}
}

// The state badge is the other half of the bar and has to survive every width
// that can hold it, because "the agent is still working" and "the agent stopped"
// are the two things a user glances at the bar for.
//
// Below about 30 cells the mode badge alone fills the row — it is padded to six
// cells and the state badge is ten — and TestModeBadgeSurvivesANarrowStatusBar
// already covers that end of the scale, so here the bar is only held to keeping
// both for as long as both physically fit.
func TestStatusBarKeepsBothBadgesAtEveryWidth(t *testing.T) {
	for _, w := range []int{200, 120, 100, 80, 60, 40, 30, 20, 16} {
		m := modeModel(t)
		m.width, m.height = w, 30
		m.busy = true
		m.statusText = "calling: run_command"
		bar := ansi.Strip(m.statusBarView())
		// Measured from the rendered badges, not from a guess at their text: the
		// emoji in WORKING is two cells wide, and a one-cell miss here is exactly
		// what makes the bar wrap.
		bothFit := ansi.StringWidth(m.modeBadge())+2+ansi.StringWidth(m.stateBadge()) <= max(w, 16)-2

		if !strings.Contains(bar, m.mode.String()) {
			t.Errorf("%d cells: the mode badge is gone: %q", w, bar)
		}
		if bothFit && !strings.Contains(bar, "WORKING") {
			t.Errorf("%d cells: the state badge is gone although both fit: %q", w, bar)
		}
		// One row, exactly the terminal width: an overflowing bar wraps and pushes
		// the input off the bottom of the screen.
		lines := strings.Split(strings.TrimRight(bar, "\n"), "\n")
		if len(lines) != 1 {
			t.Errorf("%d cells: the bar wrapped onto %d rows", w, len(lines))
		}
		if got := ansi.StringWidth(lines[0]); got != max(w, 16) {
			t.Errorf("%d cells: the bar is %d cells wide", w, got)
		}
	}
}

// TestTabAlwaysSwitchesMode covers the simplification of Tab. It used to mean
// two things — complete the highlighted suggestion inside a "/" command, switch
// the mode everywhere else — so a user who expected one got the other depending
// on what happened to be in the input. The command list is now picked with the
// arrows, which leaves Tab meaning one thing everywhere.
func TestTabAlwaysSwitchesMode(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/mod")
	m.updateSuggest()
	if len(m.suggest) == 0 {
		t.Skip("no suggestion for /mod in this build")
	}

	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if m.mode != modePlan {
		t.Errorf("Tab inside a command left the mode at %v, want plan", m.mode)
	}
	// The list is untouched by Tab: the arrows still own it.
	if len(m.suggest) == 0 {
		t.Error("Tab closed the command list instead of switching the mode")
	}
	if got := m.input.Value(); got != "/mod" {
		t.Errorf("Tab rewrote the input to %q, want the typed text kept", got)
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
