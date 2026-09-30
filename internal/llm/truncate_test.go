package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// scriptedChatServer answers each request with the next set of SSE frames, and
// repeats the last set once they run out, so a test can make the first attempt
// fail the way a capped endpoint does and the second one behave.
//
// The decoded bodies are returned alongside: what the second attempt actually
// asked for is the point of the recovery, and an assertion on the transcript's
// outcome cannot see it.
func scriptedChatServer(t *testing.T, attempts ...[]string) (*httptest.Server, *[]chatRequest) {
	t.Helper()
	if len(attempts) == 0 {
		t.Fatal("scriptedChatServer needs at least one attempt")
	}
	var (
		mu     sync.Mutex
		bodies []chatRequest
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not a chat request: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		at := len(bodies) - 1
		mu.Unlock()
		if at >= len(attempts) {
			at = len(attempts) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a flusher")
			return
		}
		for _, f := range attempts[at] {
			fmt.Fprintf(w, "data: %s\n\n", f)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// toolCallFrame is one streaming tool-call frame for edit_file with the given
// arguments text.
//
// Both frames in these tests are built with the encoder rather than written out
// by hand. A hand-written frame is one misplaced brace away from not parsing at
// all, and a frame that does not parse is skipped as noise by the reader — the
// test would then be asserting about a frame that never arrived.
func toolCallFrame(t *testing.T, id, args string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": id, "type": "function",
				"function": map[string]any{"name": "edit_file", "arguments": args},
			}},
		}}},
	})
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	return string(raw)
}

// editArgs is one complete edit_file argument object, as the model would send it.
func editArgs(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"path":        "a.go",
		"new_string":  "func a() { return 1 }",
		"replace_all": "false",
	})
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	return string(raw)
}

// truncatedEdit is the first attempt in every test: a model that set out to
// rewrite a file and was cut off inside the arguments.
//
// Only the arguments are cut, never the frame — what the model was writing is
// what fails to parse, which is the failure under test.
func truncatedEdit(t *testing.T, usage string) []string {
	// Cut inside the value the model was writing, so what arrives is an
	// unterminated string rather than a valid object that happens to be shorter.
	whole := editArgs(t)
	cut := whole[:strings.Index(whole, "func")]
	return []string{
		toolCallFrame(t, "call_1", cut),
		`{"choices":[{"delta":{},"finish_reason":"length"}]` + usage + `}`,
	}
}

// wholeEdit is the same call arriving in one piece on the second attempt.
func wholeEdit(t *testing.T) string {
	t.Helper()
	return toolCallFrame(t, "call_2", editArgs(t))
}

func readTruncated(t *testing.T, m *chatModel, req *model.LLMRequest) (*model.LLMResponse, error) {
	t.Helper()
	var (
		final *model.LLMResponse
		fail  error
	)
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			fail = err
			continue
		}
		if !resp.Partial {
			final = resp
		}
	}
	return final, fail
}

func editRequest() *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "верни единицу из a"}}}},
	}
}

// TestTruncatedToolCallIsAskedAgain is the recovery: the same endpoint is asked
// the same question once more, with a note that the call was too big. The turn
// ends with a real tool call, which is the whole point — the first attempt never
// reached the tool, so there is nothing to undo and nothing to tell the model
// about later.
func TestTruncatedToolCallIsAskedAgain(t *testing.T) {
	srv, bodies := scriptedChatServer(t,
		truncatedEdit(t, `,"usage":{"completion_tokens":4096,"prompt_tokens":900,"total_tokens":4996}`),
		[]string{wholeEdit(t), `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`},
	)

	m := NewChatModel(srv.URL+"/v1", "", "test-coder")
	final, err := readTruncated(t, m, editRequest())
	if err != nil {
		t.Fatalf("the re-ask must recover the turn, got %v", err)
	}
	if final == nil {
		t.Fatal("no aggregated final event after the re-ask")
	}
	var call *genai.FunctionCall
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			call = p.FunctionCall
		}
	}
	if call == nil {
		t.Fatal("the re-ask produced no function call")
	}
	if got, _ := call.Args["new_string"].(string); got != "func a() { return 1 }" {
		t.Errorf("call arguments = %q, want the complete edit", got)
	}

	if len(*bodies) != 2 {
		t.Fatalf("endpoint saw %d requests, want 2", len(*bodies))
	}
	second := (*bodies)[1]
	if len(second.Messages) != len((*bodies)[0].Messages)+1 {
		t.Fatalf("the re-ask sent %d messages, want one more than the first request's %d",
			len(second.Messages), len((*bodies)[0].Messages))
	}
	note := second.Messages[len(second.Messages)-1]
	if note.Role != "user" || !strings.Contains(note.Content, "edit_file") {
		t.Errorf("the re-ask note is %+v: it must be a user turn naming the call", note)
	}
	// The server applied a cap of 4096, so the second attempt asks for twice the
	// room rather than for the same wall.
	if second.MaxOutputTokens != 2*4096 {
		t.Errorf("re-ask max_tokens = %d, want %d", second.MaxOutputTokens, 2*4096)
	}
}

// TestTruncationAfterTextIsNotAskedAgain pins the rule that makes the recovery
// safe: once the user is looking at text, a second answer would print twice.
func TestTruncationAfterTextIsNotAskedAgain(t *testing.T) {
	attempt := append([]string{
		`{"choices":[{"delta":{"content":"Правлю файл."}}]}`,
	}, truncatedEdit(t, "")...)
	srv, bodies := scriptedChatServer(t, attempt)

	m := NewChatModel(srv.URL+"/v1", "", "test-coder")
	_, err := readTruncated(t, m, editRequest())
	if err == nil {
		t.Fatal("a truncation after visible text must still fail the turn")
	}
	if !strings.Contains(err.Error(), "finish_reason=length") {
		t.Errorf("error = %q, want the truncation named", err)
	}
	if len(*bodies) != 1 {
		t.Errorf("endpoint saw %d requests, want 1 — nothing may be shown twice", len(*bodies))
	}
}

// TestSecondTruncationReportsTheFirst: one re-ask is the budget. A model that
// will not split the call is not asked a third time, and the user gets the
// truncation rather than whatever the failed re-ask happened to say.
func TestSecondTruncationReportsTheFirst(t *testing.T) {
	srv, bodies := scriptedChatServer(t, truncatedEdit(t, ""))

	m := NewChatModel(srv.URL+"/v1", "", "test-coder")
	_, err := readTruncated(t, m, editRequest())
	if err == nil {
		t.Fatal("two truncations must end the turn")
	}
	if !strings.Contains(err.Error(), "finish_reason=length") {
		t.Errorf("error = %q, want the truncation named", err)
	}
	if len(*bodies) != 2 {
		t.Errorf("endpoint saw %d requests, want 2", len(*bodies))
	}
}

// TestProbeBudgetIsNotAskedForAgain: the tool probe asks for 64 tokens on
// purpose. Doubling a budget dmcode set itself, behind its own back, would make
// a five-second check a second one.
func TestProbeBudgetIsNotAskedForAgain(t *testing.T) {
	srv, bodies := scriptedChatServer(t, truncatedEdit(t, ""))

	m := NewChatModel(srv.URL+"/v1", "", "test-coder")
	req := editRequest()
	req.Config = &genai.GenerateContentConfig{MaxOutputTokens: 64}
	_, err := readTruncated(t, m, req)
	if err == nil {
		t.Fatal("the probe must still fail on a truncated call")
	}
	if len(*bodies) != 1 {
		t.Errorf("endpoint saw %d requests, want 1", len(*bodies))
	}
}

// TestMalformedArgumentsAreNotAskedAgain is the other half of the type: garbage
// arguments with a clean finish are the model's mistake, not a budget, and
// asking again is how a tool loop starts.
func TestMalformedArgumentsAreNotAskedAgain(t *testing.T) {
	attempt := []string{
		toolCallFrame(t, "call_1", `{"path":`),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv, bodies := scriptedChatServer(t, attempt)

	m := NewChatModel(srv.URL+"/v1", "", "test-coder")
	_, err := readTruncated(t, m, editRequest())
	if err == nil {
		t.Fatal("malformed arguments must fail the turn")
	}
	if !strings.Contains(err.Error(), "не удалось разобрать") {
		t.Errorf("error = %q, want the plain parse failure", err)
	}
	if len(*bodies) != 1 {
		t.Errorf("endpoint saw %d requests, want 1", len(*bodies))
	}
}

// TestReAskBudget covers the arithmetic the second attempt is sized by: no
// evidence means no change, and both ends of the range are clamped.
func TestReAskBudget(t *testing.T) {
	cases := []struct {
		name           string
		current, spent int
		want           int
	}{
		{"unknown usage leaves the budget alone", 0, 0, 0},
		{"twice what the call used", 0, 6000, 12000},
		{"a tiny wall still gets room", 0, 40, minReAskTokens},
		{"an absurd wall is clamped", 0, 200000, maxReAskTokens},
		{"a budget dmcode set is kept", 64, 4096, 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reAskBudget(c.current, c.spent); got != c.want {
				t.Errorf("reAskBudget(%d, %d) = %d, want %d", c.current, c.spent, got, c.want)
			}
		})
	}
}
