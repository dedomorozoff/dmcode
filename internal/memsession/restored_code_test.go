package memsession

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// A restored session has to carry the code, not just the prose.
//
// The store keeps every call, every result and every diff a write drew — the
// function parts are in the event and survive to disk — and TranscriptOf used to
// read none of it: eventText looks at text and nothing else. So a resumed
// conversation came back with the tool calls, the results and every change block
// gone, and read as a session that had lost work it plainly had.

// toolCallEvent is a model event that asked for a tool and said nothing else.
func toolCallEvent(name string, args map[string]any) *session.Event {
	ev := session.NewEvent(context.Background(), "inv-1")
	ev.Author = "dmcode"
	ev.Content = &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{Name: name, Args: args}},
	}}
	return ev
}

// toolResultEvent is the answer, as ADK delivers it: a user-role event whose
// part is the response. The diff rides in the same map the live turn reads it
// from.
func toolResultEvent(name string, response map[string]any) *session.Event {
	ev := session.NewEvent(context.Background(), "inv-1")
	ev.Author = "dmcode"
	ev.Content = &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
		{FunctionResponse: &genai.FunctionResponse{Name: name, Response: response}},
	}}
	return ev
}

const aWriteDiff = "── main.go\n-2: one\n+2: ONE"

func TestARestoredTranscriptKeepsTheCode(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("почини main.go"))
	appendEvent(t, s, sess, toolCallEvent("edit_file", map[string]any{
		"path": "main.go", "old_string": "one", "new_string": "ONE",
	}))
	appendEvent(t, s, sess, toolResultEvent("edit_file", map[string]any{
		"replacements": 1, "diff": aWriteDiff,
	}))
	appendEvent(t, s, sess, agentEvent("готово"))

	tr, err := s.TranscriptOf(testApp, testUser, "s1", 10)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(tr.Turns))
	}
	entries := tr.Turns[0].Entries
	if len(entries) != 3 {
		t.Fatalf("the turn has %d entries, want 3 (call, result, prose): %+v", len(entries), entries)
	}
	if entries[0].Kind != EntryTool || entries[0].Call == nil || entries[0].Call.Name != "edit_file" {
		t.Fatalf("entry 0 = %+v, want the edit_file call", entries[0])
	}
	if entries[1].Kind != EntryToolResult || entries[1].Result == nil {
		t.Fatalf("entry 1 = %+v, want the tool result", entries[1])
	}
	if got := entries[1].Result.Response["diff"]; got != aWriteDiff {
		t.Errorf("the diff in the restored result = %v, want %q", got, aWriteDiff)
	}
	if entries[2].Kind != EntryAgent || entries[2].Text != "готово" {
		t.Errorf("entry 2 = %+v, want the closing prose", entries[2])
	}
	// The prose view still answers for callers that want only the answer.
	if got := tr.Turns[0].Prose(); got != "готово" {
		t.Errorf("Prose() = %q, want the prose without the tool traffic", got)
	}
}

// TestTheTurnKeepsItsOwnOrder is what the backwards walk has to get right: an
// answer is met before the prompt it answers, so what has been met is prepended
// rather than appended.
func TestTheTurnKeepsItsOwnOrder(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("сначала"))
	appendEvent(t, s, sess, agentEvent("думаю"))
	appendEvent(t, s, sess, toolCallEvent("read_file", map[string]any{"path": "a.go"}))
	appendEvent(t, s, sess, toolResultEvent("read_file", map[string]any{"content": "x"}))
	appendEvent(t, s, sess, agentEvent("вот"))
	appendEvent(t, s, sess, userEvent("дальше"))
	appendEvent(t, s, sess, agentEvent("ок"))

	tr, err := s.TranscriptOf(testApp, testUser, "s1", 10)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(tr.Turns))
	}
	var kinds []EntryKind
	for _, e := range tr.Turns[0].Entries {
		kinds = append(kinds, e.Kind)
	}
	want := []EntryKind{EntryAgent, EntryTool, EntryToolResult, EntryAgent}
	if len(kinds) != len(want) {
		t.Fatalf("turn 1 kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("turn 1 kinds = %v, want %v", kinds, want)
		}
	}
	if tr.Turns[1].User != "дальше" || tr.Turns[1].Prose() != "ок" {
		t.Errorf("turn 2 = %+v, want the second exchange", tr.Turns[1])
	}
}

// TestAPromptWithOnlyAToolCallIsNotBroken: the turn is not over just because the
// model answered with an action instead of a sentence. Broken means nothing came
// back at all.
func TestAPromptWithOnlyAToolCallIsNotBroken(t *testing.T) {
	s := newTestService(t)
	sess := mustCreate(t, s, "s1")
	appendEvent(t, s, sess, userEvent("список файлов"))
	appendEvent(t, s, sess, toolCallEvent("list_dir", map[string]any{}))

	tr, err := s.TranscriptOf(testApp, testUser, "s1", 10)
	if err != nil {
		t.Fatalf("TranscriptOf: %v", err)
	}
	if len(tr.Turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(tr.Turns))
	}
	if tr.Turns[0].Broken {
		t.Error("a turn whose model replied with a tool call was marked broken")
	}
}
