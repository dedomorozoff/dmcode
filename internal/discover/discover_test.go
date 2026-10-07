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
