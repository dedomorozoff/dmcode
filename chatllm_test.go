package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestChatModelStreamingToolCall covers the hard case: one tool call whose id
// and name arrive in the first chunk and whose arguments are split across the
// next two, which must be reassembled into a single call.
func TestChatModelStreamingToolCall(t *testing.T) {
	srv := fakeChatServer(t, []string{
		`{"choices":[{"delta":{"content":"Смотрю."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.go\"}"}}]}}]}`,
	}, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	m := newChatModel(srv.URL+"/v1", "", "test-coder")
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
	m := newChatModel("http://example.invalid/v1", "", "m")
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

	m := newChatModel(srv.URL+"/v1", "dmcode", "test-coder")
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
	msgs := contentsToChat([]*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "сделай"}}},
		{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
			ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"},
		}}}},
		{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: "c1", Name: "read_file", Response: map[string]any{"output": "ok"},
		}}}},
	})
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

func TestPickModelPrefersConfiguredThenCoding(t *testing.T) {
	cand := freeCandidate{models: []string{"wanting", "test-coder"}}
	if got := pickModel(cand, []string{"test-coder", "other"}); got != "test-coder" {
		t.Fatalf("configured preference not honoured, got %q", got)
	}
	plain := freeCandidate{}
	if got := pickModel(plain, []string{"other", "my-coder-model"}); got != "my-coder-model" {
		t.Fatalf("coding model should outrank generic, got %q", got)
	}
	if got := pickModel(plain, nil); got != "" {
		t.Fatalf("no models served should yield empty model, got %q", got)
	}
}

func TestProbeRejectsDeadEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, ok := probe(freeCandidate{baseURL: srv.URL}, 2*time.Second); ok {
		t.Fatal("endpoint returning 500 must not be reported as usable")
	}
}

func TestDiscoverPrefersLocalOverHosted(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"local-coder"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer local.Close()

	// Shrink the candidate set to the single reachable local server so the
	// test does not depend on what happens to be listening on the machine.
	origLocal, origHosted := localCandidates, hostedCandidates
	localCandidates = []freeCandidate{{name: "TestLocal", baseURL: local.URL + "/v1", local: true}}
	hostedCandidates = nil
	defer func() { localCandidates, hostedCandidates = origLocal, origHosted }()

	p, ok := discoverFreeProvider()
	if !ok {
		t.Fatal("local server should have been discovered")
	}
	if p.model != "local-coder" {
		t.Errorf("model = %q, want local-coder", p.model)
	}
	if p.wire() != apiChat {
		t.Errorf("local servers speak the chat wire, got %q", p.wire())
	}
}

func TestProviderWireDefaultsToResponses(t *testing.T) {
	if got := (provider{}).wire(); got != apiResponses {
		t.Errorf("empty provider should default to responses, got %q", got)
	}
	if got := (provider{api: apiChat}).wire(); got != apiChat {
		t.Errorf("explicit chat wire lost, got %q", got)
	}
}
