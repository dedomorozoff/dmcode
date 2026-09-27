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
// only reads it and answers with a proposal.
type agentMode int

const (
	modeAct agentMode = iota
	modePlan
)

// agentMode mirrors agent.Mode without making the UI depend on the agent
// package's constant names, so a mode can be compared in a key handler without
// an import cycle through the tool set.
func (m agentMode) agentMode() dmagent.Mode {
	if m == modePlan {
		return dmagent.ModePlan
	}
	return dmagent.ModeAct
}

func (m agentMode) String() string {
	if m == modePlan {
		return "PLAN"
	}
	return "ACT"
}

func (m agentMode) toggled() agentMode {
	if m == modePlan {
		return modeAct
	}
	return modePlan
}

// activeTools is the instrument set the current mode is allowed. Plan mode gets
// the read-only subset, so "read-only" is a property of what the agent can
// reach rather than a promise in the prompt.
func (m *uiModel) activeTools() []tool.Tool {
	if m.mode == modePlan && len(m.readOnlyTools) > 0 {
		return m.readOnlyTools
	}
	return m.tools
}

// activeToolNames is the sidebar's tool list, which must match what the agent can
// actually call — a sidebar promising edit_file in plan mode would be a lie.
func (m *uiModel) activeToolNames() []string {
	if m.mode == modePlan && len(m.readOnlyTools) > 0 {
		return toolNamesOf(m.readOnlyTools)
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
)

// modeBadge renders the current mode for the status bar. It is deliberately
// loud: a mode the user cannot see is a mode they cannot trust, and "the agent
// did not edit" is exactly the kind of thing that has to be visible to be
// believed.
func (m *uiModel) modeBadge() string {
	if m.mode == modePlan {
		return styleBadgePlan.Render(i18n.T("PLAN"))
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
	m.history = append(m.history, line{kindSys, "— " + i18n.T("mode") + ": " + m.mode.String() + " —"})
	m.historyDirty = true
	if len(m.readOnlyTools) == 0 {
		// Nothing to switch to: the session was built without a read-only set,
		// so say so rather than leaving a badge that promises nothing.
		m.statusText = i18n.T("plan mode is unavailable")
		return nil
	}
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
