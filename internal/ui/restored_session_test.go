package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/memsession"
)

// Restoring a session used to bring back the conversation without the code.
//
// summariseSession printed Turn.User and Turn.Agent, and the store's
// TranscriptOf had thrown the tool calls, the results and every change block away
// before it got there — so a session resumed into a chat that looked like the work
// had never happened. These tests drive the real store through the real switch and
// read the transcript that comes out.

const restoredWriteDiff = "── main.go\n-2: one\n+2: ONE"

// editArgs is the argument map the restored turn's edit_file call carries, kept in
// one place so the test can build the same call and compare the two lines.
var editArgs = map[string]any{
	"path": "main.go", "old_string": "one", "new_string": "ONE",
}

// sessionWithAWrite is a store holding one turn that edited a file: the prompt,
// the call, the result carrying a diff, and the closing sentence.
func sessionWithAWrite(t *testing.T) *memsession.Service {
	t.Helper()
	t.Setenv("DMCODE_SESSIONS_DIR", t.TempDir())
	s := memsession.NewPersistent(config.SessionsDir())
	s.SetIdentity(sessionApp, sessionUser)
	resp, err := s.Create(context.Background(), &session.CreateRequest{
		AppName: sessionApp, UserID: sessionUser, SessionID: "s-restored",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess := resp.Session
	add := func(ev *session.Event) {
		if err := s.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	// The prompt is the user's, and it is the only thing that starts a turn: the
	// store keys turns off the author, so a prompt stored as the agent would leave
	// the transcript with nothing to hang it on.
	add(ev(sessionUser, genai.RoleUser, "почини main.go"))
	add(ev(sessionApp, genai.RoleModel, &genai.FunctionCall{Name: "edit_file", Args: editArgs}))
	add(ev(sessionApp, genai.RoleUser, &genai.FunctionResponse{Name: "edit_file",
		Response: map[string]any{"replacements": 1, "diff": restoredWriteDiff}}))
	add(ev(sessionApp, genai.RoleModel, "готово"))
	return s
}

// ev builds one stored event from a sentence, a tool call or a tool result. An
// event with no content at all would be stored and then read back as nothing, so
// there is no way to ask for one.
func ev(author string, role genai.Role, part any) *session.Event {
	e := session.NewEvent(context.Background(), "inv-1")
	e.Author = author
	switch p := part.(type) {
	case string:
		e.Content = genai.NewContentFromText(p, role)
	case *genai.FunctionCall:
		e.Content = &genai.Content{Role: string(role), Parts: []*genai.Part{{FunctionCall: p}}}
	case *genai.FunctionResponse:
		e.Content = &genai.Content{Role: string(role), Parts: []*genai.Part{{FunctionResponse: p}}}
	default:
		panic(fmt.Sprintf("ev: part of type %T", part))
	}
	return e
}

// TestARestoredSessionStillShowsTheCode is the bug, as a test: switch to a
// session whose turn edited a file, and the change block must be on the transcript
// — a kindDiff line, with the diff's own text, so it renders as the block the
// live turn drew.
func TestARestoredSessionStillShowsTheCode(t *testing.T) {
	m := newSessionModel(t)
	m.sessions = sessionWithAWrite(t)

	m.switchSession("s-restored")

	var diffs, tools, results int
	for _, l := range m.history {
		switch l.kind {
		case kindDiff:
			diffs++
			if l.text != restoredWriteDiff {
				t.Errorf("the restored diff is %q, want %q", l.text, restoredWriteDiff)
			}
		case kindTool:
			tools++
		case kindToolRes:
			results++
		}
	}
	if diffs != 1 {
		t.Errorf("the restored transcript has %d change blocks, want 1", diffs)
	}
	if tools != 1 {
		t.Errorf("the restored transcript has %d tool calls, want 1", tools)
	}
	if results != 1 {
		t.Errorf("the restored transcript has %d tool results, want 1", results)
	}
}

// TestARestoredToolLineIsTheOneTheLiveTurnDrew pins that the two paths share one
// formatter. A resumed session whose tool lines were shaped differently from the
// ones the user remembers is a second renderer for the same row, and nothing
// would notice.
func TestARestoredToolLineIsTheOneTheLiveTurnDrew(t *testing.T) {
	call := &genai.FunctionCall{Name: "edit_file", Args: editArgs}
	live := toolLine(call.Name, toolArgs(call.Args))

	m := newSessionModel(t)
	m.sessions = sessionWithAWrite(t)
	m.switchSession("s-restored")

	var got string
	for _, l := range m.history {
		if l.kind == kindTool {
			got = l.text
		}
	}
	if want := live; got != want {
		t.Errorf("the restored tool line is %q, want %q — the same string the live turn draws", got, want)
	}
}

// TestARestoredToolResultHasNoDiffLeftInIt: the live turn pulls the diff out of
// the result and draws it as its own block, so the JSON one-liner does not carry
// it twice. The restored path goes through the same function, and this says so.
func TestARestoredToolResultHasNoDiffLeftInIt(t *testing.T) {
	m := newSessionModel(t)
	m.sessions = sessionWithAWrite(t)
	m.switchSession("s-restored")

	for _, l := range m.history {
		if l.kind != kindToolRes {
			continue
		}
		if strings.Contains(l.text, "diff") {
			t.Errorf("the restored result still carries the diff: %q", l.text)
		}
		if !strings.Contains(l.text, "replacements") {
			t.Errorf("the restored result lost the summary: %q", l.text)
		}
		return
	}
	t.Fatal("no tool result was restored")
}

// TestARestoredChangeBlockIsClickableAgain ties the two halves together: the
// block is drawn with the same kind the live one uses, so a click on it resolves
// to a file and a line. A restored block stored as plain text would look right and
// do nothing.
func TestARestoredChangeBlockIsClickableAgain(t *testing.T) {
	dir := t.TempDir()
	writeJumpFile(t, dir, "main.go", "package main", "one", "two")
	m := jumpModel(t, dir)
	m.sessions = sessionWithAWrite(t)
	m.switchSession("s-restored")

	row, col := frameRowContaining(t, m.View().Content, "+ ONE")
	clickAt(m, col+1, row)
	if m.ed == nil {
		t.Fatal("a click on a restored change block must open the workspace")
	}
	if m.statusText != "main.go:2" {
		t.Fatalf("the restored block opened %q, want main.go:2", m.statusText)
	}
	m.ed.Shutdown()
}

// TestResumeStartsOnThatSessionsTail is the other half: the id is not enough. A
// session opened on an empty transcript beside a model that remembers everything
// is the one thing that makes a resume look broken, so the tail goes up before
// the first frame.
func TestResumeStartsOnThatSessionsTail(t *testing.T) {
	m := newSessionModel(t)
	m.sessions = sessionWithAWrite(t)
	m.sessionID = "s-restored"

	m.resumedSession()

	if m.statusText != i18n.T("resumed session: ")+"s-restored" {
		t.Errorf("status = %q, want the resumed session named", m.statusText)
	}
	// The same evidence the switch path produces, so a resume and a switch cannot
	// show different conversations.
	var diffs, users int
	for _, l := range m.history {
		switch l.kind {
		case kindDiff:
			diffs++
		case kindUser:
			users++
		}
	}
	if users == 0 {
		t.Error("a resumed session opened with no prompt on screen")
	}
	if diffs == 0 {
		t.Error("a resumed session opened with no change block — the code is gone again")
	}
}
