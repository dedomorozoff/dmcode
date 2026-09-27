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

// The polling default is the point of the whole feature, so keep it honest: the
// hosted list must contain a provider that needs no key at all.
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
	for _, k := range []string{"OPENCODE_API_KEY", "GROQ_API_KEY", "GITHUB_TOKEN", "MISTRAL_API_KEY", "OPENROUTER_API_KEY", "OPENAI_BASE_URL", "DMCODE_MODEL"} {
		t.Setenv(k, "")
	}
	provs, err := DetectProviders()
	if err != nil {
		t.Fatalf("DetectProviders: %v", err)
	}
	if len(provs) != 1 || provs[0].BaseURL != "https://api.kilo.ai/api/gateway" ||
		provs[0].Model != "kilo-auto/free" || provs[0].Wire() != config.APIChat {
		t.Errorf("KILO_API_KEY did not select the Kilo provider: %+v", provs)
	}
}
