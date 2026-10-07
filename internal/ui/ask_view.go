package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// askState drives the choice overlay: the question, the rows, and the text field
// behind the "write your own" row.
type askState struct {
	open   bool
	req    ask.Request
	cursor int
	// checked holds the ticked rows. It is a set of indexes rather than a count
	// so unticking the cursor row works.
	checked map[int]bool
	// customOpen is the text field for an answer that is not on the list, which
	// is the whole point of offering one.
	customOpen bool
	customBuf  string
	// reply is where the answer goes. It is buffered by one so the tool's
	// goroutine is released whether or not anyone is still waiting by the time
	// the answer is sent.
	reply chan ask.Answer
	// deadline is when the timer runs out, zero when it is off. The countdown
	// is drawn from it, so the user watches the same number the broker uses.
	deadline time.Time
}

// askRequestMsg arrives when the agent asks something. It carries the channel
// the answer must go back on, so the tool's blocked goroutine and the overlay
// meet on the event loop.
type askRequestMsg struct {
	req   ask.Request
	reply chan ask.Answer
	wait  time.Duration
}

// askTickMsg drives the countdown.
type askTickMsg struct{}

// subAgentMsg is a progress note from inside a delegation, so a sub-agent that
// reads forty files for a minute does not look like a frozen turn.
type subAgentMsg struct{ ev dmagent.SubEvent }

// handleSubAgent records what a delegation is doing.
//
// The first note of a task carries the task itself, and the rest are indented
// under it, so the transcript reads as a nested block rather than as a list of
// unrelated tool calls.
func (m *uiModel) handleSubAgent(ev dmagent.SubEvent) {
	if strings.TrimSpace(ev.Task) == "" {
		return
	}
	// The task is shown once, on its "started" note; after that the rows are
	// just what it is doing, and repeating the task on every line would bury
	// the progress it is reporting.
	if ev.Text == "started" {
		m.history = append(m.history, line{kindTool, "⚙ " + i18n.T("sub-agent") + ": " + ev.Task})
		m.historyDirty = true
		m.followVP()
		return
	}
	m.statusText = i18n.T("sub-agent: ") + ev.Text
	m.history = append(m.history, line{kindToolRes, "  ↳ " + ev.Text})
	m.historyDirty = true
	m.followVP()
}

// openAsk puts the question on screen and returns the command that keeps the
// countdown moving.
func (m *uiModel) openAsk(msg askRequestMsg) tea.Cmd {
	m.ask = askState{
		open:     true,
		req:      msg.req,
		checked:  make(map[int]bool),
		reply:    msg.reply,
		deadline: time.Time{},
	}
	if msg.wait > 0 {
		m.ask.deadline = time.Now().Add(msg.wait)
	}
	m.statusText = i18n.T("waiting for your answer")
	// The countdown only ticks when there is a deadline: with the timer off a
	// tick would wake the event loop once a second for nothing.
	if m.ask.deadline.IsZero() {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return askTickMsg{} })
}

// answer collects the current selection and hands it back to the waiting tool.
//
// The send does not block: the tool's goroutine may already have given up on a
// cancelled turn, and a send waiting for a reader that will never come would
// hang the event loop instead of the turn.
func (m *uiModel) answer(a ask.Answer) {
	reply := m.ask.reply
	m.ask = askState{}
	if reply != nil {
		select {
		case reply <- a:
		default:
		}
	}
}

// answerCurrent builds the answer from what is ticked and typed.
func (m *uiModel) answerCurrent() ask.Answer {
	a := ask.Answer{}
	if custom := strings.TrimSpace(m.ask.customBuf); custom != "" {
		a.Custom = custom
	}
	// The rows are walked in order rather than in map order, so the answer reads
	// the way the question was asked.
	for i, o := range m.ask.req.Options {
		if m.ask.checked[i] {
			a.Selected = append(a.Selected, o.Label)
		}
	}
	if len(a.Selected) == 0 && m.ask.cursor < len(m.ask.req.Options) {
		// Nothing ticked means the highlighted row, in a multi question as much
		// as in a single one. Enter always answers with the row the user is
		// looking at; the alternative is a question they can only answer by
		// ticking something they have just deliberately unticked.
		a.Selected = []string{m.ask.req.Options[m.ask.cursor].Label}
	}
	return a
}

// skipAsk closes the overlay without an answer, which is what escape means.
func (m *uiModel) skipAsk() {
	m.history = append(m.history, line{kindSys, "⏭ " + i18n.T("question skipped — the agent will carry on")})
	m.historyDirty = true
	m.answer(ask.Answer{Skipped: true})
	m.statusText = i18n.T("ready")
}

// customRow is the index one past the last option — the row that opens the text
// field. It is a row rather than a key because a key nobody thinks of is a key
// nobody presses.
func (m *uiModel) customRow() int {
	if m.ask.req.AllowCustom {
		return len(m.ask.req.Options)
	}
	return -1
}

// askKey drives the overlay.
func (m *uiModel) askKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	opts := m.ask.req.Options
	cr := m.customRow()
	last := len(opts) - 1
	if cr >= 0 {
		last = cr
	}
	switch msg.String() {
	case "ctrl+c":
		// The turn is not cancelled here: escape already reaches the tool through
		// the turn's context, and quitting is the one thing that must always work.
		return m, m.quitCmd()
	case "esc":
		if m.ask.customOpen {
			// Escape backs out of the text field first: a user who opened it by
			// accident must not have to close the whole question to get out.
			m.ask.customOpen = false
			m.ask.customBuf = ""
			return m, nil
		}
		m.skipAsk()
		return m, nil
	case "up":
		if m.ask.cursor > 0 {
			m.ask.cursor--
		}
		return m, nil
	case "down":
		if m.ask.cursor < last {
			m.ask.cursor++
		}
		return m, nil
	case "space":
		if m.ask.cursor >= len(opts) {
			return m, nil // the custom row is opened by enter, not ticked
		}
		if !m.ask.req.Multi {
			// Single choice: ticking a row replaces the tick, so space means
			// "this one" rather than "and this one too".
			m.ask.checked = map[int]bool{m.ask.cursor: true}
			return m, nil
		}
		if m.ask.checked[m.ask.cursor] {
			delete(m.ask.checked, m.ask.cursor)
		} else {
			m.ask.checked[m.ask.cursor] = true
		}
		return m, nil
	case "c":
		// The same row the user can reach with the cursor, bound to a key as
		// well: a shortcut nobody discovers is still a shortcut.
		if cr >= 0 && !m.ask.customOpen {
			m.ask.customOpen = true
			return m, nil
		}
	case "enter":
		if cr >= 0 && m.ask.cursor == cr && !m.ask.customOpen {
			m.ask.customOpen = true
			return m, nil
		}
		if m.ask.customOpen {
			if strings.TrimSpace(m.ask.customBuf) == "" {
				// An empty custom field confirms nothing and goes back, rather
				// than answering the question with a blank.
				m.ask.customOpen = false
				return m, nil
			}
		}
		m.answer(m.answerCurrent())
		return m, nil
	case "backspace":
		if m.ask.customOpen {
			if r := []rune(m.ask.customBuf); len(r) > 0 {
				m.ask.customBuf = string(r[:len(r)-1])
			}
		}
		return m, nil
	}
	if m.ask.customOpen && len(msg.Text) > 0 {
		m.ask.customBuf += msg.Text
	}
	return m, nil
}

// handleAskTick advances the countdown, and closes the overlay when the time is
// up. The broker has already chosen by then, on its own side of the same
// deadline, so the overlay only records which option it was — a question still
// on screen after the timer has fired looks unanswered.
func (m *uiModel) handleAskTick() tea.Cmd {
	if !m.ask.open || m.ask.deadline.IsZero() {
		return nil
	}
	if time.Now().Before(m.ask.deadline) {
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return askTickMsg{} })
	}
	// The same preference order the broker applies, so the transcript does not
	// claim a different option than the model is about to be told.
	chosen := ""
	for _, o := range m.ask.req.Options {
		if o.Recommended {
			chosen = o.Label
			break
		}
	}
	if chosen == "" && len(m.ask.req.Options) > 0 {
		chosen = m.ask.req.Options[0].Label
	}
	m.history = append(m.history, line{kindSys,
		fmt.Sprintf("%s: %s", i18n.T("no answer in time, chose"), chosen)})
	m.historyDirty = true
	m.ask = askState{}
	return nil
}

// askRemaining is how long the overlay has left, zero when the timer is off.
func (m *uiModel) askRemaining() time.Duration {
	if !m.ask.open || m.ask.deadline.IsZero() {
		return 0
	}
	left := time.Until(m.ask.deadline)
	if left <= 0 {
		return 0
	}
	// Rounded up: a countdown showing 0s while a second is still running reads
	// as expired a second early.
	return left.Truncate(time.Second) + time.Second
}

// askBox renders the question: the options with their notes, the custom row, and
// the hint line with the countdown.
func (m *uiModel) askBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	style := func(i int, marker, text string) []string {
		rows := wrapIndent(text, inner, marker, "     ")
		if i != m.ask.cursor {
			for j := range rows {
				rows[j] = styleHint.Render(rows[j])
			}
		}
		return rows
	}
	for i, o := range m.ask.req.Options {
		marker := "   "
		switch {
		case i == m.ask.cursor:
			marker = " ▸ "
		case m.ask.checked[i]:
			marker = " ✓ "
		}
		text := o.Label
		if o.Note != "" {
			text += "  — " + o.Note
		}
		if o.Recommended {
			text += "  " + i18n.T("(recommended)")
		}
		entries = append(entries, style(i, marker, text))
	}
	if cr := m.customRow(); cr >= 0 {
		label := i18n.T("write your own answer")
		if m.ask.customOpen {
			label = m.ask.customBuf + "▏"
		}
		marker := "   "
		if cr == m.ask.cursor {
			marker = " ▸ "
		}
		entries = append(entries, style(cr, marker, label))
	}

	header := i18n.T("? choose one")
	if m.ask.req.Multi {
		header = i18n.T("? choose one or more")
	}
	hints := i18n.T("enter — confirm · space — tick · esc — skip")
	if m.ask.req.AllowCustom {
		hints = i18n.T("enter — confirm · space — tick · c — your own · esc — skip")
	}
	if m.ask.customOpen {
		hints = i18n.T("enter — send · esc — back")
	}
	if left := m.askRemaining(); left > 0 {
		hints = fmt.Sprintf("%s · %s", hints, formatWait(left))
	}
	title := header
	if q := strings.TrimSpace(m.ask.req.Question); q != "" {
		title += "\n" + q
	}
	return m.floatingPanel(title, hints, entries, &listView{sel: m.ask.cursor})
}
