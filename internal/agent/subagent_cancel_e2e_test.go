package agent_test

// Does pressing Esc reach a delegation that is already in flight?
//
// The claim in the docs is that cancellation reaches the context but not a
// sub-agent mid-model-call — that the delegation finishes its turn on its own
// schedule. That is a claim about ADK's tool plumbing and the chat client, and
// it can be settled here: a parent turn that calls `sub_agent`, a sub endpoint
// that answers nothing until the client hangs up, and a parent context that is
// cancelled the moment the sub-agent's request arrives.
//
// If cancellation reaches the delegation, the sub-agent's HTTP request is
// interrupted and the turn ends in well under the watchdog. If the docs are
// right, the turn hangs on a stream that is still open — which is exactly what
// the failure message says.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/llm"
	"github.com/dedomorozoff/dmcode/internal/memsession"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// blockingChat is a chat endpoint that never answers: it accepts the request,
// flushes the headers so the client is genuinely waiting on the body, and then
// holds the stream open until either the client hangs up — which is what a
// reaching cancellation must cause — or its own minute is up.
//
// It reports two things: a request arrived, and a request was interrupted.
func blockingChat(t *testing.T) (baseURL string, arrived, hungUp chan struct{}) {
	t.Helper()
	arrived = make(chan struct{}, 8)
	hungUp = make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			once.Do(func() { close(hungUp) })
		case <-time.After(time.Minute):
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/v1", arrived, hungUp
}

// cancelToolSet is the act instrument set with one difference: the delegation
// tool points its sub-agent at the given provider, so the test can watch what
// the sub-agent's model call does.
func cancelToolSet(t *testing.T, sub []config.Provider) []tool.Tool {
	t.Helper()
	tools, err := dmtools.MakeTools()
	if err != nil {
		t.Fatal(err)
	}
	subTool, err := dmagent.SubAgentTool(sub, tools, nil)
	if err != nil {
		t.Fatal(err)
	}
	return append(tools, subTool)
}

func TestCancellationReachesTheSubAgentsModelCall(t *testing.T) {
	if err := dmtools.SetRoot(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	subURL, arrived, hungUp := blockingChat(t)
	parent := newEndpoint(t, toolCall(dmagent.SubAgentName, `{"task":"read something"}`))

	m, err := llm.BuildLLM(context.Background(), config.Provider{
		BaseURL: parent.url + "/v1", Model: "test-coder", API: config.APIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := dmagent.BuildAgentMode(m, cancelToolSet(t, []config.Provider{{
		BaseURL: subURL, Model: "sub-coder", API: config.APIChat,
	}}), dmagent.ModeAct)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{
		AppName: "dmcode", Agent: a,
		SessionService: memsession.NewMemory(), AutoCreateSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		for _, err := range r.Run(ctx, "u1", "s-cancel",
			genai.NewContentFromText("delegate this", genai.RoleUser),
			adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE}) {
			if err != nil {
				res <- err
				return
			}
		}
		res <- nil
	}()

	// Wait for the delegation to actually reach its endpoint — the moment a
	// user would press Esc with a sub-agent in flight.
	select {
	case <-arrived:
	case err := <-res:
		t.Fatalf("the turn ended before the sub-agent reached its endpoint: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the sub-agent never reached its endpoint")
	}
	cancel()

	select {
	case <-res:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not reach the sub-agent: the turn is still waiting on its model call")
	}
	select {
	case <-hungUp:
	case <-time.After(2 * time.Second):
		// The turn ended, but the stream is still open: the client is gone
		// without hanging up, which means the delegation runs on behind a
		// finished turn. Two seconds is fair warning — a real disconnect
		// reaches the server within milliseconds, and if it never will, the
		// request lives for the sub-agent's full timeout.
		t.Error("the turn ended, but the sub-agent's HTTP request was not interrupted — the delegation is running on behind a finished turn")
	}
}
