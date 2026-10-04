package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"google.golang.org/adk/v2/session"

	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/memsession"
)

// deleteRequest is the ADK delete request the UI sends. Named here so the call
// site reads as what it is rather than as a struct literal in the middle of a
// key handler.
type deleteRequest = session.DeleteRequest

// sessionsState drives the /sessions overlay: a filterable list of the
// conversations on disk, with the current one marked.
type sessionsState struct {
	open     bool
	all      []memsession.Summary
	filtered []int // indexes into all, so the highlight survives filtering
	selected int
	query    string
	// confirmDelete holds the id awaiting a second keypress. Deleting a
	// conversation is not undoable — ctrl+z rewinds a turn, not a session —
	// so the destructive key asks twice.
	confirmDelete string
}

// openSessions lists the saved conversations.
//
// It is a command that returns nothing: the overlay is a view over a list that
// is already in memory, so there is no work to do off the event loop and a
// spinner would only make the list feel slower than it is.
func (m *uiModel) openSessions() tea.Cmd {
	if m.sessions == nil {
		m.statusText = i18n.T("this session has no session store")
		return nil
	}
	m.sessions.Discover(m.ctx, sessionApp, sessionUser)
	m.sessionsList = sessionsState{
		open:  true,
		all:   m.sessions.Summaries(),
		query: "",
	}
	m.sessionsList.refilter()
	if len(m.sessionsList.filtered) == 0 {
		m.sessionsList.open = false
		m.statusText = i18n.T("no saved sessions yet")
		return nil
	}
	return nil
}

// refilter recomputes the visible rows from the query.
func (s *sessionsState) refilter() {
	q := strings.ToLower(strings.TrimSpace(s.query))
	s.filtered = s.filtered[:0]
	for i, sum := range s.all {
		if q == "" ||
			strings.Contains(strings.ToLower(sum.Title), q) ||
			strings.Contains(strings.ToLower(sum.ID), q) {
			s.filtered = append(s.filtered, i)
		}
	}
	if s.selected >= len(s.filtered) {
		s.selected = max(len(s.filtered)-1, 0)
	}
}

// current is the summary under the highlight, or nil when the list is empty.
func (s *sessionsState) current() *memsession.Summary {
	if s.selected < 0 || s.selected >= len(s.filtered) {
		return nil
	}
	return &s.all[s.filtered[s.selected]]
}

// sessionsKey drives the overlay.
//
// Escape closes it from any state, including the delete confirmation: a user
// who reached a destructive prompt by accident must never be trapped in it.
func (m *uiModel) sessionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	list := &m.sessionsList
	switch msg.String() {
	case "esc", "ctrl+p", "q":
		list.open = false
		list.confirmDelete = ""
		return m, nil
	case "ctrl+c":
		return m, m.quitCmd()
	case "up":
		if list.selected > 0 {
			list.selected--
		}
		return m, nil
	case "down":
		if list.selected < len(list.filtered)-1 {
			list.selected++
		}
		return m, nil
	case "backspace":
		if q := []rune(list.query); len(q) > 0 {
			list.query = string(q[:len(q)-1])
			list.refilter()
		}
		return m, nil
	case "d":
		cur := list.current()
		if cur == nil {
			return m, nil
		}
		if list.confirmDelete == cur.ID {
			list.confirmDelete = ""
			m.deleteSession(cur.ID)
			return m, nil
		}
		list.confirmDelete = cur.ID
		return m, nil
	case "enter":
		cur := list.current()
		if cur == nil {
			return m, nil
		}
		list.open = false
		m.switchSession(cur.ID)
		return m, nil
	}
	if len(msg.Text) > 0 {
		list.query += msg.Text
		list.selected = 0
		list.refilter()
	}
	return m, nil
}

// deleteSession removes a conversation from the store and from the list.
func (m *uiModel) deleteSession(id string) {
	if m.sessions == nil || id == "" {
		return
	}
	if err := m.sessions.Delete(m.ctx, &deleteRequest{AppName: sessionApp, UserID: sessionUser, SessionID: id}); err != nil {
		m.history = append(m.history, line{kindErr, i18n.T("could not delete that session: ") + err.Error()})
		m.historyDirty = true
		m.followVP()
		return
	}
	// Deleting the session in use would leave the runner writing to a store that
	// no longer has it, so the UI moves to a fresh one instead.
	if id == m.sessionID {
		m.newSession("")
	}
	m.sessionsList = sessionsState{open: true, all: m.sessions.Summaries()}
	m.sessionsList.refilter()
	if len(m.sessionsList.filtered) == 0 {
		m.sessionsList.open = false
	}
	m.statusText = i18n.T("session deleted")
	m.historyDirty = true
	m.followVP()
}

// sessionsBox renders the list: * marks the current session, the age and the
// message count say what a row is worth before it is opened.
func (m *uiModel) sessionsBox() string {
	list := &m.sessionsList
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, idx := range list.filtered {
		sum := list.all[idx]
		marker, style := "   ", styleHint
		switch {
		case i == list.selected:
			marker, style = " ▸ ", styleTool
		case sum.ID == m.sessionID:
			marker, style = " * ", styleTool
		}
		title := sum.Title
		if strings.TrimSpace(title) == "" {
			title = i18n.T("(untitled)")
		}
		// Formatted rather than passed through i18n.T: the catalog translates
		// whole strings, and a number inside one would need a placeholder the
		// lookup has no way to fill in.
		text := fmt.Sprintf("%s · %s · %d %s", title, relativeTime(sum.Updated), sum.Events, i18n.T("events"))
		rows := wrapIndent(text, inner, marker, "     ")
		if i != list.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	header := i18n.T("? sessions  (* — current, enter — switch, d — delete, esc — close)")
	if list.confirmDelete != "" {
		header = i18n.T("? press d again to delete that session for good")
	}
	return m.floatingPanel(header, list.query, entries, list.selected)
}

// relativeTime renders a timestamp as "3 min ago", degrading to a date once
// distance stops being the interesting part.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return i18n.T("unknown")
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T("just now")
	case d < time.Hour:
		return fmt.Sprintf("%d %s", int(d.Minutes()), i18n.T("min ago"))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d %s", int(d.Hours()), i18n.T("h ago"))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d %s", int(d.Hours()/24), i18n.T("d ago"))
	}
	return t.Format("2006-01-02")
}
