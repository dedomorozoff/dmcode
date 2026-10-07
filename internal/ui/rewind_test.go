package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/memsession"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// newSessionModel returns a model with a real session store in a temp
// directory, so a rewind can be asserted against the store the runner uses
// rather than against a stub.
func newSessionModel(t *testing.T) *uiModel {
	t.Helper()
	t.Setenv("DMCODE_SESSIONS_DIR", t.TempDir())
	withTempHistory(t)
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.ctx = context.Background()
	m.sessions = memsession.NewPersistent(config.SessionsDir())
	m.sessions.SetIdentity(sessionApp, sessionUser)
	if id, err := m.sessions.Ensure(m.ctx, sessionApp, sessionUser, m.sessionID); err == nil {
		m.sessionID = id
	}
	m.svc = m.sessions
	return m
}

// sendTurn appends a prompt to the transcript and to the store, the way the
// enter handler does, so a test exercises the same marks the UI records.
func sendTurn(t *testing.T, m *uiModel, prompt string) {
	t.Helper()
	sess, err := m.sessions.Get(m.ctx, &session.GetRequest{
		AppName: sessionApp, UserID: sessionUser, SessionID: m.sessionID,
	})
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	ev := session.NewEvent(m.ctx, "inv")
	ev.Author = "user"
	ev.Content = genai.NewContentFromText(prompt, genai.RoleUser)
	if err := m.sessions.AppendEvent(m.ctx, sess.Session, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	reply := session.NewEvent(m.ctx, "inv")
	reply.Author = "dmcode"
	reply.Content = genai.NewContentFromText("ответ: "+prompt, genai.RoleModel)
	if err := m.sessions.AppendEvent(m.ctx, sess.Session, reply); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	m.promptMarks = append(m.promptMarks, promptMark{text: prompt, idx: len(m.history)})
	m.history = append(m.history, line{kindUser, prompt})
	m.history = append(m.history, line{kindAgent, "ответ: " + prompt})
	m.turnCount++
	m.savePrompt(prompt)
}

// TestRewindPutsThePromptBackAndCutsBothHistories is the contract of ctrl+z:
// the prompt returns to the input, the transcript loses the exchange, and the
// model's memory is cut at the same place.
func TestRewindPutsThePromptBackAndCutsBothHistories(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "первый")
	sendTurn(t, m, "второй")
	before := len(m.history)

	m.rewind()

	if got := m.input.Value(); got != "второй" {
		t.Errorf("input = %q, want %q", got, "второй")
	}
	if len(m.history) >= before {
		t.Errorf("transcript has %d lines, want fewer than %d", len(m.history), before)
	}
	for _, l := range m.history {
		if l.text == "ответ: второй" {
			t.Error("the rewound reply is still in the transcript")
		}
	}
	tr, err := m.sessions.TranscriptOf(sessionApp, sessionUser, m.sessionID, 10)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 1 || tr.Turns[0].User != "первый" {
		t.Errorf("the model's memory = %+v, want only the first exchange", tr.Turns)
	}
}

// TestRewindAgainGoesBackOneMoreTurn: the key is a repeatable undo, so a
// second press must undo the turn before it.
func TestRewindAgainGoesBackOneMoreTurn(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "первый")
	sendTurn(t, m, "второй")

	m.rewind()
	if got := m.input.Value(); got != "второй" {
		t.Fatalf("after the first rewind input = %q, want %q", got, "второй")
	}
	m.rewind()
	if got := m.input.Value(); got != "первый" {
		t.Errorf("after the second rewind input = %q, want %q", got, "первый")
	}
}

// TestRewindIsRefusedDuringATurn: cutting underneath a running turn would leave
// the runner and the screen disagreeing about what happened.
func TestRewindIsRefusedDuringATurn(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "вопрос")
	m.busy = true

	m.rewind()

	if got := m.input.Value(); got != "" {
		t.Errorf("input = %q, want it untouched during a turn", got)
	}
	tr, _ := m.sessions.TranscriptOf(sessionApp, sessionUser, m.sessionID, 10)
	if len(tr.Turns) != 1 {
		t.Errorf("the session was rewound mid-turn: %+v", tr.Turns)
	}
}

// TestRewindWithNothingToUndoSaysSo: a key that does nothing must say so rather
// than leave the user pressing it again.
func TestRewindWithNothingToUndoSaysSo(t *testing.T) {
	m := newSessionModel(t)
	m.rewind()
	if m.statusText == "" {
		t.Error("rewinding an empty session left no message in the status line")
	}
}

// TestRewindDropsThePromptFromRecall: offering the same prompt straight after
// an undo would walk the user in circles.
func TestRewindDropsThePromptFromRecall(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "вопрос")
	m.rewind()

	for _, p := range m.promptHistory {
		if p == "вопрос" {
			t.Error("the rewound prompt is still offered by the recall list")
		}
	}
}

// pressKey builds a key press for a key name, so a test can drive the overlay
// handlers directly instead of going through the whole Update. It is not called
// key because the package imports charm.land/bubbles/v2/key, and a test helper
// sharing a package's name is a collision waiting for the next test that wants
// the import.
func pressKey(name string) tea.KeyPressMsg {
	switch name {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: rune(name[0]), Text: name}
}

// TestNewSessionKeepsTheOldOne: a /new pressed by accident has to be
// recoverable, or starting a new conversation is a one-way door.
func TestNewSessionKeepsTheOldOne(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "старая работа")
	old := m.sessionID

	m.newSession("")

	if m.sessionID == old {
		t.Fatal("newSession did not change the session id")
	}
	tr, err := m.sessions.TranscriptOf(sessionApp, sessionUser, old, 10)
	if err != nil {
		t.Fatalf("the previous session is gone: %v", err)
	}
	if len(tr.Turns) != 1 {
		t.Errorf("the previous session lost its content: %+v", tr.Turns)
	}
}

// TestNewSessionNamesItFromTheArgument: /new <name> is how a user labels a
// conversation they intend to come back to.
//
// The name is checked on a session that has a message in it, because a session
// with nothing in it is left out of the list on purpose — a row with a name and
// no conversation behind it is a row that leads nowhere.
func TestNewSessionNamesItFromTheArgument(t *testing.T) {
	m := newSessionModel(t)
	m.newSession("рефакторинг парсера")
	sendTurn(t, m, "с чего начать")

	sums := m.sessions.Summaries()
	var found bool
	for _, s := range sums {
		if s.ID == m.sessionID {
			found = true
			if s.Title != "рефакторинг парсера" {
				t.Errorf("title = %q, want the name given to /new", s.Title)
			}
		}
	}
	if !found {
		t.Error("the named session is not in the list")
	}
}

// TestSwitchSessionMovesAndSummarises: switching without showing any of the
// conversation would leave the user looking at a blank screen next to a model
// that remembers everything.
func TestSwitchSessionMovesAndSummarises(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "первая сессия")
	old := m.sessionID
	m.newSession("")
	sendTurn(t, m, "вторая сессия")

	m.switchSession(old)

	if m.sessionID != old {
		t.Errorf("sessionID = %q, want %q", m.sessionID, old)
	}
	var seen bool
	for _, l := range m.history {
		if l.text == "первая сессия" || l.text == "ответ: первая сессия" {
			seen = true
		}
	}
	if !seen {
		t.Error("the switch printed nothing from the session it opened")
	}
}

// TestSwitchSessionIsRefusedDuringATurn: moving the id under a running turn
// would send the rest of that turn into a different history.
func TestSwitchSessionIsRefusedDuringATurn(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "работа")
	old := m.sessionID
	m.busy = true

	m.switchSession("some-other-session")

	if m.sessionID != old {
		t.Errorf("sessionID = %q, want it unchanged during a turn", m.sessionID)
	}
	if m.statusText == "" {
		t.Error("the refused switch said nothing")
	}
}

// TestSwitchResetsWhatBelongedToTheOldSession: the marks a rewind would cut at
// belong to the conversation that was left, so carrying them over would undo
// the wrong turn.
func TestSwitchResetsWhatBelongedToTheOldSession(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "первая")
	old := m.sessionID
	m.newSession("")
	sendTurn(t, m, "вторая")
	if len(m.promptMarks) == 0 {
		t.Fatal("the new session has no marks to clear")
	}

	m.switchSession(old)

	if len(m.promptMarks) != 0 {
		t.Errorf("%d marks survived the switch, want 0", len(m.promptMarks))
	}
	if m.turnCount != 0 {
		t.Errorf("turnCount = %d, want 0 after a switch", m.turnCount)
	}
}

// TestSessionsOverlayListsSavedConversations: the list is how a user gets back
// to anything, so it has to show what is actually there.
func TestSessionsOverlayListsSavedConversations(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "про первый проект")
	m.newSession("")
	sendTurn(t, m, "про второй проект")

	m.openSessions()

	if !m.sessionsList.open {
		t.Fatal("the sessions overlay did not open")
	}
	if len(m.sessionsList.filtered) != 2 {
		t.Fatalf("the list shows %d sessions, want 2", len(m.sessionsList.filtered))
	}
	box := m.sessionsBox()
	if box == "" {
		t.Error("the overlay rendered nothing")
	}
}

// TestSessionsOverlayFiltersByTitle: a list of a hundred conversations is only
// usable if it can be searched.
func TestSessionsOverlayFiltersByTitle(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "alpha работа")
	m.newSession("")
	sendTurn(t, m, "beta работа")

	m.openSessions()
	m.sessionsList.query = "beta"
	m.sessionsList.refilter()

	if len(m.sessionsList.filtered) != 1 {
		t.Fatalf("filtering by \"beta\" left %d rows, want 1", len(m.sessionsList.filtered))
	}
	cur := m.sessionsList.current()
	if cur == nil || cur.Title != "beta работа" {
		t.Errorf("the surviving row is %+v, want the beta session", cur)
	}
}

// TestDeleteAsksTwice: deleting a conversation cannot be undone — ctrl+z
// rewinds a turn, not a session — so the destructive key is asked for twice.
func TestDeleteAsksTwice(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "единственная")
	only := m.sessionID
	m.openSessions()
	m.sessionsList.list.sel = 0

	m.sessionsKey(pressKey("d"))
	if m.sessionsList.confirmDelete != only {
		t.Fatal("the first d did not ask for confirmation")
	}
	if len(m.sessions.Summaries()) != 1 {
		t.Error("the session was deleted on the first d")
	}

	m.sessionsKey(pressKey("d"))
	if len(m.sessions.Summaries()) != 0 {
		t.Error("the session survived the confirmed delete")
	}
	if m.sessionID == only {
		t.Error("the deleted session is still the current one")
	}
}

// TestEscapeLeavesTheDeleteConfirmation: a user who reached a destructive
// prompt by accident must never be trapped in it.
func TestEscapeLeavesTheDeleteConfirmation(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "единственная")
	m.openSessions()
	m.sessionsList.list.sel = 0
	m.sessionsKey(pressKey("d"))

	m.sessionsKey(pressKey("esc"))

	if m.sessionsList.open {
		t.Error("escape did not close the overlay")
	}
	if m.sessionsList.confirmDelete != "" {
		t.Error("escape left the delete confirmation armed")
	}
	if len(m.sessions.Summaries()) != 1 {
		t.Error("escape deleted the session")
	}
}

// TestRewindReportsShellChangedFilesNotRestored is the second half of the
// rewind contract: ctrl+z restores what the tools have before-content for, and
// says loudly about what a shell command changed that nobody can take back. The
// transcript must carry that line, or a rewind that left a sed -i result in the
// tree would read as a rewind that undid it.
func TestRewindReportsShellChangedFilesNotRestored(t *testing.T) {
	m := newSessionModel(t)
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dmtools.BeginTurn()
	dmtools.RecordShellChange(p)
	sendTurn(t, m, "правка через шелл")

	m.rewind()

	var saw bool
	for _, l := range m.history {
		if strings.Contains(l.text, "not restored") && strings.Contains(l.text, "f.txt") {
			saw = true
		}
	}
	if !saw {
		t.Errorf("the rewind did not report the unrestorable file, history:\n%+v", m.history)
	}
	if b, _ := os.ReadFile(p); string(b) != "content\n" {
		t.Errorf("a shell-touched file was modified by the rewind: %q", b)
	}
}

// TestShortListCapsAndCountsTheOverflow: a rewind that restored two hundred
// files must say "+196" rather than wrap the screen with absolute paths.
func TestShortListCapsAndCountsTheOverflow(t *testing.T) {
	got := shortList([]string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"}, 3)
	if !strings.Contains(got, "… +2") {
		t.Errorf("shortList = %q, want the overflow counted", got)
	}
	if strings.Count(got, ".txt") != 3 {
		t.Errorf("shortList = %q, want only the first three named", got)
	}
	if got := shortList([]string{"one.go"}, 3); got != "one.go" {
		t.Errorf("a short list changed shape: %q", got)
	}
}

// TestShortPathPrefersWorkspaceRelative: inside the root a path is shown as
// the user thinks of it, not as an absolute surprise.
func TestShortPathPrefersWorkspaceRelative(t *testing.T) {
	dir := t.TempDir()
	if err := dmtools.SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	rel := shortPath(filepath.Join(dir, "src", "main.go"))
	if rel != filepath.Join("src", "main.go") {
		t.Errorf("shortPath = %q, want the workspace-relative form", rel)
	}
}
