package memsession

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

const (
	testApp  = "dmcode"
	testUser = "user"
)

// userEvent builds a stored user message the way the runner would: an event
// authored by "user" carrying one text part.
func userEvent(text string) *session.Event {
	ev := session.NewEvent(context.Background(), "inv-1")
	ev.Author = userAuthor
	ev.Content = genai.NewContentFromText(text, genai.RoleUser)
	return ev
}

// agentEvent builds a stored model message.
func agentEvent(text string) *session.Event {
	ev := session.NewEvent(context.Background(), "inv-1")
	ev.Author = "dmcode"
	ev.Content = genai.NewContentFromText(text, genai.RoleModel)
	return ev
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s := NewMemory()
	s.SetIdentity(testApp, testUser)
	return s
}

// mustCreate registers a session and returns it.
func mustCreate(t *testing.T, s *Service, id string) session.Session {
	t.Helper()
	resp, err := s.Create(context.Background(), &session.CreateRequest{
		AppName:   testApp,
		UserID:    testUser,
		SessionID: id,
	})
	if err != nil {
		t.Fatalf("Create(%q): %v", id, err)
	}
	return resp.Session
}

func appendEvent(t *testing.T, s *Service, sess session.Session, ev *session.Event) {
	t.Helper()
	if err := s.AppendEvent(context.Background(), sess, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}

func getEvents(t *testing.T, s *Service, id string) []*session.Event {
	t.Helper()
	resp, err := s.Get(context.Background(), &session.GetRequest{
		AppName:   testApp,
		UserID:    testUser,
		SessionID: id,
	})
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}
	// Collected through the Events interface rather than a field, because that
	// is the only view a caller ever gets — a test that reached into the store
	// would pass even if the interface were broken.
	var out []*session.Event
	for ev := range resp.Session.Events().All() {
		out = append(out, ev)
	}
	return out
}

// TestAppendThenGetRoundTrips is the contract the runner depends on: what was
// appended comes back, in order, with its author and text intact.
func TestAppendThenGetRoundTrips(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	appendEvent(t, s, sess, userEvent("привет"))
	appendEvent(t, s, sess, agentEvent("здраствуй"))

	evs := getEvents(t, s, "s1")
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Author != userAuthor || evs[1].Author != "dmcode" {
		t.Errorf("authors = %q, %q; want %q, %q", evs[0].Author, evs[1].Author, userAuthor, "dmcode")
	}
	if got := eventText(evs[0]); got != "привет" {
		t.Errorf("first event text = %q, want %q", got, "привет")
	}
}

// TestAppendEventStampsMissingIdentity covers the ADK obligation: an event
// built as a struct literal has no id, and an event nothing can name is an
// event nothing can refer to.
func TestAppendEventStampsMissingIdentity(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	bare := &session.Event{Author: "dmcode"}
	appendEvent(t, s, sess, bare)

	if bare.ID == "" {
		t.Error("AppendEvent left the event without an id")
	}
	if bare.Timestamp.IsZero() {
		t.Error("AppendEvent left the event without a timestamp")
	}
}

// TestPartialEventsAreNotStored: streaming chunks are not conversation. Storing
// them would replay every token as its own message.
func TestPartialEventsAreNotStored(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	chunk := agentEvent("часть ")
	chunk.Partial = true
	appendEvent(t, s, sess, chunk)
	appendEvent(t, s, sess, agentEvent("продолжение"))

	if got := len(getEvents(t, s, "s1")); got != 1 {
		t.Errorf("stored %d events, want 1 — a partial chunk must not be kept", got)
	}
}

// TestTempStateKeysAreDropped: a "temp:" key is scratch state the next turn
// must not inherit, since no surviving event describes it.
func TestTempStateKeysAreDropped(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	ev := agentEvent("ok")
	ev.Actions.StateDelta = map[string]any{
		"kept":                            "yes",
		session.KeyPrefixTemp + "scratch": "no",
	}
	appendEvent(t, s, sess, ev)

	// Read back through Get rather than through the handle Create returned: a
	// view is a snapshot, so the state on it predates the event just appended.
	// Reading it here would test the copy, not the store.
	resp, err := s.Get(context.Background(), &session.GetRequest{
		AppName: testApp, UserID: testUser, SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	st := resp.Session.State()
	if v, err := st.Get("kept"); err != nil || v != "yes" {
		t.Errorf("Get(kept) = %v, %v; want \"yes\", nil", v, err)
	}
	if _, err := st.Get(session.KeyPrefixTemp + "scratch"); err == nil {
		t.Error("a temp: state key survived into the session state")
	}
}

// TestRewindDropsTheLastUserMessageAndEverythingAfter is the whole point of
// ctrl+z: the prompt comes back to the user, and the model no longer sees a
// question that has already been answered.
func TestRewindDropsTheLastUserMessageAndEverythingAfter(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	appendEvent(t, s, sess, userEvent("первый"))
	appendEvent(t, s, sess, agentEvent("ответ на первый"))
	appendEvent(t, s, sess, userEvent("второй"))
	appendEvent(t, s, sess, agentEvent("ответ на второй"))

	text, ok, err := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "s1")
	if err != nil || !ok {
		t.Fatalf("RewindToLastUserMessage = %q, %v, %v; want ok", text, ok, err)
	}
	if text != "второй" {
		t.Errorf("rewound text = %q, want %q", text, "второй")
	}

	evs := getEvents(t, s, "s1")
	if len(evs) != 2 {
		t.Fatalf("kept %d events, want 2", len(evs))
	}
	if got := eventText(evs[0]); got != "первый" {
		t.Errorf("first kept event = %q, want %q", got, "первый")
	}
}

// TestRewindAgainGoesBackAFurther: a second press must undo the previous turn,
// not report that there is nothing left to undo.
func TestRewindAgainGoesBackAFurther(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	appendEvent(t, s, sess, userEvent("один"))
	appendEvent(t, s, sess, agentEvent("ответ один"))
	appendEvent(t, s, sess, userEvent("два"))
	appendEvent(t, s, sess, agentEvent("ответ два"))

	if text, _, _ := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "s1"); text != "два" {
		t.Fatalf("first rewind = %q, want %q", text, "два")
	}
	text, ok, err := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "s1")
	if err != nil || !ok {
		t.Fatalf("second rewind = %q, %v, %v; want ok", text, ok, err)
	}
	if text != "один" {
		t.Errorf("second rewind = %q, want %q", text, "один")
	}
	if got := len(getEvents(t, s, "s1")); got != 0 {
		t.Errorf("%d events left, want 0", got)
	}
}

// TestRewindWithNoUserMessageReportsFailure: "nothing to undo" has to be
// distinguishable from "rewound successfully", or the UI would claim a rewind
// that did not happen.
func TestRewindWithNoUserMessageReportsFailure(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, agentEvent("без вопроса"))

	_, ok, err := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("rewind reported success on a session with no user message")
	}
}

// TestRewindRebuildsState is why the state is replayed rather than left alone:
// a key written by a dropped event must not survive it, and a key written
// earlier must.
func TestRewindRebuildsState(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	first := userEvent("первый")
	first.Actions.StateDelta = map[string]any{"early": 1}
	appendEvent(t, s, sess, first)
	appendEvent(t, s, sess, agentEvent("ответ"))

	second := userEvent("второй")
	second.Actions.StateDelta = map[string]any{"late": 2}
	appendEvent(t, s, sess, second)

	if _, _, err := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "s1"); err != nil {
		t.Fatalf("rewind: %v", err)
	}

	resp, err := s.Get(context.Background(), &session.GetRequest{
		AppName: testApp, UserID: testUser, SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := resp.Session.State().Get("late"); err == nil {
		t.Error("state from a rewound event survived — the delta was not replayed")
	}
	if v, err := resp.Session.State().Get("early"); err != nil || v != 1 {
		t.Errorf("Get(early) = %v, %v; want 1, nil — an earlier delta must stay", v, err)
	}
}

// TestGetMissingSessionReportsNotFound matters because the runner auto-creates
// on exactly this error: any other error would end the session.
func TestGetMissingSessionReportsNotFound(t *testing.T) {
	s := newTestService(t)
	_, err := s.Get(context.Background(), &session.GetRequest{
		AppName: testApp, UserID: testUser, SessionID: "nope",
	})
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("error = %v, want one wrapping session.ErrNotFound", err)
	}
}

// TestRewindUnknownSessionFails: a missing session is reported as missing, so
// the UI can say "no session" instead of treating it as an empty rewind.
func TestRewindUnknownSessionFails(t *testing.T) {
	s := newTestService(t)
	_, _, err := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "nope")
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("error = %v, want one wrapping session.ErrNotFound", err)
	}
}

// TestCreateRejectsADuplicateID: two sessions under one id would be one
// conversation with a split history.
func TestCreateRejectsADuplicateID(t *testing.T) {
	s := newTestService(t)
	mustCreate(t, s, "s1")
	if _, err := s.Create(context.Background(), &session.CreateRequest{
		AppName: testApp, UserID: testUser, SessionID: "s1",
	}); err == nil {
		t.Error("Create accepted a duplicate session id")
	}
}

// TestListIsOldestFirst: the list reads top-down, so the order has to be a
// promise rather than a map iteration accident.
func TestListIsOldestFirst(t *testing.T) {
	s := newTestService(t)
	for _, id := range []string{"a", "b", "c"} {
		mustCreate(t, s, id)
	}
	resp, err := s.List(context.Background(), &session.ListRequest{AppName: testApp, UserID: testUser})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(resp.Sessions) != len(want) {
		t.Fatalf("got %d sessions, want %d", len(resp.Sessions), len(want))
	}
	for i, id := range want {
		if got := resp.Sessions[i].ID(); got != id {
			t.Errorf("session %d = %q, want %q", i, got, id)
		}
	}
}

// TestDeleteIsIdempotent: the caller's intent is that a session be gone, which
// is already true if it never existed.
func TestDeleteIsIdempotent(t *testing.T) {
	s := newTestService(t)
	mustCreate(t, s, "s1")
	req := &session.DeleteRequest{AppName: testApp, UserID: testUser, SessionID: "s1"}
	if err := s.Delete(context.Background(), req); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := s.Delete(context.Background(), req); err != nil {
		t.Errorf("second Delete: %v, want nil", err)
	}
}

// TestSummariesSkipsEmptySessions: a /new the user never typed into is not a
// conversation, and a list of blank rows helps nobody.
func TestSummariesSkipsEmptySessions(t *testing.T) {
	s := newTestService(t)
	mustCreate(t, s, "empty")
	used := mustCreate(t, s, "used")
	appendEvent(t, s, used, userEvent("что-то сделали"))

	got := s.Summaries()
	if len(got) != 1 || got[0].ID != "used" {
		t.Fatalf("Summaries = %+v, want just the used session", got)
	}
	if got[0].Title != "что-то сделали" {
		t.Errorf("title = %q, want the first user message", got[0].Title)
	}
}

// TestTranscriptOfPairsPromptsWithAnswers is what a session switch shows: the
// last exchanges, so the screen is not blank while the model remembers
// everything.
func TestTranscriptOfPairsPromptsWithAnswers(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("вопрос"))
	appendEvent(t, s, sess, agentEvent("ответ"))
	appendEvent(t, s, sess, userEvent("ещё вопрос"))
	appendEvent(t, s, sess, agentEvent("ещё ответ"))

	tr, err := s.TranscriptOf(testApp, testUser, "s1", 10)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(tr.Turns))
	}
	if tr.Turns[0].User != "вопрос" || tr.Turns[0].Agent != "ответ" {
		t.Errorf("turn 1 = %+v, want the first exchange", tr.Turns[0])
	}
	if tr.Turns[1].User != "ещё вопрос" {
		t.Errorf("turn 2 user = %q, want %q", tr.Turns[1].User, "ещё вопрос")
	}
}

// TestTranscriptOfLimitsToTheTail: a long session must not be dumped onto the
// screen to explain a switch.
func TestTranscriptOfLimitsToTheTail(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	for i := range 5 {
		appendEvent(t, s, sess, userEvent(promptText(i)))
		appendEvent(t, s, sess, agentEvent("ответ"))
	}
	tr, err := s.TranscriptOf(testApp, testUser, "s1", 2)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(tr.Turns))
	}
	if tr.Turns[1].User != promptText(4) {
		t.Errorf("last turn = %q, want the newest prompt %q", tr.Turns[1].User, promptText(4))
	}
}

// TestTranscriptOfMarksAnUnansweredPrompt: a turn cut short by an error or an
// Esc has no answer, and printing it as one would misrepresent what happened.
func TestTranscriptOfMarksAnUnansweredPrompt(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("осталось без ответа"))

	tr, err := s.TranscriptOf(testApp, testUser, "s1", 10)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(tr.Turns))
	}
	if !tr.Turns[0].Broken {
		t.Error("a prompt with no answer was not marked as broken")
	}
}

func promptText(i int) string { return "вопрос " + string(rune('a'+i)) }
