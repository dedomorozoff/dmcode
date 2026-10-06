package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// fakeChatServer emulates an OpenAI-compatible /chat/completions endpoint with
// SSE streaming, so the tests exercise the same wire format a real provider
// sends.
func fakeChatServer(t *testing.T, chunks []string, final string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"test-coder"},{"id":"other"}]}`)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a flusher")
			return
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		if final != "" {
			fmt.Fprintf(w, "data: %s\n\n", final)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A streamed turn has to ask for its usage explicitly. Without the field an
// OpenAI-compatible server sends usage only on a non-streaming request, so
// every token counter in the UI stays at zero — which is not a display bug but
// a number the endpoint was never asked for.
func TestAStreamedTurnAsksForUsage(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	m := NewChatModel(srv.URL, "", "m")
	for range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
	}

	opts, ok := body["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("stream_options.include_usage was not requested: %v", body)
	}
}

// A server that rejects the field outright must not break the turn: the retry
// costs the token counters, which is a far smaller loss than a provider that
// refuses to answer at all.
func TestARejectedStreamOptionsFieldIsRetriedWithoutIt(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["stream_options"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"unknown field"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	m := NewChatModel(srv.URL, "", "m")
	var text string
	for resp, err := range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
		if err != nil {
			t.Fatalf("the turn failed against a server that rejects the field: %v", err)
		}
		// Only the aggregated final response is the answer: the deltas before it
		// carry the same text, and counting both would say the model replied
		// twice — which is the shape a broken retry actually produces, so the
		// test would pass on the bug it exists to catch.
		if resp != nil && resp.TurnComplete && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				text += p.Text
			}
		}
	}
	if text != "hi" {
		t.Errorf("the retry produced %q, want the answer", text)
	}
	if calls < 2 {
		t.Errorf("the request was not retried (%d calls)", calls)
	}
}

// A gateway may validate the payload itself and report its own rejection under
// a different code. Pollinations answers with HTTP 500 carrying
// `{"error":"400 Bad Request"}`, so a retry keyed on the 400 alone never fires
// and every turn against that provider fails — which is exactly the regression
// this case was found in.
func TestARejectedStreamOptionsWrappedInA500IsStillRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["stream_options"]; ok {
			// The real shape: a 5xx whose payload describes a 400.
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"400 Bad Request","status":500}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	m := NewChatModel(srv.URL, "", "m")
	var text string
	for resp, err := range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
		if err != nil {
			t.Fatalf("the turn failed against a gateway that rejects the field: %v", err)
		}
		if resp != nil && resp.TurnComplete && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				text += p.Text
			}
		}
	}
	if text != "hi" {
		t.Errorf("the retry produced %q, want the answer", text)
	}
	if calls < 2 {
		t.Errorf("the request was not retried (%d calls)", calls)
	}
}

// A 5xx that is an actual server failure must not trigger the retry: retrying a
// rate limit or an outage without the field would double every such request for
// no reason.
func TestAGenuineServerErrorIsNotRetriedAsAFieldRejection(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"upstream is overloaded"}}`)
	}))
	defer srv.Close()

	m := NewChatModel(srv.URL, "", "m")
	for range m.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "q"}}}},
	}, true) {
	}
	if calls != 1 {
		t.Errorf("a real 500 was retried %d times; the field is not the cause", calls)
	}
}

// TestChatModelStreamingToolCall covers the hard case: one tool call whose id
// and name arrive in the first chunk and whose arguments are split across the
// next two, which must be reassembled into a single call.
func TestChatModelStreamingToolCall(t *testing.T) {
	srv := fakeChatServer(t, []string{
		`{"choices":[{"delta":{"content":"Смотрю."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.go\"}"}}]}}]}`,
	}, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	m := NewChatModel(srv.URL+"/v1", "", "test-coder")
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "read a.go"}}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "read_file",
				Description: "Reads a file",
				Parameters: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"path": {Type: genai.TypeString, Description: "file path"},
					},
					Required: []string{"path"},
				},
			}}}},
		},
	}

	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("stream returned error: %v", err)
		}
		if !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("no aggregated final event")
	}

	var call *genai.FunctionCall
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			call = p.FunctionCall
		}
	}
	if call == nil {
		t.Fatal("tool call not reassembled from streamed fragments")
	}
	if call.Name != "read_file" || call.ID != "call_1" {
		t.Errorf("call identity = %s/%s, want read_file/call_1", call.Name, call.ID)
	}
	if got, _ := call.Args["path"].(string); got != "a.go" {
		t.Errorf("split arguments not joined, got %q want a.go", got)
	}
}

// TestBuildChatRequestToolShape guards the JSON Schema conversion: genai spells
// types in uppercase, and a chat endpoint rejects "OBJECT".
func TestBuildChatRequestToolShape(t *testing.T) {
	m := NewChatModel("http://example.invalid/v1", "", "m")
	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "be brief"}}},
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name: "t",
				Parameters: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"f":   {Type: genai.TypeString},
						"arr": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeInteger}},
					},
				},
			}}}},
		},
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "go"}}}},
	}
	body, err := m.buildChatRequest(req, false)
	if err != nil {
		t.Fatalf("buildChatRequest: %v", err)
	}
	if body.Messages[0].Role != "system" {
		t.Errorf("system instruction must lead the messages, got %q", body.Messages[0].Role)
	}
	if body.Messages[1].Role != "user" {
		t.Errorf("expected user message second, got %q", body.Messages[1].Role)
	}
	if len(body.Tools) != 1 || body.Tools[0].Type != "function" {
		t.Fatalf("tool not carried over: %+v", body.Tools)
	}
	raw, err := json.Marshal(body.Tools[0].Function.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "OBJECT") || strings.Contains(string(raw), "STRING") {
		t.Errorf("schema types must be lowercase JSON Schema, got %s", raw)
	}
	if !strings.Contains(string(raw), `"type":"object"`) {
		t.Errorf("missing object type: %s", raw)
	}
}

// TestChatModelStreamingText checks that deltas surface as partial events and
// that the turn is closed by one aggregated event carrying TurnComplete.
func TestChatModelStreamingText(t *testing.T) {
	srv := fakeChatServer(t,
		[]string{`{"model":"m","choices":[{"delta":{"content":"При"}}]}`},
		`{"model":"m","choices":[{"delta":{"content":"вет"},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`)

	m := NewChatModel(srv.URL+"/v1", "dmcode", "test-coder")
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "hi"}}}},
	}

	var partial strings.Builder
	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("stream returned error: %v", err)
		}
		if resp.Partial {
			partial.WriteString(resp.Content.Parts[0].Text)
			continue
		}
		final = resp
	}

	if partial.String() != "Привет" {
		t.Fatalf("partial deltas = %q, want Привет", partial.String())
	}
	if final == nil {
		t.Fatal("no aggregated final event")
	}
	if !final.TurnComplete {
		t.Error("final event must set TurnComplete so the runner persists the turn")
	}
	if got := final.Content.Parts[0].Text; got != "Привет" {
		t.Errorf("aggregated text = %q, want Привет", got)
	}
	if final.UsageMetadata == nil || final.UsageMetadata.TotalTokenCount != 7 {
		t.Errorf("usage not propagated: %+v", final.UsageMetadata)
	}
}

// TestContentsToChatToolLoop verifies the tool round-trip shape the chat API
// requires: an assistant message carrying tool_calls followed by tool messages
// keyed by tool_call_id.
func TestContentsToChatToolLoop(t *testing.T) {
	msgs, err := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "сделай"}}},
		{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
			ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"},
		}}}},
		{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: "c1", Name: "read_file", Response: map[string]any{"output": "ok"},
		}}}},
	})
	if err != nil {
		t.Fatalf("contentsToChat: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls message malformed: %+v", msgs[1])
	}
	if !strings.Contains(msgs[1].ToolCalls[0].Function.Arguments, "a.go") {
		t.Errorf("call arguments not serialised: %s", msgs[1].ToolCalls[0].Function.Arguments)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "c1" {
		t.Fatalf("tool result message malformed: %+v", msgs[2])
	}
}

// TestChatModelTruncatedToolCall covers what Pollinations actually does to a
// long write_file: the output cap cuts the arguments mid-JSON and the stream
// still ends cleanly with finish_reason=length. The turn must fail with an
// error that names the real cause instead of a bare JSON parser complaint.
func TestChatModelTruncatedToolCall(t *testing.T) {
	srv := fakeChatServer(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"index.html\",\"content\":\"<html>"}}]}}]}`,
	}, `{"choices":[{"delta":{},"finish_reason":"length"}]}`)

	m := NewChatModel(srv.URL+"/v1", "", "openai-fast")
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "напиши тетрис"}}}},
	}

	var gotErr error
	for _, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatal("truncated tool call must fail the turn")
	}
	msg := gotErr.Error()
	if !strings.Contains(msg, "finish_reason=length") {
		t.Errorf("error must name the token-limit truncation, got %q", msg)
	}
	if strings.Contains(msg, "не удалось разобрать") {
		t.Errorf("truncation must not be reported as a plain parse failure, got %q", msg)
	}
}

// TestBuildChatRequestReasoningEffort checks that the configured reasoning
// level reaches the wire and that an unset one stays off the request entirely,
// so providers that never heard of the field see no surprises.
func TestBuildChatRequestReasoningEffort(t *testing.T) {
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "hi"}}}},
	}

	plain := NewChatModel("http://example.invalid/v1", "", "m")
	body, err := plain.buildChatRequest(req, false)
	if err != nil {
		t.Fatalf("buildChatRequest: %v", err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "reasoning_effort") {
		t.Errorf("unset reasoning effort must not be sent, got %s", raw)
	}

	capped := NewChatModel("http://example.invalid/v1", "", "m")
	capped.setReasoningEffort("low")
	body, err = capped.buildChatRequest(req, false)
	if err != nil {
		t.Fatalf("buildChatRequest: %v", err)
	}
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reasoning_effort":"low"`) {
		t.Errorf("reasoning_effort missing from wire body: %s", raw)
	}
}
