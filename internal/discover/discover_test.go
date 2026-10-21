package discover

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dedomorozoff/dmcode/internal/config"
)

func TestPickModelPrefersConfiguredThenCoding(t *testing.T) {
	cand := freeCandidate{Models: []string{"wanting", "test-coder"}}
	if got := pickModel(cand, []servedModel{{ID: "test-coder"}, {ID: "other"}}); got != "test-coder" {
		t.Fatalf("configured preference not honoured, got %q", got)
	}
	plain := freeCandidate{}
	if got := pickModel(plain, []servedModel{{ID: "other"}, {ID: "my-coder-model"}}); got != "my-coder-model" {
		t.Fatalf("coding model should outrank generic, got %q", got)
	}
	if got := pickModel(plain, nil); got != "" {
		t.Fatalf("no models served should yield empty model, got %q", got)
	}
}

// The endpoint's own context length travels from /v1/models onto the provider:
// it is the number the meter and the compaction threshold are allowed to trust
// over the heuristic table.
func TestTheEndpointReportedWindowTravelsOntoTheProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"local-coder","max_model_len":131072}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origLocal, origHosted := localCandidates, hostedCandidates
	localCandidates = []freeCandidate{{name: "TestLocal", BaseURL: srv.URL + "/v1", local: true}}
	hostedCandidates = nil
	defer func() { localCandidates, hostedCandidates = origLocal, origHosted }()

	p, ok := DiscoverFreeProvider()
	if !ok {
		t.Fatal("local server should have been discovered")
	}
	if p.Context != 131_072 {
		t.Errorf("Context = %d, want 131072", p.Context)
	}
}

// llama.cpp names the context it actually runs with under meta, not at the
// top level. A parser that reads only the OpenAI-style aliases leaves a local
// provider with no window, which switches compaction off for the model that
// needs it most.
func TestLlamaCppSlotContextIsReadFromMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
			{"id":"slot","meta":{"n_ctx":16384,"n_ctx_train":131072}},
			{"id":"train-only","meta":{"n_ctx_train":32768}},
			{"id":"top-level","context_length":8192}
		]}`)
	}))
	defer srv.Close()

	served, err := fetchModels(srv.URL+"/v1", "", time.Second)
	if err != nil {
		t.Fatalf("fetchModels: %v", err)
	}
	want := map[string]int{
		"slot":       16_384, // the enforced slot context, not the training cap
		"train-only": 32_768,
		"top-level":  8_192,
	}
	for _, m := range served {
		if got, ok := want[m.ID]; ok && m.Context != got {
			t.Errorf("%s context = %d, want %d", m.ID, m.Context, got)
		}
	}
}

// A server that says nothing about its window yields a provider whose Context
// is zero, so callers fall back to the name-to-tokens table.
func TestASilentEndpointLeavesTheWindowZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"local-coder"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origLocal, origHosted := localCandidates, hostedCandidates
	localCandidates = []freeCandidate{{name: "TestLocal", BaseURL: srv.URL + "/v1", local: true}}
	hostedCandidates = nil
	defer func() { localCandidates, hostedCandidates = origLocal, origHosted }()

	p, ok := DiscoverFreeProvider()
	if !ok {
		t.Fatal("local server should have been discovered")
	}
	if p.Context != 0 {
		t.Errorf("Context = %d, want 0", p.Context)
	}
}

func TestProbeRejectsDeadEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, _, ok := probe(freeCandidate{BaseURL: srv.URL}, 2*time.Second); ok {
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
	localCandidates = []freeCandidate{{name: "TestLocal", BaseURL: local.URL + "/v1", local: true}}
	hostedCandidates = nil
	defer func() { localCandidates, hostedCandidates = origLocal, origHosted }()

	p, ok := DiscoverFreeProvider()
	if !ok {
		t.Fatal("local server should have been discovered")
	}
	if p.Model != "local-coder" {
		t.Errorf("model = %q, want local-coder", p.Model)
	}
	if p.Wire() != config.APIChat {
		t.Errorf("local servers speak the chat wire, got %q", p.Wire())
	}
}
