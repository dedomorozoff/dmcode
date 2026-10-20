package llm

// context_test.go — the typed context-window failure: recognised from the
// bodies the OpenAI-compatible endpoints actually print, still a 400 to the
// pool, and producible of a restart figure from the numbers it names.

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func contextResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestTheLlamaCppBodyIsTypedApart drives the exact reply a llama-server with
// its default window gives an agent turn that outgrew it — the failure this
// whole seam exists for.
func TestTheLlamaCppBodyIsTypedApart(t *testing.T) {
	body := `{"error":{"message":"request (5701 tokens) exceeds the available context size (4096 tokens), try increasing it"}}`
	err := httpError("POST /v1/chat/completions", contextResponse(body))
	var e *ContextExceededError
	if !errors.As(err, &e) {
		t.Fatalf("httpError = %v, want a *ContextExceededError", err)
	}
	// The message is untouched: the body is the diagnosis, the type adds only
	// what the UI can act on.
	want := "POST /v1/chat/completions: 400 Bad Request: request (5701 tokens) exceeds the available context size (4096 tokens), try increasing it"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if len(e.Numbers) != 2 || e.Numbers[0] != 5701 || e.Numbers[1] != 4096 {
		t.Errorf("Numbers = %v, want [5701 4096]", e.Numbers)
	}
	if got := e.SuggestedContext(); got != 12288 {
		t.Errorf("SuggestedContext = %d, want 12288 — twice the larger figure, rounded up to 1024", got)
	}
}

// TestTheOpenAIOrderIsTypedApartTheSameWay covers the sibling shape: OpenAI
// and vLLM name the window first and the request second, so the numbers are
// deliberately not read into roles — only the larger of them drives the
// restart figure.
func TestTheOpenAIOrderIsTypedApartTheSameWay(t *testing.T) {
	body := `This model's maximum context length is 8192 tokens. However, you requested 10000 tokens (prompt + output).`
	err := httpError("POST /v1/chat/completions", contextResponse(body))
	var e *ContextExceededError
	if !errors.As(err, &e) {
		t.Fatalf("httpError = %v, want a *ContextExceededError", err)
	}
	if len(e.Numbers) != 2 || e.Numbers[0] != 8192 || e.Numbers[1] != 10000 {
		t.Errorf("Numbers = %v, want [8192 10000]", e.Numbers)
	}
	if got := e.SuggestedContext(); got != 20480 {
		t.Errorf("SuggestedContext = %d, want 20480", got)
	}
}

// TestAPlainBodyIsTypedApartToo — a reply whose body is text rather than the
// chat error object still carries the message, and the type must not lose it:
// some endpoints answer a 400 without any JSON envelope at all.
func TestAPlainBodyIsTypedApartToo(t *testing.T) {
	body := `input length exceeds context length`
	err := httpError("POST /v1/chat/completions", contextResponse(body))
	var e *ContextExceededError
	if !errors.As(err, &e) {
		t.Fatalf("httpError = %v, want a *ContextExceededError", err)
	}
	if len(e.Numbers) != 0 {
		t.Errorf("Numbers = %v, want none — the body named no figures", e.Numbers)
	}
	if got := e.SuggestedContext(); got != 16384 {
		t.Errorf("SuggestedContext = %d, want the fallback 16384", got)
	}
}

func TestAnUnrelatedBodyKeepsThePlainError(t *testing.T) {
	body := `{"error":{"message":"unknown field: tools"}}`
	err := httpError("POST /v1/chat/completions", contextResponse(body))
	var ce *ContextExceededError
	if errors.As(err, &ce) {
		t.Fatalf("an unknown-field 400 came back typed as a full context window: %v", err)
	}
	var pe *providerError
	if !errors.As(err, &pe) {
		t.Fatalf("httpError = %v, want a *providerError", err)
	}
}

// TestTheTypedErrorIsStillA400ToThePool: the type must not change how the
// failover pool answers it. A window that is too small on this host is too
// small on every host — walking the pool burns each member on the same
// verdict.
func TestTheTypedErrorIsStillA400ToThePool(t *testing.T) {
	body := `{"error":{"message":"request (5701 tokens) exceeds the available context size (4096 tokens), try increasing it"}}`
	err := httpError("POST /v1/chat/completions", contextResponse(body))
	if retryable(err) {
		t.Error("a context-size 400 must not be routed to another endpoint")
	}
	if sameEndpointRetryable(err) {
		t.Error("asking the same host again repeats the same rejection")
	}
}
