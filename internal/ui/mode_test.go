package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"google.golang.org/adk/v2/tool"

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
// selection that points past its own end.
func TestTypingResetsTheHighlight(t *testing.T) {
	m := modeModel(t)
	m.input.SetValue("/")
	m.updateSuggest()
	m.moveSuggest(1)
	m.moveSuggest(1)
	if m.suggestSel == 0 {
		t.Skip("the command list has one row")
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
	header := strings.Split(m.suggestView(), "\n")[0]
	// The arrow keys and the words around them: if the catalog lost the entry the
	// whole line comes back in English, which is the thing to catch.
	for _, want := range []string{"↑↓", "enter", "esc"} {
		if !strings.Contains(header, want) {
			t.Errorf("the Russian header %q lost %q", header, want)
		}
	}
	if strings.Contains(header, "choose") || strings.Contains(header, "dismiss") {
		t.Errorf("the header is still English: %q", header)
	}
}

// The command list is part of the frame, not a floating overlay, so every row of
// it has to be exactly the terminal wide. A row one cell over makes JoinVertical
// pad the whole frame sideways; a row wrapping onto a second line makes the frame
// a row taller than the terminal and pushes the input off the bottom.
//
// It is also bounded in height, and the "↓ more" marker has to survive that cap —
// a list that looks complete because its overflow marker got cut off is the exact
// failure worth guarding.
func TestCommandListFitsTheFrame(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {92, 30}, {80, 24}, {60, 20}, {40, 12}, {30, 10}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			m := modeModel(t)
			m.width, m.height = w, h
			m.input.SetValue("/")
			m.updateSuggest()
			if len(m.suggest) < 8 {
				t.Fatalf("only %d commands, too few to overflow the list", len(m.suggest))
			}
			m.layout()

			// Below the bound the list is dropped entirely rather than squeezed
			// into a chat panel that has already hit its floor. The commands are
			// still reachable by typing them, so nothing is lost.
			if m.suggestHeight() == 0 {
				if m.suggestView() != "" {
					t.Error("the list is not budgeted any rows but still renders")
				}
				return
			}

			lines := strings.Split(m.suggestView(), "\n")
			if len(lines) > m.suggestHeight() {
				t.Errorf("the list is %d rows but only %d were budgeted", len(lines), m.suggestHeight())
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != w {
					t.Errorf("row %d is %d cells, want %d", i, got, w)
				}
			}
			// The whole frame, list included, still has to be exactly the terminal.
			view := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
			if len(view) != max(h, chromeHeight) {
				t.Errorf("the frame is %d rows, want %d", len(view), max(h, chromeHeight))
			}

			// With the list overflowing, the marker must be on screen: the user
			// cannot tell there are more commands if it is not.
			if len(m.suggest) > m.suggestHeight()-1 && !strings.Contains(m.suggestView(), "more") {
				t.Error("the list overflows but shows no overflow marker")
			}
		})
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
