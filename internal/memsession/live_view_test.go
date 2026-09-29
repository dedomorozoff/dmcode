package memsession

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// TestSessionViewSeesLaterAppends is the contract the runner depends on and the
// one that was broken.
//
// The runner holds one session.Session for a whole invocation, appends every
// event to it through AppendEvent, and then reads the conversation back out of
// that same object to build the next model request. A view frozen at Create
// time therefore shows the model nothing at all: it never sees the user's
// prompt and never sees the result of the tool it just called, so it repeats
// one identical tool call for as long as the runner will let it.
func TestSessionViewSeesLaterAppends(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	if n := sess.Events().Len(); n != 0 {
		t.Fatalf("a fresh session reports %d events, want 0", n)
	}

	appendEvent(t, s, sess, userEvent("привет"))

	if n := sess.Events().Len(); n != 1 {
		t.Fatalf("through the view handed to the runner: %d events, want 1", n)
	}
	if got := eventText(sess.Events().At(0)); got != "привет" {
		t.Errorf("through the view: event 0 is %q, want %q", got, "привет")
	}

	// The whole point is the *next* round: the model has to be able to read
	// back the tool call it made and the result that came of it.
	appendEvent(t, s, sess, func() *session.Event {
		ev := session.NewEvent(context.Background(), "inv-1")
		ev.Author = "dmcode"
		ev.Content = &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "list_dir", Args: map[string]any{}}},
		}}
		return ev
	}())
	appendEvent(t, s, sess, func() *session.Event {
		ev := session.NewEvent(context.Background(), "inv-1")
		ev.Author = "dmcode"
		ev.Content = &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{Name: "list_dir",
				Response: map[string]any{"entries": "main.go"}}},
		}}
		return ev
	}())

	evs := sess.Events()
	if evs.Len() != 3 {
		t.Fatalf("through the view after a tool round: %d events, want 3", evs.Len())
	}
	if evs.At(1).Content.Parts[0].FunctionCall == nil {
		t.Error("the model's own tool call is not visible to it on the next round")
	}
	if evs.At(2).Content.Parts[0].FunctionResponse == nil {
		t.Error("the tool result is not visible to the model on the next round")
	}
}

// TestSessionViewReadsStateAppendedLater covers the same liveness for state: a
// tool that writes session state has to see it on the next round.
func TestSessionViewReadsStateAppendedLater(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	if _, err := sess.State().Get("step"); err == nil {
		t.Fatal("a fresh session reports state it does not have")
	}

	appendEvent(t, s, sess, func() *session.Event {
		ev := session.NewEvent(context.Background(), "inv-1")
		ev.Author = "dmcode"
		ev.Actions.StateDelta = map[string]any{"step": 2}
		return ev
	}())

	got, err := sess.State().Get("step")
	if err != nil {
		t.Fatalf("state written by an event is not visible through the view: %v", err)
	}
	if got != 2 {
		t.Errorf("step = %v, want 2", got)
	}
}

// TestSessionViewLastUpdateTimeMoves pins the third thing a runner may read off
// the live session. The event carries an explicit timestamp: platform.Now in a
// test does not tick, so two reads a moment apart would otherwise compare
// equal and the assertion would be testing the clock, not the view.
func TestSessionViewLastUpdateTimeMoves(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	before := sess.LastUpdateTime()

	stamp := before.Add(time.Hour)
	appendEvent(t, s, sess, func() *session.Event {
		ev := userEvent("привет")
		ev.Timestamp = stamp
		return ev
	}())

	if after := sess.LastUpdateTime(); !after.Equal(stamp) {
		t.Errorf("LastUpdateTime = %v, want the last appended event's %v", after, stamp)
	}
}

// TestGetNarrowingStillApplies keeps the window a Get may ask for working after
// the view stopped being a snapshot: it has to narrow what Events returns, not
// merely be carried along.
func TestGetNarrowingStillApplies(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("один"))
	appendEvent(t, s, sess, agentEvent("два"))
	appendEvent(t, s, sess, userEvent("три"))

	resp, err := s.Get(context.Background(), &session.GetRequest{
		AppName:         testApp,
		UserID:          testUser,
		SessionID:       "s1",
		NumRecentEvents: 1,
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := resp.Session.Events().Len(); n != 1 {
		t.Fatalf("NumRecentEvents=1 returned %d events, want 1", n)
	}
	if got := eventText(resp.Session.Events().At(0)); got != "три" {
		t.Errorf("narrowed view starts at %q, want %q", got, "три")
	}
}

// TestStateWriteThroughViewDoesNotReachTheStore keeps the deliberate rule that a
// state write is only real once it arrives as an event: a Set that skipped
// AppendEvent would exist in one turn's prompt and in no session.
func TestStateWriteThroughViewDoesNotReachTheStore(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")

	if err := sess.State().Set("ghost", 1); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := sess.State().Get("ghost"); err == nil {
		t.Error("a state write that never reached AppendEvent is visible in the same view")
	}
	resp, err := s.Get(context.Background(), &session.GetRequest{
		AppName: testApp, UserID: testUser, SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := resp.Session.State().Get("ghost"); err == nil {
		t.Error("a state write that never reached AppendEvent reached the store")
	}
}
