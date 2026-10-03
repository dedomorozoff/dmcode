package ui

import (
	"errors"
	"strings"

	"google.golang.org/adk/v2/session"

	"github.com/dedomorozoff/dmcode/internal/i18n"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// sessionApp and sessionUser are the ADK session identity. They are constants
// because dmcode is a single-user local tool: the triple exists to key the
// store, not to model tenancy.
const (
	sessionApp  = "dmcode"
	sessionUser = "user"
)

// sessionSummaryTurns is how many exchanges a switch prints. Enough to place
// the user in the conversation, few enough that the screen is not buried.
const sessionSummaryTurns = 5

// rewind undoes the last turn: the prompt comes back to the input, the
// transcript loses the exchange, and the model's memory is cut at the same
// place.
//
// The two cuts have to happen together. Truncating only the transcript would
// leave the model answering a question the user can no longer see; truncating
// only the session would leave the user reading a reply that, from the model's
// side, was never produced. The mark recorded when the prompt was sent is what
// keeps them aligned.
func (m *uiModel) rewind() {
	if m.busy {
		// A turn in flight is appending to both histories right now. Cutting
		// underneath it would leave the runner and the screen disagreeing about
		// what happened, so the user is asked to stop the turn first.
		m.statusText = i18n.T("wait for the turn to finish before rewinding")
		return
	}
	if m.sessions == nil {
		m.statusText = i18n.T("rewind needs a session store, which this session has none of")
		return
	}
	if len(m.promptMarks) == 0 {
		m.statusText = i18n.T("nothing to rewind to")
		return
	}

	mark := m.promptMarks[len(m.promptMarks)-1]
	text, ok, err := m.sessions.RewindToLastUserMessage(m.ctx, sessionApp, sessionUser, m.sessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			m.statusText = i18n.T("the current session is no longer on disk")
		} else {
			m.statusText = i18n.T("rewind failed: ") + err.Error()
		}
		m.history = append(m.history, line{kindErr, m.statusText})
		m.historyDirty = true
		m.followVP()
		return
	}
	if !ok {
		// The mark and the store disagree: a prompt was sent that never reached
		// the session. Cutting the transcript alone would be a guess, so both
		// are left alone and the user is told.
		m.statusText = i18n.T("nothing to rewind to")
		m.history = append(m.history, line{kindErr, m.statusText})
		m.historyDirty = true
		m.followVP()
		return
	}
	// The store is the authority on what the last prompt was: it read the text
	// from the event, which is what the model actually received.
	prompt := text
	if strings.TrimSpace(prompt) == "" {
		prompt = mark.text
	}

	m.promptMarks = m.promptMarks[:len(m.promptMarks)-1]
	// The mark's idx is where the prompt line was appended, so truncating to it
	// removes the prompt and everything the turn produced after it. Anything
	// above — a /new notice, an earlier turn — stays. The previews were appended
	// before the mark, so they go with the prompt that sent them rather than
	// staying on screen describing a turn that no longer exists.
	if mark.idx <= len(m.history) {
		m.history = m.history[:mark.idx]
	}
	// The pictures come back with the prompt. A turn sent as nothing but a
	// screenshot has no text for the store to hand back, so without this the user
	// would find an empty input box and have to go and find the file again.
	if len(mark.images) > 0 {
		// The strip comes back rather than the transcript line: a rewind un-sends
		// the turn, so the pictures return to being undecided, and the strip above
		// the input is where an undecided picture lives. Putting them back into the
		// transcript would show a conversation entry for a turn that no longer
		// happened.
		//
		// They come back with fromInput false: the path is no longer in the input
		// — the prompt went with the rewind — so editing the text must not be able
		// to take them away.
		for _, a := range mark.images {
			m.pending = append(m.pending, pendingImage{Attachment: a})
		}
	}
	m.turnCount--
	// The recall list offered the prompt as something already sent; offering it
	// again straight after an undo would walk the user in circles.
	m.dropLastPrompt()

	m.input.SetValue(prompt)
	m.input.CursorEnd()
	m.history = append(m.history, line{kindSys, "↩ " + i18n.T("rolled back to the previous message")})
	m.historyDirty = true
	m.statusText = i18n.T("the message is back in the input — edit it and send again")
	// A picture that came back with the prompt reappears in the pending strip, and
	// the strip needs rows the transcript no longer has. Resized before the
	// viewport is re-synced, or the frame ends up taller than the terminal.
	m.layout()
	m.followVP()
}

// dropLastPrompt removes the most recent prompt from the in-memory recall list.
//
// The on-disk history is deliberately left alone: rewriting an append-only
// jsonl to erase one line is a different operation from appending to it, and a
// prompt the user rewound once is still a prompt they sent. The cost is that
// /history and ↑ can still offer it until the next start.
func (m *uiModel) dropLastPrompt() {
	if n := len(m.promptHistory); n > 0 {
		m.promptHistory = m.promptHistory[:n-1]
	}
	if m.histPos > len(m.promptHistory) {
		m.histPos = len(m.promptHistory)
	}
}

// clearScreen empties the transcript.
//
// It is one method because three things now do the same thing: /clear, the
// palette entry and ctrl+l. They used to be written out separately, and the
// palette's copy had already drifted — it forgot followVP, so clearing from
// ctrl+p left the viewport scrolled past the bottom of an empty transcript.
//
// The session is deliberately untouched. "Clear the screen" is about what is
// on it; wiping the conversation behind it is /new's job, and conflating the
// two is how a user loses a transcript they meant to keep.
func (m *uiModel) clearScreen() {
	m.history = nil
	m.historyDirty = true
	m.followVP()
}

// newSession starts a fresh conversation and leaves the old one on disk.
//
// The previous session is not discarded: /sessions is what makes it reachable
// again, so a user who starts a new chat by accident gets it back with one
// keypress instead of having lost it.
func (m *uiModel) newSession(title string) {
	prev := m.sessionID
	m.sessionID = newSessionID()
	m.turnCount = 0
	m.toolCallCount = 0
	m.promptMarks = nil
	// An attachment belongs to the prompt it was going to ride with, and /new is
	// a different conversation. Carrying it over would attach the previous
	// session's screenshot to the first message of this one.
	m.pending = nil
	// The change tally belongs to the session that produced it. Carrying the
	// numbers into a fresh session would report edits the new one never made,
	// against files it has never opened.
	dmtools.ResetChanges()
	// The workspace's tabs belong to the session that opened them: a new
	// conversation starting from the previous one's open files reads as the
	// old session continuing. The change tally reset above is what makes the
	// tabs stop coming back on the next ctrl+e; this is what clears the ones
	// already open.
	if m.ed != nil {
		m.ed.CloseAllTabs()
	}
	if m.sessions != nil {
		if id, err := m.sessions.Ensure(m.ctx, sessionApp, sessionUser, m.sessionID); err == nil {
			m.sessionID = id
		} else {
			m.statusText = i18n.T("could not start a new session: ") + err.Error()
		}
		if name := strings.TrimSpace(title); name != "" {
			m.sessions.Rename(sessionApp, sessionUser, m.sessionID, name)
		}
	}
	label := i18n.T("session reset")
	if prev != "" && prev != m.sessionID {
		label = i18n.T("new session") + " — " + prev + " " + i18n.T("is kept, see /sessions")
	}
	m.history = append(m.history, line{kindSys, label})
	m.historyDirty = true
	// Dropping the attachments empties the pending strip, and the rows it held go
	// back to the transcript.
	m.layout()
	m.followVP()
}

// switchSession moves the session to another conversation on disk.
//
// The runner is not rebuilt: it is handed the session id on every turn, so
// pointing m.sessionID at another id is the whole switch. What does have to be
// reset is what the UI accumulated for the old one — the marks a rewind would
// cut at, and the counters the sidebar reports.
func (m *uiModel) switchSession(id string) {
	if m.busy {
		// The same reason a mode switch is refused mid-turn: the runner is
		// mid-conversation, and moving the id under it would send the rest of
		// the turn into a different history.
		m.statusText = i18n.T("wait for the turn to finish before switching sessions")
		return
	}
	if m.sessions == nil {
		m.statusText = i18n.T("this session has no session store")
		return
	}
	id = strings.TrimSpace(id)
	if id == "" || id == m.sessionID {
		return
	}
	// Discover returns a count rather than an error, and reports what went
	// wrong through LastWriteError, so there is nothing to check here.
	m.sessions.Discover(m.ctx, sessionApp, sessionUser)

	prev := m.sessionID
	m.sessionID = id
	m.promptMarks = nil
	m.pending = nil
	m.turnCount = 0
	m.toolCallCount = 0
	m.lastTool = ""
	// The workspace's tabs belong to the conversation that opened them; a
	// resumed one starts from its own files, not the previous session's.
	if m.ed != nil {
		m.ed.CloseAllTabs()
	}
	m.history = append(m.history, line{kindSys, i18n.T("switched session from ") + prev})
	m.summariseSession(id)
	m.statusText = i18n.T("session: ") + id
	m.historyDirty = true
	m.layout()
	m.followVP()
}

// summariseSession prints the tail of another conversation into the transcript.
//
// Switching without it would show an empty screen next to a model that
// remembers everything, which reads as a bug rather than as a switch. A few
// exchanges are enough to place the user; holding the whole history is the
// model's job, not the screen's to print.
func (m *uiModel) summariseSession(id string) {
	tr, err := m.sessions.TranscriptOf(sessionApp, sessionUser, id, sessionSummaryTurns)
	if err != nil {
		m.history = append(m.history, line{kindErr, i18n.T("could not read that session: ") + err.Error()})
		m.historyDirty = true
		m.followVP()
		return
	}
	if tr.Title != "" {
		m.history = append(m.history, line{kindSys, "— " + tr.Title + " —"})
	}
	if tr.Truncated {
		m.history = append(m.history, line{kindSys, i18n.T("only the last events of that session were loaded")})
	}
	if len(tr.Turns) == 0 {
		m.history = append(m.history, line{kindSys, i18n.T("that session has no messages yet")})
	}
	for _, t := range tr.Turns {
		m.history = append(m.history, line{kindUser, truncate(t.User, 400)})
		if t.Broken {
			// Marked rather than omitted: a prompt with no answer is a fact
			// about the session, and printing it bare would look like a bug.
			m.history = append(m.history, line{kindSys, i18n.T("(no answer — that turn was cut short)")})
			continue
		}
		m.history = append(m.history, line{kindAgent, truncate(t.Agent, 800)})
	}
	m.historyDirty = true
	m.followVP()
}

// reportStoreProblem surfaces a failed session write once, in the transcript
// and the status line, and then forgets it — a store that cannot be written
// would otherwise repeat the same complaint on every token of every turn.
func (m *uiModel) reportStoreProblem() {
	if m.sessions == nil {
		return
	}
	err := m.sessions.LastWriteError()
	if err == nil {
		return
	}
	m.sessions.ClearWriteError()
	m.statusText = i18n.T("sessions are not being saved: ") + err.Error()
	m.history = append(m.history, line{kindErr,
		i18n.T("this session could not be written to disk: ") + err.Error()})
	m.historyDirty = true
}
