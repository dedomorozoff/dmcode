package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// TestStreamStopsWhenTheConsumerWalksAway covers the stream that is abandoned
// mid-answer — a cancelled turn, or a runner that has what it needs.
//
// Yielding after a yield returned false is not a harmless extra event: a
// range-over-func panics with "continued iteration after function for loop body
// returned false", and the panic surfaces as a crashed agent node rather than
// as the cancellation the caller asked for.
func TestStreamStopsWhenTheConsumerWalksAway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < 20; i++ {
			fmt.Fprintf(w, "data: %s\n\n",
				fmt.Sprintf(`{"model":"m","choices":[{"delta":{"content":"word%d "}}]}`, i))
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)

	m := NewChatModel(srv.URL, "k", "test-model")
	req := &model.LLMRequest{Contents: []*genai.Content{
		genai.NewContentFromText("hi", genai.RoleUser),
	}}

	seen := 0
	for range m.GenerateContent(context.Background(), req, true) {
		seen++
		if seen == 2 {
			break // walk away mid-stream, as a cancelled turn does
		}
	}
	if seen != 2 {
		t.Errorf("saw %d events, want 2", seen)
	}
}

// TestStreamDeliversTheClosingEventWhenTheConsumerStays is the other half: the
// aggregated final event is what the runner needs to persist a turn and
// dispatch tools, so abandoning the stream must be the only way to lose it.
func TestStreamDeliversTheClosingEventWhenTheConsumerStays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: %s\n\n",
				fmt.Sprintf(`{"model":"m","choices":[{"delta":{"content":"w%d"}}]}`, i))
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)

	m := NewChatModel(srv.URL, "k", "test-model")
	req := &model.LLMRequest{Contents: []*genai.Content{
		genai.NewContentFromText("hi", genai.RoleUser),
	}}

	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("a consumed stream delivered no closing event")
	}
	if !final.TurnComplete {
		t.Error("the closing event is not marked complete")
	}
	var text string
	for _, p := range final.Content.Parts {
		text += p.Text
	}
	if text != "w0w1w2" {
		t.Errorf("closing event carried %q, want %q", text, "w0w1w2")
	}
}

// TestStreamErrorMidAnswerStillClosesTheTurn: an error frame arrives after some
// text. The consumer has to get the error and still not be panicked into.
func TestStreamErrorMidAnswerStillClosesTheTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, `data: {"model":"m","choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, `data: {"error":{"message":"upstream exploded"}}`+"\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)

	m := NewChatModel(srv.URL, "k", "test-model")
	req := &model.LLMRequest{Contents: []*genai.Content{
		genai.NewContentFromText("hi", genai.RoleUser),
	}}

	var gotErr error
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			gotErr = err
			continue
		}
		_ = resp
	}
	if gotErr == nil {
		t.Fatal("the error frame was not reported")
	}
	if !strings.Contains(gotErr.Error(), "upstream exploded") {
		t.Errorf("error = %v, want the provider's message", gotErr)
	}
}
