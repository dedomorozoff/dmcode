package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/todo"
)

// planMarks are the three states a step can be in, as the transcript and the
// sidebar draw them. A glyph rather than a word: a plan is read at a glance,
// and three characters do that where three words do not.
var planMarks = map[todo.Status]string{
	todo.Done:       "✓", // ✓
	todo.InProgress: "▸", // ▸
	todo.Pending:    "○", // ○
}

// showPlan prints the current plan into the transcript.
//
// The plan lives in the model, not only on screen, so a user who wants to see
// it mid-task should not have to ask for a summary: /todo is the same list the
// agent is keeping.
func (m *uiModel) showPlan() {
	items := todo.Default.Snapshot()
	if len(items) == 0 {
		m.statusText = i18n.T("the plan is empty")
		m.history = append(m.history, line{kindSys, i18n.T("the plan is empty — the agent has not published one")})
		m.historyDirty = true
		m.followVP()
		return
	}
	p := todo.Default.Progress()
	m.history = append(m.history, line{kindSys, fmt.Sprintf("— %s %d/%d —", i18n.T("plan"), p.Done, p.Total)})
	for _, it := range items {
		mark := planMarks[it.Status]
		if mark == "" {
			mark = planMarks[todo.Pending]
		}
		m.history = append(m.history, line{kindSys, fmt.Sprintf(" %s %d. %s", mark, it.ID, it.Content)})
	}
	m.historyDirty = true
	m.followVP()
}

// planSidebar renders the plan block for the sidebar: the progress line and the
// step in hand.
//
// Only the current step is listed. A fifty-step plan does not fit in a panel
// twenty-four columns wide, and a block cut off mid-list reads as a rendering
// bug rather than as "there is more".
//
// row and value are the sidebar's own writers, passed in so this block is
// measured and wrapped exactly like the sections around it — a block that
// wrapped differently would be a block one column off the panel edge.
func (m *uiModel) planSidebar(row func(lipgloss.Style, string), value func(lipgloss.Style, string, string)) {
	items := todo.Default.Snapshot()
	if len(items) == 0 {
		return
	}
	p := todo.Default.Progress()
	row(styleSidebarLabel, i18n.T("PLAN"))
	row(styleHint, fmt.Sprintf(" %d/%d %s", p.Done, p.Total, i18n.T("done")))
	for _, it := range items {
		if it.Status != todo.InProgress {
			continue
		}
		value(styleTool, " ▸ ", fmt.Sprintf("%d. %s", it.ID, it.Content))
	}
}
