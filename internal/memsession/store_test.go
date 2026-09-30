package memsession

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// newFileService returns a persistent service writing into a temp directory,
// so a test can assert on the files without touching the user's home.
func newFileService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	s := NewPersistent(dir)
	s.SetIdentity(testApp, testUser)
	return s, dir
}

// reopen returns a second service over the same directory: this is what a
// restart of dmcode does, and the only way to prove a session really survived.
func reopen(t *testing.T, dir string) *Service {
	t.Helper()
	s := NewPersistent(dir)
	s.SetIdentity(testApp, testUser)
	return s
}

// TestPersistentSessionSurvivesARestart is the promise of the whole store: a
// conversation written by one run is the conversation the next run continues.
func TestPersistentSessionSurvivesARestart(t *testing.T) {
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("вопрос"))
	appendEvent(t, s, sess, agentEvent("ответ"))

	again := reopen(t, dir)
	if n := again.Discover(context.Background(), testApp, testUser); n != 1 {
		t.Fatalf("Discover found %d sessions, want 1", n)
	}
	evs := getEvents(t, again, "s1")
	if len(evs) != 2 {
		t.Fatalf("after a restart the session holds %d events, want 2", len(evs))
	}
	if got := eventText(evs[0]); got != "вопрос" {
		t.Errorf("first event = %q, want %q", got, "вопрос")
	}
}

// TestRewindIsPersisted guards the case that matters most: an undo that lives
// only in memory would come back after a restart, which is worse than not
// offering it.
func TestRewindIsPersisted(t *testing.T) {
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("первый"))
	appendEvent(t, s, sess, agentEvent("ответ"))
	appendEvent(t, s, sess, userEvent("второй"))
	appendEvent(t, s, sess, agentEvent("ответ на второй"))

	if _, ok, err := s.RewindToLastUserMessage(context.Background(), testApp, testUser, "s1"); err != nil || !ok {
		t.Fatalf("rewind = ok %v, err %v", ok, err)
	}

	again := reopen(t, dir)
	again.Discover(context.Background(), testApp, testUser)
	evs := getEvents(t, again, "s1")
	if len(evs) != 2 {
		t.Fatalf("after a restart the session holds %d events, want 2", len(evs))
	}
	if got := eventText(evs[0]); got != "первый" {
		t.Errorf("first event = %q, want %q — the rewound turn came back", got, "первый")
	}
}

// TestFunctionCallsSurviveARestart: a session holding tool calls is the normal
// case, and a store that dropped FunctionCall parts would produce a
// conversation the model cannot make sense of.
func TestFunctionCallsSurviveARestart(t *testing.T) {
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("прочитай файл"))

	call := session.NewEvent(context.Background(), "inv-1")
	call.Author = "dmcode"
	call.Content = &genai.Content{
		Role:  genai.RoleModel,
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "read_file", Args: map[string]any{"path": "main.go"}}}},
	}
	appendEvent(t, s, sess, call)

	// The runner sends a tool result as a user-authored event: from the
	// session's point of view that is who produced it.
	resp := session.NewEvent(context.Background(), "inv-1")
	resp.Author = userAuthor
	resp.Content = &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "read_file", Response: map[string]any{"content": "package main"}}}},
	}
	appendEvent(t, s, sess, resp)

	again := reopen(t, dir)
	again.Discover(context.Background(), testApp, testUser)
	evs := getEvents(t, again, "s1")
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3", len(evs))
	}
	if evs[1].Content == nil || evs[1].Content.Parts[0].FunctionCall == nil {
		t.Fatal("the function call did not survive the restart")
	}
	if got := evs[1].Content.Parts[0].FunctionCall.Name; got != "read_file" {
		t.Errorf("tool name = %q, want %q", got, "read_file")
	}
	if evs[2].Content == nil || evs[2].Content.Parts[0].FunctionResponse == nil {
		t.Fatal("the function response did not survive the restart")
	}
}

// TestBrokenLineDoesNotLoseTheSession: a truncated last line is what a crash
// mid-write leaves behind. Losing that one event is acceptable; refusing the
// file would throw away the whole conversation.
func TestBrokenLineDoesNotLoseTheSession(t *testing.T) {
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("вопрос"))
	appendEvent(t, s, sess, agentEvent("ответ"))

	path := filepath.Join(dir, "s1.jsonl")
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	fh.WriteString(`{"author":"dmcode","content":{"parts":[{"te`)
	fh.Close()

	again := reopen(t, dir)
	again.Discover(context.Background(), testApp, testUser)
	evs := getEvents(t, again, "s1")
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2 — one damaged line must not cost the session", len(evs))
	}
}

// TestDeleteRemovesTheFile: a session deleted in the UI must not reappear in
// the list on the next start.
func TestDeleteRemovesTheFile(t *testing.T) {
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("вопрос"))

	if err := s.Delete(context.Background(), &session.DeleteRequest{
		AppName: testApp, UserID: testUser, SessionID: "s1",
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.jsonl")); !os.IsNotExist(err) {
		t.Errorf("the session file is still there (stat error: %v)", err)
	}
	again := reopen(t, dir)
	if n := again.Discover(context.Background(), testApp, testUser); n != 0 {
		t.Errorf("a deleted session was discovered %d times", n)
	}
}

// TestSummariesReadsMetadataWithoutEvents is the reason the header is separate:
// listing must not depend on how long the conversations are.
func TestSummariesReadsMetadataWithoutEvents(t *testing.T) {
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("сделай мне план"))

	// A fresh service has registered nothing, so the list has to come from the
	// files alone — exactly what happens on a restart.
	again := reopen(t, dir)
	summaries := again.Summaries()
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(summaries))
	}
	if summaries[0].ID != "s1" {
		t.Errorf("id = %q, want %q", summaries[0].ID, "s1")
	}
	if summaries[0].Title != "сделай мне план" {
		t.Errorf("title = %q, want the first prompt", summaries[0].Title)
	}
}

// TestSessionFileIsNotWorldReadable: the file holds everything the user typed
// and everything the agent read.
//
// Skipped on Windows, where Go's Chmod only toggles the read-only attribute and
// cannot express the Unix permission bits this asserts. The mode is still
// requested on write there, so nothing regresses for a Windows user.
func TestSessionFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits to assert on")
	}
	s, dir := newFileService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("секрет"))

	info, err := os.Stat(filepath.Join(dir, "s1.jsonl"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("file mode = %v, want no group or world access", perm)
	}
}

// TestUnsafeSessionIDStaysInsideTheDirectory: a session id reaches the
// filesystem as a filename, and /resume takes one from the keyboard.
func TestUnsafeSessionIDStaysInsideTheDirectory(t *testing.T) {
	dir := t.TempDir()
	s := NewPersistent(dir)
	s.SetIdentity(testApp, testUser)

	hostile := mustCreate(t, s, "../../escaped")
	appendEvent(t, s, hostile, userEvent("вопрос"))

	f := &fileStore{dir: dir}
	got := f.path(hostile.ID())
	if filepath.Dir(got) != filepath.Clean(dir) {
		t.Errorf("path = %q, which is outside %q", got, dir)
	}
	if strings.Contains(filepath.Base(got), "..") {
		t.Errorf("file name %q still contains a parent reference", filepath.Base(got))
	}
}

// TestWriteErrorDoesNotFailTheTurn: a session that cannot be written is still a
// usable session, and losing history on exit is better than losing the turn.
func TestWriteErrorDoesNotFailTheTurn(t *testing.T) {
	// A path under a regular file cannot be created, which is a portable way
	// to make every write fail.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewPersistent(filepath.Join(blocker, "sessions"))
	s.SetIdentity(testApp, testUser)

	sess := mustCreate(t, s, "s1")
	if err := s.AppendEvent(context.Background(), sess, userEvent("вопрос")); err != nil {
		t.Fatalf("AppendEvent failed on an unwritable store: %v", err)
	}
	if s.LastWriteError() == nil {
		t.Error("no write error was recorded")
	}
	// The conversation is still there, which is the point.
	if got := len(getEvents(t, s, "s1")); got != 1 {
		t.Errorf("got %d events, want 1 — the turn must survive a failed write", got)
	}
}

// PLACEHOLDER_STORE_TESTS
