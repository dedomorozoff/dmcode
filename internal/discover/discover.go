package discover

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// freeCandidate is a provider dmcode can use without the user configuring
// anything: a locally running inference server, or a hosted endpoint that
// needs no API key at all.
type freeCandidate struct {
	name    string
	BaseURL string
	APIKey  string
	// models are tried in order; the first one the endpoint actually serves
	// wins. Empty means "ask /v1/models and pick the best-looking match".
	Models []string
	// local candidates are probed first and are preferred when present.
	local bool
	// reasoning is the OpenAI `reasoning_effort` to request from this endpoint.
	Reasoning string
}

// localCandidates covers the OpenAI-compatible servers people run on their own
// machine. Ports are the documented defaults for each project.
//
// Unsloth is deliberately absent. It does speak OpenAI at /v1/chat/completions,
// but it rejects every unauthenticated request, and it does not listen on a
// fixed port — `unsloth run` prints whatever it bound, and GET /v1/models only
// answers with a bearer key. A candidate here is probed with no key, so an entry
// could never succeed; it is offered by /setup instead, where the user pastes
// the URL, model and sk-unsloth-… key their own instance printed.
var localCandidates = []freeCandidate{
	{name: "Ollama", BaseURL: "http://127.0.0.1:11434/v1", Models: []string{"qwen2.5-coder:7b", "qwen3-coder:30b", "llama3.1:8b"}, local: true},
	{name: "LM Studio", BaseURL: "http://127.0.0.1:1234/v1", local: true},
	{name: "llama.cpp", BaseURL: "http://127.0.0.1:8080/v1", local: true},
	{name: "vLLM", BaseURL: "http://127.0.0.1:8000/v1", local: true},
	{name: "Jan", BaseURL: "http://127.0.0.1:1337/v1", local: true},
}

// hostedCandidates need no key. Pollinations serves an OpenAI-compatible
// /chat/completions API anonymously; "dmcode" is used as the bearer token
// because the endpoint rejects a request that carries no Authorization header.
//
// openai-fast and openai are aliases of the same anonymous model (GPT-OSS 20B),
// so listing both would only make the probe try the same thing twice.
//
// Other keyless hosts were probed live and rejected, so they do not belong
// here:
//   - Kilo (api.kilo.ai/api/gateway): Cloudflare answers 403 to every
//     unauthenticated request from a non-browser client, models and chat
//     alike. It is offered by /setup instead, where the free account key
//     unlocks kilo-auto/free.
//   - LLM7.io: the documented anonymous token is refused on almost every
//     model, and the one model that does answer (codestral-latest) ignores
//     the tools array entirely — a turn would degrade into prose.
//   - OVHcloud AI Endpoints: /models is open, but chat completions require
//     an OAuth-issued key, and the anonymous tier is ~2 requests/minute.
var hostedCandidates = []freeCandidate{
	// reasoning "low" is not a tuning choice but a necessity: GPT-OSS spends
	// its whole output budget on the analysis channel before the tool call
	// starts, and the anonymous cap truncates the call's arguments mid-JSON.
	{name: "Pollinations (без ключа)", BaseURL: "https://text.pollinations.ai/openai", APIKey: "dmcode", Models: []string{"openai-fast"}, Reasoning: "low"},
}

// preferKeywords rank model ids returned by /v1/models: coding-tuned models
// first, then larger ones, so an arbitrary local server still gets a usable
// default without the user picking.
var preferKeywords = []string{"coder", "code", "starcoder", "deepseek-coder"}

func fetchModels(BaseURL, APIKey string, timeout time.Duration) ([]string, error) {
	client := &http.Client{Timeout: timeout}
	url := strings.TrimRight(BaseURL, "/") + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+APIKey)
	} else {
		req.Header.Set("Authorization", "Bearer dmcode")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", BaseURL, resp.Status)
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
	for _, want := range cand.Models {
		if has(want) {
			return want
		}
	}
	if len(served) == 0 {
		if len(cand.Models) > 0 {
			return cand.Models[0]
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

// probe checks that a candidate is reachable and lists a model to run, which
// is the fast half of the decision. Whether that model can actually call tools
// is a separate, slower question answered off the startup path — see
// VerifyTools, which the UI runs in the background.
func probe(cand freeCandidate, timeout time.Duration) (string, bool) {
	served, err := fetchModels(cand.BaseURL, cand.APIKey, timeout)
	if err != nil {
		return "", false
	}
	modelName := pickModel(cand, served)
	if modelName == "" {
		return "", false
	}
	return modelName, true
}

// discoverFreeProviders probes every candidate and returns the working ones in
// preference order: a local server first, a keyless host after. Every candidate
// is probed, not just the first that answers — that is the whole point of the
// pool, since a second reachable endpoint is the difference between a turn that
// completes and a turn that ends in an error.
func DiscoverFreeProviders() []config.Provider {
	all := append(append([]freeCandidate{}, localCandidates...), hostedCandidates...)

	type result struct {
		cand  freeCandidate
		Model string
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
			results[i] = result{cand: cand, Model: model, ok: ok}
		}(i, cand)
	}
	wg.Wait()

	// Candidate order is preserved, so locals win over hosted when both are
	// available, and two servers on the same host stay in the order the list
	// declares rather than the order the network answered in.
	out := make([]config.Provider, 0, len(results))
	for _, r := range results {
		if r.ok {
			out = append(out, config.Provider{
				BaseURL:   r.cand.BaseURL,
				APIKey:    r.cand.APIKey,
				Model:     r.Model,
				API:       config.APIChat,
				Label:     r.cand.name,
				Reasoning: r.cand.Reasoning,
			})
		}
	}
	return out
}

// discoverFreeProvider is the first working candidate, for callers that want
// one endpoint and nothing else.
func DiscoverFreeProvider() (config.Provider, bool) {
	all := DiscoverFreeProviders()
	if len(all) == 0 {
		return config.Provider{}, false
	}
	return all[0], true
}
