package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// freeCandidate is a provider dmcode can use without the user configuring
// anything: a locally running inference server, or a hosted endpoint that
// needs no API key at all.
type freeCandidate struct {
	name    string
	baseURL string
	apiKey  string
	// models are tried in order; the first one the endpoint actually serves
	// wins. Empty means "ask /v1/models and pick the best-looking match".
	models []string
	// local candidates are probed first and are preferred when present.
	local bool
}

// localCandidates covers the OpenAI-compatible servers people run on their own
// machine. Ports are the documented defaults for each project.
var localCandidates = []freeCandidate{
	{name: "Ollama", baseURL: "http://127.0.0.1:11434/v1", models: []string{"qwen2.5-coder:7b", "qwen3-coder:30b", "llama3.1:8b"}, local: true},
	{name: "LM Studio", baseURL: "http://127.0.0.1:1234/v1", local: true},
	{name: "llama.cpp", baseURL: "http://127.0.0.1:8080/v1", local: true},
	{name: "vLLM", baseURL: "http://127.0.0.1:8000/v1", local: true},
	{name: "Jan", baseURL: "http://127.0.0.1:1337/v1", local: true},
}

// hostedCandidates need no key. Pollinations serves an OpenAI-compatible
// /chat/completions API anonymously; "dmcode" is used as the bearer token
// because the endpoint rejects a request that carries no Authorization header.
var hostedCandidates = []freeCandidate{
	{name: "Pollinations (без ключа)", baseURL: "https://text.pollinations.ai/openai", apiKey: "dmcode", models: []string{"openai-fast", "openai"}},
}

// preferKeywords rank model ids returned by /v1/models: coding-tuned models
// first, then larger ones, so an arbitrary local server still gets a usable
// default without the user picking.
var preferKeywords = []string{"coder", "code", "starcoder", "deepseek-coder"}

func fetchModels(baseURL, apiKey string, timeout time.Duration) ([]string, error) {
	client := &http.Client{Timeout: timeout}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer dmcode")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", baseURL, resp.Status)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// pickModel resolves the model to use for a candidate: the first configured
// preference the server lists, otherwise the best-ranked model it reports.
func pickModel(cand freeCandidate, served []string) string {
	has := func(id string) bool {
		for _, s := range served {
			if s == id {
				return true
			}
		}
		return false
	}
	for _, want := range cand.models {
		if has(want) {
			return want
		}
	}
	if len(served) == 0 {
		if len(cand.models) > 0 {
			return cand.models[0]
		}
		return ""
	}
	ranked := append([]string(nil), served...)
	score := func(id string) int {
		lower := strings.ToLower(id)
		for i, kw := range preferKeywords {
			if strings.Contains(lower, kw) {
				// Lower is better: a coding model outranks a generic one.
				return len(preferKeywords) - i
			}
		}
		return 0
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		si, sj := score(ranked[i]), score(ranked[j])
		if si != sj {
			return si > sj
		}
		return ranked[i] < ranked[j]
	})
	return ranked[0]
}

// probe checks that a candidate is reachable and usable, returning the model
// to drive it with. A candidate that answers /models but lists nothing is
// treated as unusable, since there would be no model to run.
func probe(cand freeCandidate, timeout time.Duration) (string, bool) {
	served, err := fetchModels(cand.baseURL, cand.apiKey, timeout)
	if err != nil {
		return "", false
	}
	model := pickModel(cand, served)
	if model == "" {
		return "", false
	}
	return model, true
}

// discoverFreeProvider looks for a usable free provider, preferring a local
// server over a hosted keyless one. Probes run concurrently with a short
// timeout so startup stays fast even when nothing is listening.
func discoverFreeProvider() (provider, bool) {
	all := append(append([]freeCandidate{}, localCandidates...), hostedCandidates...)

	type result struct {
		cand  freeCandidate
		model string
		ok    bool
	}
	results := make([]result, len(all))

	var wg sync.WaitGroup
	for i, cand := range all {
		wg.Add(1)
		go func(i int, cand freeCandidate) {
			defer wg.Done()
			// Local sockets answer instantly or not at all; a short deadline
			// keeps an absent server from stalling startup.
			timeout := 1200 * time.Millisecond
			if !cand.local {
				timeout = 5 * time.Second
			}
			model, ok := probe(cand, timeout)
			results[i] = result{cand: cand, model: model, ok: ok}
		}(i, cand)
	}
	wg.Wait()

	// Order is preserved, so locals win over hosted when both are available.
	for _, r := range results {
		if r.ok {
			return provider{
				baseURL: r.cand.baseURL,
				apiKey:  r.cand.apiKey,
				model:   r.model,
				api:     apiChat,
				label:   r.cand.name,
			}, true
		}
	}
	return provider{}, false
}

// freeProviderHint is shown when nothing was auto-detected, so the user knows
// the no-key path exists.
const freeProviderHint = "подними локально Ollama (ollama serve) или LM Studio — dmcode подхватит её сам; " +
	"либо укажи любой OpenAI-совместимый endpoint через /setup"
