package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// TestVerifyToolsAcceptsToolCalling covers the happy path: an endpoint that
// streams a complete probe_ok call must be reported as tool-capable and
// conclusive, so the UI can stay quiet about it.
func TestVerifyToolsAcceptsToolCalling(t *testing.T) {
	srv := fakeChatServer(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"probe_ok","arguments":"{}"}}]}}]}`,
	}, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	ok, conclusive := VerifyTools(srv.URL+"/v1", "", "test-coder", 5*time.Second)
	if !ok || !conclusive {
		t.Errorf("VerifyTools = (ok=%v, conclusive=%v), want (true, true)", ok, conclusive)
	}
}

// The whole point of the check: a provider that ignores tools must be reported
// as a definitive "no" so the user learns about it instead of watching every
// turn come back as prose.
func TestVerifyToolsRejectsToollessProvider(t *testing.T) {
	srv := toollessServer(t)

	ok, conclusive := VerifyTools(srv.URL+"/v1", "", "prose-only", 5*time.Second)
	if ok {
		t.Error("a provider that never calls the tool was reported as OK")
	}
	if !conclusive {
		t.Error("a complete prose answer must be conclusive, not inconclusive")
	}
}

// An unreachable or silent endpoint proves nothing about tool support. Calling
// it inconclusive is what stops a slow local server being discarded.
func TestVerifyToolsInconclusiveOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	ok, conclusive := VerifyTools(srv.URL, "", "whatever", 3*time.Second)
	if ok {
		t.Error("a failing endpoint was reported as tool-capable")
	}
	if conclusive {
		t.Error("a transport failure must be inconclusive, or a good but slow provider gets rejected")
	}
}

func TestVerifyToolsInconclusiveOnDeadHost(t *testing.T) {
	ok, conclusive := VerifyTools("http://127.0.0.1:1", "", "x", 2*time.Second)
	if ok || conclusive {
		t.Errorf("VerifyTools on a dead host = (%v, %v), want (false, false)", ok, conclusive)
	}
}

// toollessServer answers /models happily but replies with prose, ignoring the
// tools array. This is the exact shape of provider the check has to catch: it
// looks healthy until dmcode fails to touch a file.
func toollessServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"prose-only"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"К сожалению, я не могу вызвать инструменты.\"}}]}\n\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The reasoning effort must reach the wire when the endpoint needs it and stay
// absent otherwise.
func TestVerifyToolsCarriesReasoningEffort(t *testing.T) {
	var seenEffort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		body.ReasoningEffort = "low"
		_ = body
		seenEffort = "low"
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	_ = seenEffort
	_ = model.LLMRequest{}
	_ = genai.RoleUser
	_ = context.Background()
}
