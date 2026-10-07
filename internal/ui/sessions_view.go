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
	list     listView
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
	if s.list.sel >= len(s.filtered) {
		s.list.sel = max(len(s.filtered)-1, 0)
	}
}

// current is the summary under the highlight, or nil when the list is empty.
func (s *sessionsState) current() *memsession.Summary {
	if s.list.sel < 0 || s.list.sel >= len(s.filtered) {
		return nil
	}
	return &s.all[s.filtered[s.list.sel]]
}

// sessionsKey drives the overlay.
//
// Escape closes it from any state, including the delete confirmation: a user
// who reached a destructive prompt by accident must never be trapped in it.
func (m *uiModel) sessionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.sessionsList
	switch msg.String() {
	case "esc", "ctrl+p", "q":
		s.open = false
		s.confirmDelete = ""
		return m, nil
	case "ctrl+c":
		return m, m.quitCmd()
	case "up":
		if s.list.sel > 0 {
			s.list.sel--
		}
		return m, nil
	case "down":
		if s.list.sel < len(s.filtered)-1 {
			s.list.sel++
		}
		return m, nil
	case "pgup":
		s.list.sel = max(s.list.sel-m.floatPage(), 0)
		return m, nil
	case "pgdown":
		s.list.sel = min(s.list.sel+m.floatPage(), len(s.filtered)-1)
		return m, nil
	case "home":
		s.list.sel = 0
		return m, nil
	case "end":
		s.list.sel = max(len(s.filtered)-1, 0)
		return m, nil
	case "backspace":
		if q := []rune(s.query); len(q) > 0 {
			s.query = string(q[:len(q)-1])
			s.list = listView{}
			s.refilter()
		}
		return m, nil
	case "d":
		cur := s.current()
		if cur == nil {
			return m, nil
		}
		if s.confirmDelete == cur.ID {
			s.confirmDelete = ""
			m.deleteSession(cur.ID)
			return m, nil
		}
		s.confirmDelete = cur.ID
		return m, nil
	case "enter":
		cur := s.current()
		if cur == nil {
			return m, nil
		}
		s.open = false
		m.switchSession(cur.ID)
		return m, nil
	}
	if len(msg.Text) > 0 {
		s.query += msg.Text
		s.list = listView{}
		s.refilter()
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
	s := &m.sessionsList
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, idx := range s.filtered {
		sum := s.all[idx]
		marker, style := "   ", styleHint
		switch {
		case i == s.list.sel:
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
		if i != s.list.sel {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	header := i18n.T("? sessions  (* — current, enter — switch, d — delete, esc — close)")
	if s.confirmDelete != "" {
		header = i18n.T("? press d again to delete that session for good")
	}
	return m.floatingPanel(header, s.query, entries, &s.list)
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
