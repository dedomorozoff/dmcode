package config

import "testing"

// TestFreeModelIDReadsWholeSegments: the badge is only as good as what it
// refuses. A substring test marks "freeplay" and "freestyle" free, which is a
// paid row behind a free badge — the one mistake this filter must not make.
func TestFreeModelIDReadsWholeSegments(t *testing.T) {
	for _, tc := range []struct {
		id   string
		free bool
	}{
		{"deepseek/deepseek-chat-v3.1:free", true},
		{"kilo-auto/free", true},
		{"some-model-free", true},
		{"some_model_free", true},
		{"openai/gpt-4o", false},
		{"meta-llama/Llama-3.3-70B-Instruct", false},
		{"freeplay-2", false},
		{"nvidia/llama-3.1-nemotron-freestyle", false},
		{"freestyle", false},
		{"", false},
	} {
		if got := FreeModelID(tc.id); got != tc.free {
			t.Errorf("FreeModelID(%q) = %v, want %v", tc.id, got, tc.free)
		}
	}
}

// TestFreeEndpointAsksTheEndpointNotTheId: the two halves of the same question
// do not overlap. OpenRouter tells you in the id, Groq tells you nothing in the
// id and everything in the endpoint, and a filter built on either half alone
// fails on the other.
func TestFreeEndpointAsksTheEndpointNotTheId(t *testing.T) {
	for _, tc := range []struct {
		base string
		free bool
	}{
		// Local: somebody's own machine, whatever it runs.
		{"http://127.0.0.1:11434/v1", true},
		{"http://localhost:1234/v1", true},
		{"http://192.168.1.10:8080/v1", false},
		// Two spellings of one endpoint, because .env holds whatever was pasted.
		{"https://api.groq.com/openai/v1", true},
		{"https://api.groq.com/openai/v1/", true},
		{"API.GROQ.COM/openai/v1", true},
		// Free tiers that need a key.
		{"https://models.github.ai/inference", true},
		{"https://api.cerebras.ai/v1", true},
		// Mixed catalogues: free here means "some of them", which the id says.
		{"https://openrouter.ai/api/v1", false},
		{"https://api.mistral.ai/v1", false},
		// An endpoint dmcode has never heard of is not claimed to be free.
		{"https://billing.example.com/v1", false},
		{"", false},
	} {
		if got := FreeEndpoint(tc.base); got != tc.free {
			t.Errorf("FreeEndpoint(%q) = %v, want %v", tc.base, got, tc.free)
		}
	}
}

// TestFreeModelCombinesBothAnswers is the row-level truth table: a model is free
// if the endpoint says so or the id says so, and a paid model on a free tier is
// not an option dmcode should offer to someone hunting for one.
func TestFreeModelCombinesBothAnswers(t *testing.T) {
	groq := Provider{BaseURL: "https://api.groq.com/openai/v1"}
	if !FreeModel(groq, "qwen/qwen3-32b") {
		t.Error("a model on Groq's free tier was reported as paid")
	}

	or := Provider{BaseURL: "https://openrouter.ai/api/v1"}
	if !FreeModel(or, "deepseek/deepseek-chat-v3.1:free") {
		t.Error("a model whose id says :free was reported as paid")
	}
	if FreeModel(or, "openai/gpt-4o") {
		t.Error("a paid model on a mixed endpoint was reported as free")
	}
}

// The wizard is the first thing a new user reads and dmcode's claim is that it
// runs for nothing, so the order is a promise: a free option is never ranked
// below one that can bill. Nothing else in the code would notice a provider
// added to the wrong side — /models badges rows and FreeEndpoint answers
// questions, but neither one looks at position — so this is where the rule is
// kept.
//
// "Free" is asked of the option the user would actually get, which is not always
// what the Free flag says. OpenRouter and Kilo carry Free: false because their
// whole catalogue is mixed, yet the model each one defaults to names itself
// :free — a user who takes option 3 pays nothing. Reading the flag alone would
// call them paid and rank them below the line, which is exactly the false
// negative that pushes a free choice out of reach.
//
// An option with no preset endpoint is not ranked at all: the user's own
// endpoint costs whatever their machine's provider charges, and the wizard's
// last row is the escape hatch rather than a recommendation.
func TestNoFreeOptionIsRankedBelowAPaidOne(t *testing.T) {
	opts := SetupOptions()
	billedAt := -1
	for i, o := range opts {
		if o.BaseURL == "" {
			continue // local, prompt-only or the user's own endpoint
		}
		if o.Free || FreeModelID(o.Model) {
			if billedAt >= 0 {
				t.Errorf("%q costs nothing but is ranked at %d, below %q which can bill",
					o.Label, i, opts[billedAt].Label)
			}
			continue
		}
		if billedAt < 0 {
			billedAt = i
		}
	}
}

// The promise has to hold at the top too, or "free first" is only true of the
// middle of the list. The stdin wizard defaults to option 1 on an empty answer,
// so this row is the one a user who presses enter gets.
func TestTheFirstOptionIsFreeAndKeyless(t *testing.T) {
	first := SetupOptions()[0]
	if !first.Keyless {
		t.Errorf("the first option (%q) needs a key", first.Label)
	}
	if !first.Free && !FreeModelID(first.Model) {
		t.Errorf("the first option (%q, model %q) can bill the user", first.Label, first.Model)
	}
}

// TestTheFreeFlagSaysWhatTheLabelSays: /setup already promises "free" in the
// labels it prints. The flag is the same claim in a form code can read, so a
// label that grows a new free provider and forgets the flag would leave the
// picker quietly lying about the one thing it exists to answer.
func TestTheFreeFlagSaysWhatTheLabelSays(t *testing.T) {
	for _, o := range SetupOptions() {
		if o.BaseURL == "" {
			// A local or prompt-only option: free because the wizard named it so.
			if !o.Free && (o.GGUF || o.Keyless) {
				t.Errorf("%q is keyless but not marked free", o.Label)
			}
			continue
		}
		marked := FreeEndpoint(o.BaseURL)
		if marked != o.Free {
			t.Errorf("%q (free=%v) disagrees with FreeEndpoint, which says %v", o.Label, o.Free, marked)
		}
	}
}
