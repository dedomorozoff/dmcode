package discover

import (
	"os"
	"strings"
	"testing"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// An explicit endpoint needs no key: keyless hosts and a local Ollama have none,
// and demanding one would push users to invent a dummy secret.
func TestExplicitBaseURLNeedsNoKey(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"OPENAI_BASE_URL", "OPENAI_API_KEY", "DMCODE_MODEL", "DMCODE_API", "OPENCODE_API_KEY"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DMCODE_MODEL", "qwen2.5-coder:7b")

	p, err := DetectProvider()
	if err != nil {
		t.Fatalf("detectProvider: %v", err)
	}
	if p.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("baseURL = %q, want the configured endpoint", p.BaseURL)
	}
	if p.Model != "qwen2.5-coder:7b" {
		t.Errorf("model = %q", p.Model)
	}
}

// A candidate in this list is probed with no key, so every entry must be a host
// that answers an unauthenticated request *and carries a tool call*. Answering
// /models is not enough, and it is not even necessary — a bare-completion probe
// passes on a host that refuses every real turn.
func TestHostedCandidatesAreKeyless(t *testing.T) {
	if len(hostedCandidates) == 0 {
		t.Fatal("no hosted free candidate: a fresh checkout would need setup")
	}
	for _, c := range hostedCandidates {
		if !strings.HasPrefix(c.BaseURL, "https://") {
			t.Errorf("%s uses %q, which is not TLS", c.name, c.BaseURL)
		}
		if c.local {
			t.Errorf("%s is listed as hosted but marked local", c.name)
		}
		if len(c.Models) == 0 {
			t.Errorf("%s names no model to fall back on", c.name)
		}
	}
}

// Pollinations has two hosts and they answer opposite questions, so both facts
// have to hold at once or the list is wrong in one direction or the other.
//
// The anonymous text host belongs in the probed list: it serves the exact body
// dmcode sends — stream, tools, stream_options and reasoning_effort — without a
// key, which is the whole free path. gen.pollinations.ai must stay out of it: the
// host answers 401 as soon as a body carries `tools`, but its /models is open,
// so a candidate there would pass the probe and fail every real turn.
func TestPollinationsHostsAreSplitByWhatTheyServeWithoutAKey(t *testing.T) {
	var anonymous bool
	for _, c := range hostedCandidates {
		if !strings.Contains(c.BaseURL, "pollinations.ai") {
			continue
		}
		if c.BaseURL != "https://text.pollinations.ai/openai" {
			t.Errorf("%s probes %s with no key, which answers 401 to any tools request", c.name, c.BaseURL)
		}
		if c.Models[0] != "openai-fast" {
			t.Errorf("%s defaults to %q, want openai-fast", c.name, c.Models[0])
		}
		if c.Reasoning != "low" {
			t.Errorf("%s does not cap reasoning: GPT-OSS truncates its tool call arguments at the anonymous output cap", c.name)
		}
		anonymous = true
	}
	if !anonymous {
		t.Fatal("no anonymous Pollinations candidate: a checkout with nothing configured would need setup")
	}

	var preset keyedPreset
	found := false
	for _, p := range keyedPresets() {
		if p.env == "POLLINATIONS_API_KEY" {
			preset, found = p, true
		}
	}
	if !found {
		t.Fatal("POLLINATIONS_API_KEY is not a preset: a key in the environment would select nothing")
	}
	if preset.url != "https://gen.pollinations.ai/v1" {
		t.Errorf("preset base = %q, want %q", preset.url, "https://gen.pollinations.ai/v1")
	}
	if preset.API != config.APIChat {
		t.Errorf("wire = %q, want %q", preset.API, config.APIChat)
	}
}

// The preset scan stops at the first key it finds, so a paid provider must come
// after every free tier: exporting both Pollinations and Cerebras should still
// land on Cerebras, not bill the user for the one that happens to sort first.
func TestPollinationsDoesNotOutrankAFreeTier(t *testing.T) {
	presets := keyedPresets()
	var pollinations, free int = -1, -1
	for i, p := range presets {
		if p.env == "POLLINATIONS_API_KEY" {
			pollinations = i
		}
		if config.FreeEndpoint(p.url) {
			free = i
		}
	}
	if pollinations < 0 {
		t.Fatal("no Pollinations preset")
	}
	if free >= 0 && free > pollinations {
		t.Errorf("free endpoint at %d sorts after Pollinations at %d", free, pollinations)
	}
}

// Unsloth speaks OpenAI at /v1/chat/completions but authenticates every request
// and binds a port it prints at run time, so it can only be offered as a choice
// the user fills in. Two mistakes are easy to make here and both produce a
// broken option: listing it as keyless, or giving it a guessed default port.
func TestUnslothIsOfferedAsAUserSuppliedEndpoint(t *testing.T) {
	var opt config.SetupOption
	found := 0
	for _, o := range config.SetupOptions() {
		if strings.Contains(o.Label, "Unsloth") {
			opt, found = o, found+1
		}
	}
	if found != 1 {
		t.Fatalf("Unsloth appears %d times in /setup, want exactly 1", found)
	}
	if opt.Keyless {
		t.Error("Unsloth is marked keyless, but it rejects unauthenticated requests")
	}
	if opt.EnvKey == "" {
		t.Error("Unsloth needs somewhere to keep its sk-unsloth-… key")
	}
	if opt.BaseURL != "" {
		t.Errorf("Unsloth has a preset endpoint %q, but the port is chosen at run time", opt.BaseURL)
	}
	if opt.Signup == "" {
		t.Error("Unsloth should point at its API docs, since the key is created in-app")
	}
	if opt.API != config.APIChat {
		t.Errorf("Unsloth api = %q, want %q (it serves /v1/chat/completions)", opt.API, config.APIChat)
	}

	// It must not be in the auto-probed list either: a candidate is probed with
	// no key, so Unsloth would only ever produce a 401 and cost a round trip.
	for _, c := range localCandidates {
		if strings.Contains(strings.ToLower(c.name), "unsloth") {
			t.Errorf("Unsloth is in localCandidates as %q, where it can never be probed successfully", c.name)
		}
	}
	for _, c := range hostedCandidates {
		if strings.Contains(strings.ToLower(c.name), "unsloth") {
			t.Errorf("Unsloth is listed as a hosted candidate %q, but it is a local server", c.name)
		}
	}
}

func TestCandidateModelsAreDistinct(t *testing.T) {
	for _, c := range append(append([]freeCandidate{}, localCandidates...), hostedCandidates...) {
		seen := map[string]bool{}
		for _, mdl := range c.Models {
			if seen[mdl] {
				t.Errorf("%s lists %q twice", c.name, mdl)
			}
			seen[mdl] = true
		}
	}
}

// TestKiloPresetSelected: a KILO_API_KEY in the environment must be picked up
// as a configured provider on the chat wire, pointing at the Kilo gateway with
// the free routing model.
func TestKiloPresetSelected(t *testing.T) {
	t.Setenv("KILO_API_KEY", "kilo-key")
	// The preset scan stops at the first key it finds, so the other preset
	// variables must be emptied even when the developer's shell carries them.
	for _, k := range []string{"OPENCODE_API_KEY", "GROQ_API_KEY", "GITHUB_TOKEN", "MISTRAL_API_KEY", "OPENROUTER_API_KEY", "CEREBRAS_API_KEY", "NVIDIA_API_KEY", "SAMBANOVA_API_KEY", "HF_TOKEN", "POLLINATIONS_API_KEY", "OPENAI_BASE_URL", "DMCODE_MODEL"} {
		t.Setenv(k, "")
	}
	provs, err := DetectProviders()
	if err != nil {
		t.Fatalf("DetectProviders: %v", err)
	}
	if len(provs.Pool) != 1 || provs.Pool[0].BaseURL != "https://api.kilo.ai/api/gateway" ||
		provs.Pool[0].Model != "kilo-auto/free" || provs.Pool[0].Wire() != config.APIChat {
		t.Errorf("KILO_API_KEY did not select the Kilo provider: %+v", provs.Pool)
	}
}

// The other half of the split: POLLINATIONS_API_KEY selects the *new* host, not
// the anonymous one both presets and probes could have reached. The two are
// different endpoints serving different models, and picking the wrong one is a
// silent downgrade rather than an error — gen.pollinations.ai answers /models
// without a key too, so a wrong base URL here looks fine until the first turn.
func TestPollinationsPresetSelected(t *testing.T) {
	for _, k := range []string{"OPENCODE_API_KEY", "CLINE_API_KEY", "GROQ_API_KEY", "GITHUB_TOKEN", "MISTRAL_API_KEY", "OPENROUTER_API_KEY", "KILO_API_KEY", "CEREBRAS_API_KEY", "NVIDIA_API_KEY", "SAMBANOVA_API_KEY", "HF_TOKEN", "OPENAI_BASE_URL", "DMCODE_MODEL"} {
		t.Setenv(k, "")
	}
	t.Setenv("POLLINATIONS_API_KEY", "sk_test")

	provs, err := DetectProviders()
	if err != nil {
		t.Fatalf("DetectProviders: %v", err)
	}
	if len(provs.Pool) != 1 || provs.Pool[0].BaseURL != "https://gen.pollinations.ai/v1" ||
		provs.Pool[0].Model != "openai/gpt-5.4-nano" || provs.Pool[0].Wire() != config.APIChat {
		t.Fatalf("POLLINATIONS_API_KEY did not select the Pollinations provider: %+v", provs.Pool)
	}
	if provs.Pool[0].APIKey != "sk_test" {
		t.Errorf("api key = %q, want the one in the environment", provs.Pool[0].APIKey)
	}
}

// A provider found automatically from an environment variable and the same
// provider chosen through /setup must land on the same endpoint and the same
// model. Otherwise a user with, say, NVIDIA_API_KEY exported runs on one model,
// and the same user picking NVIDIA in the wizard runs on another, with nothing
// in the UI to explain the difference.
//
// Presets are matched by URL rather than by key variable: the wizard writes the
// generic OPENAI_API_KEY for most providers (it also writes OPENAI_BASE_URL,
// which detect prefers), while a preset key of the same name is only a shortcut
// for people who set the variable by hand. The key name is therefore allowed to
// differ; the endpoint, the model and the wire are not.
func TestSetupOptionsCoverTheKeyedPresets(t *testing.T) {
	byURL := map[string]config.SetupOption{}
	for _, o := range config.SetupOptions() {
		if o.Keyless || o.BaseURL == "" {
			continue
		}
		byURL[o.BaseURL] = o
	}
	for _, p := range keyedPresets() {
		o, ok := byURL[p.url]
		if !ok {
			t.Errorf("%s selects %q but /setup does not offer it", p.env, p.Label)
			continue
		}
		if o.Model != p.Model {
			t.Errorf("%s: the preset uses model %q, /setup writes %q", p.env, p.Model, o.Model)
		}
		if o.API != p.API {
			t.Errorf("%s: the preset uses the %q wire, /setup writes %q", p.env, p.API, o.API)
		}
	}
}
