package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// bodyCapturingServer records the last request body it was sent, so a test can
// ask what actually went on the wire rather than what the builder produced.
func bodyCapturingServer(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = nil
		json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// maxTokensFrom pulls the one field out of a recorded body, reporting whether it
// was there at all. Absent and zero are different answers here: absent means the
// endpoint's own default stands, which is the status quo dmcode is replacing.
func maxTokensFrom(t *testing.T, body *map[string]any) (int, bool) {
	t.Helper()
	if *body == nil {
		t.Fatal("the endpoint was never called")
	}
	v, ok := (*body)["max_tokens"]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("max_tokens is %T, want a number: %v", v, v)
	}
	return int(f), true
}

// The bug this pins: an ordinary turn sent no max_tokens at all, so the output
// limit was whatever the endpoint happened to default to — the model's ceiling
// on a hosted provider, the GGUF author's number on a local server, 4096 on some
// gateways. A model writing a long calculation then stopped mid-sentence with
// finish_reason=length, and nothing in dmcode said so.
func TestAnOrdinaryTurnAsksForAnOutputBudget(t *testing.T) {
	srv, body := bodyCapturingServer(t)
	m := NewChatModel(srv.URL, "", "gpt-4o")
	for range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
	}
	got, ok := maxTokensFrom(t, body)
	if !ok {
		t.Fatalf("a normal turn sent no max_tokens: %v", *body)
	}
	if got != defaultOutputTokens {
		t.Errorf("max_tokens = %d, want %d", got, defaultOutputTokens)
	}
}

// The budget cannot be allowed to cause the very failure it prevents. Most
// endpoints require the prompt and the answer to fit the same window, so a 16k
// answer on an 8k model is a request rejected before generation starts — on the
// first turn of a conversation that had room to spare.
func TestTheBudgetLeavesRoomInsideTheWindow(t *testing.T) {
	srv, body := bodyCapturingServer(t)
	// gemma is 8192 in the window table.
	m := NewChatModel(srv.URL, "", "gemma-2-9b")
	for range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
	}
	got, ok := maxTokensFrom(t, body)
	if !ok {
		t.Fatalf("a recognised model sent no max_tokens: %v", *body)
	}
	if got >= 8192 {
		t.Errorf("max_tokens = %d on an 8192-token window; the prompt gets nothing", got)
	}
	if want := 8192 / 2; got != want {
		t.Errorf("max_tokens = %d, want %d (half the window)", got, want)
	}
}

// A model this build does not recognise has an unknown window, and a budget
// invented against one can fail the very first request. The endpoint's own
// default is left standing, exactly as compaction refuses to guess a threshold —
// and DMCODE_CONTEXT is the escape hatch, since naming the window turns the
// budget back on.
func TestAnUnknownModelKeepsTheEndpointsOwnLimit(t *testing.T) {
	srv, body := bodyCapturingServer(t)
	m := NewChatModel(srv.URL, "", "some-local-gguf-abc")
	for range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
	}
	if got, ok := maxTokensFrom(t, body); ok {
		t.Errorf("max_tokens = %d was invented for a model of unknown window", got)
	}

	t.Setenv("DMCODE_CONTEXT", "4096")
	if got := m.outputBudget(0); got != 2048 {
		t.Errorf("with the window named the budget stayed off: %d", got)
	}
}

// The override exists for a model whose ceiling the table overshoots, and it has
// to survive the window cap too — otherwise raising the budget on a large-window
// model works and raising it on a small one silently does nothing.
func TestTheOutputBudgetOverrideIsHonouredAndCapped(t *testing.T) {
	t.Setenv("DMCODE_MAX_OUTPUT", "40000")
	m := NewChatModel("http://example.invalid/v1", "", "gpt-4o")
	if got := m.outputBudget(0); got != 40_000 {
		t.Errorf("override ignored: %d, want 40000", got)
	}
	small := NewChatModel("http://example.invalid/v1", "", "gemma-2-9b")
	if got := small.outputBudget(0); got != 4096 {
		t.Errorf("override must still be capped by the window, got %d", got)
	}
}

// A budget the caller chose is left alone. The tool probe asks for 64 on
// purpose, and the truncation re-ask has already computed its own.
func TestACallersOwnBudgetIsNotOverridden(t *testing.T) {
	t.Setenv("DMCODE_MAX_OUTPUT", "40000")
	m := NewChatModel("http://example.invalid/v1", "", "gpt-4o")
	if got := m.outputBudget(64); got != 64 {
		t.Errorf("outputBudget(64) = %d, want 64", got)
	}
}

// The regression this guards against is moving the default from doRequest into
// buildChatRequest, which looks equivalent and silently disables the re-ask:
// reAskable reads the built body, and a body that always carries a budget is
// indistinguishable from one the caller budgeted on purpose.
func TestTheDefaultBudgetDoesNotDisableTheTruncationReAsk(t *testing.T) {
	m := NewChatModel("http://example.invalid/v1", "", "gpt-4o")
	body, err := m.buildChatRequest(&model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, false)
	if err != nil {
		t.Fatalf("buildChatRequest: %v", err)
	}
	if body.MaxOutputTokens != 0 {
		t.Errorf("the built body already carries max_tokens=%d; the re-ask is now undatable", body.MaxOutputTokens)
	}
	if !reAskable(body) {
		t.Error("an ordinary request stopped being re-askable")
	}
}
