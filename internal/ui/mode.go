package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"google.golang.org/adk/v2/tool"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// agentMode is what the session is allowed to do. Act edits the workspace; plan
// only reads it and answers with a proposal; yolo edits it and is never allowed
// to stop and ask.
type agentMode int

const (
	modeAct agentMode = iota
	modePlan
	// modeYolo is act's reach with the question tool withdrawn. The values are
	// deliberately not the agent package's: agentModeFrom is the only conversion,
	// so adding a mode on either side cannot silently alias the other.
	modeYolo
)

// agentMode mirrors the agent package's modes without making the UI depend on
// its constant names, so a mode can be compared in a key handler without an
// import cycle through the tool set. The mapping is written out rather than
// cast: a cast would make the two enums silently alias, and the day the agent
// package reorders its constants the UI would start building the wrong agent
// with no error anywhere.
func (m agentMode) agentMode() dmagent.Mode {
	switch m {
	case modePlan:
		return dmagent.ModePlan
	case modeYolo:
		return dmagent.ModeYolo
	}
	return dmagent.ModeAct
}

// agentModeFrom is the way back. An unrecognised request falls back to act
// rather than to whatever the numbers happen to line up as: the only caller is
// the switch tool, which may ask for act and nothing else, and act is the mode
// that can do least harm to a plan that has just been written.
func agentModeFrom(m dmagent.Mode) agentMode {
	switch m {
	case dmagent.ModePlan:
		return modePlan
	case dmagent.ModeYolo:
		return modeYolo
	}
	return modeAct
}

func (m agentMode) String() string {
	switch m {
	case modePlan:
		return "PLAN"
	case modeYolo:
		return "YOLO"
	}
	return "ACT"
}

// toggled is the plan/act pair, which is what tab walks. Yolo is not in it:
// yolo is not the other half of a choice between two ways to work, it is a
// separate way to work that the user turns on deliberately. See toggleYolo.
func (m agentMode) toggled() agentMode {
	if m == modePlan {
		return modeAct
	}
	return modePlan
}

// yolo reports whether this is the mode the shift+tab key turns on and off.
func (m agentMode) yolo() bool { return m == modeYolo }

// activeTools is the instrument set the current mode is allowed. Plan mode gets
// the read-only subset, so "read-only" is a property of what the agent can
// reach rather than a promise in the prompt. Yolo gets its own set, which is
// act's minus the question tool: same reach, no way to interrupt.
func (m *uiModel) activeTools() []tool.Tool {
	switch {
	case m.mode == modePlan && len(m.readOnlyTools) > 0:
		return m.readOnlyTools
	case m.mode == modeYolo && len(m.yoloTools) > 0:
		return m.yoloTools
	}
	return m.tools
}

// activeToolNames is the sidebar's tool list, which must match what the agent can
// actually call — a sidebar promising edit_file in plan mode would be a lie, and
// one promising ask_user in yolo mode would be worse: the user would expect a
// question that can never come.
func (m *uiModel) activeToolNames() []string {
	switch {
	case m.mode == modePlan && len(m.readOnlyTools) > 0:
		return toolNamesOf(m.readOnlyTools)
	case m.mode == modeYolo && len(m.yoloTools) > 0:
		return toolNamesOf(m.yoloTools)
	}
	return m.toolNames
}

func toolNamesOf(ts []tool.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

var (
	styleBadgeAct  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(cSuccess).Padding(0, 1)
	styleBadgePlan = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(cTool).Padding(0, 1)
	// Yolo gets the warning colour rather than a third success tone: it is the
	// one mode where the agent will not come back and ask, and the badge is the
	// only thing on screen that says so while a turn is already running.
	styleBadgeYolo = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(cErr).Padding(0, 1)
)

// modeBadge renders the current mode for the status bar. It is deliberately
// loud: a mode the user cannot see is a mode they cannot trust, and "the agent
// did not edit" is exactly the kind of thing that has to be visible to be
// believed.
func (m *uiModel) modeBadge() string {
	switch m.mode {
	case modePlan:
		return styleBadgePlan.Render(i18n.T("PLAN"))
	case modeYolo:
		return styleBadgeYolo.Render(i18n.T("YOLO"))
	}
	return styleBadgeAct.Render(i18n.T("ACT"))
}

// toggleMode switches between planning and working.
//
// A switch during a turn is refused: the runner is built around one tool set for
// the duration of a turn, and swapping it mid-stream would let the model keep
// calling a tool that has just been withdrawn.
func (m *uiModel) toggleMode() tea.Cmd {
	if m.busy {
		m.statusText = i18n.T("wait for the turn to finish before switching mode")
		return nil
	}
	m.mode = m.mode.toggled()
	// Nothing is written to the transcript. The badge in the status bar is the
	// whole report: a line saying "mode: ACT" pushed a blank row into a
	// conversation the user was reading, and the badge has said the same thing on
	// screen the entire time.
	if len(m.readOnlyTools) == 0 {
		// Nothing to switch to: the session was built without a read-only set,
		// so say so rather than leaving a badge that promises nothing.
		m.statusText = i18n.T("plan mode is unavailable")
		return nil
	}
	return m.rebuildRunner()
}

// toggleYolo turns the no-questions mode on, and off again.
//
// It is bound to shift+tab and to nothing else — no /mode argument reaches it —
// because it is the one mode whose cost is paid while it runs rather than when
// it is entered. A user who cannot see the badge has no way to know the agent is
// not going to ask, and a user who is not watching has no way to turn it off.
//
// Leaving yolo returns to whichever of act and plan was in use before, so the
// key does not silently undo a deliberate plan-mode choice.
func (m *uiModel) toggleYolo() tea.Cmd {
	if m.busy {
		m.statusText = i18n.T("wait for the turn to finish before switching mode")
		return nil
	}
	if m.mode.yolo() {
		m.mode = m.beforeYolo
		if !m.mode.yolo() && m.beforeYolo == modePlan && len(m.readOnlyTools) == 0 {
			// The plan set is gone, so there is nothing to go back to.
			m.mode = modeAct
		}
	} else {
		m.beforeYolo = m.mode
		m.mode = modeYolo
		if len(m.yoloTools) == 0 {
			// No yolo set was built, so the agent would keep the question tool
			// and the badge would be the only difference. Say so rather than
			// showing a mode that does not do what it says.
			m.mode = m.beforeYolo
			m.statusText = i18n.T("yolo mode is unavailable")
			return nil
		}
	}
	// No transcript line, for the reason in toggleMode: the badge is the report.
	return m.rebuildRunner()
}

// setMode switches to an explicit mode, used by the /mode command.
func (m *uiModel) setMode(name string) tea.Cmd {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "plan", "read", "readonly":
		if m.mode == modePlan {
			m.statusText = i18n.T("already in plan mode")
			return nil
		}
	case "act", "write", "edit", "":
		if m.mode == modeAct {
			m.statusText = i18n.T("already in act mode")
			return nil
		}
	case "yolo", "auto":
		// Named explicitly so the answer is an explanation rather than a usage
		// line: someone who has just heard about the mode and typed /mode yolo
		// deserves to be told which key it is, not that the spelling was wrong.
		m.statusText = i18n.T("yolo mode is turned on with shift+tab, not with a command")
		return nil
	default:
		m.statusText = i18n.T("usage: /mode plan|act")
		return nil
	}
	// Force the toggle to land on the requested mode.
	if m.mode.toggled() != m.mode {
		return m.toggleMode()
	}
	m.statusText = i18n.T("already in ") + strings.ToLower(m.mode.String()) + i18n.T(" mode")
	return nil
}

// pendingMode is a mode the agent asked for, applied when the current turn ends.
//
// It cannot be applied where it is asked for: the runner is built around one
// instrument set for the whole turn, so swapping it under a running turn would
// let the model keep calling tools that have just been withdrawn. The tool ends
// the invocation instead, and the switch lands here, between turns.
type pendingMode struct {
	mode agentMode
	// asked is whether there is anything pending at all, kept separate from the
	// mode itself: modeAct is the zero value, so "nothing pending" and "a
	// request to switch to act" would otherwise be the same state.
	asked bool
}

// setModeRequest is the hook the switch tool gets: it records the request, and
// the UI applies it when the turn ends.
//
// Yolo is refused here rather than merely not requested. The switch tool is
// built in plan mode only and can only ask for act, so this cannot fire today —
// but the field is one line wide and a future tool that could ask would
// otherwise hand the user a mode they never pressed a key for, which is the one
// thing the key exists to prevent.
func (m *uiModel) setModeRequest(mode dmagent.Mode, reason string) {
	// The reason is not printed here. The transcript line is written when the
	// switch is applied, not when it is asked for, so a request that is never
	// applied — a mode the model does not have — leaves nothing behind.
	_ = reason
	if agentModeFrom(mode).yolo() {
		return
	}
	m.pendingMode = pendingMode{mode: agentModeFrom(mode), asked: true}
}

// applyPendingMode makes a queued switch real, if there is one.
//
// It runs on the turnDone message and nowhere else. The session id and the
// session service both survive, so the conversation the agent built while
// planning is still there when it comes back with write tools — which is the
// whole point of a switch rather than a restart.
func (m *uiModel) applyPendingMode() tea.Cmd {
	if !m.pendingMode.asked {
		return nil
	}
	want := m.pendingMode.mode
	m.pendingMode = pendingMode{}
	if want == m.mode {
		return nil
	}
	// A switch that lands the session in yolo would leave it in a mode the user
	// never asked for, with the agent no longer able to ask them anything.
	// setModeRequest already refuses one; this is the second door, for the case
	// where a pending request was recorded before that check existed.
	if want.yolo() {
		return nil
	}
	// Coming out of yolo, fall back to the mode the key remembers rather than to
	// the pending one, so a queued request cannot strand the session in a mode
	// the user has to guess their way out of.
	if m.mode.yolo() {
		want = m.beforeYolo
		if want.yolo() {
			want = modeAct
		}
	}
	m.mode = want
	// No transcript line, for the reason in toggleMode.
	return m.rebuildRunner()
}
