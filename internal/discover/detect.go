// Package discover finds the endpoints dmcode can run on: the explicitly
// configured one, keyless free hosts, and local inference servers.
package discover

import (
	"fmt"
	"os"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// detectProvider is the single-endpoint view of detectProviders, kept for
// callers that only need the one it will use first.
func DetectProvider() (config.Provider, error) {
	pool, err := DetectProviders()
	if err != nil {
		return config.Provider{}, err
	}
	return pool[0], nil
}

// keyedPreset ties an environment variable to the endpoint it selects, so a
// provider can be found with no configuration at all.
//
// The list is a named function rather than an inline literal because
// TestSetupOptionsCoverTheKeyedPresets compares it against config.SetupOptions:
// a second, hand-written copy of these endpoints would be free to drift, and the
// drift would only show up as "the wizard set up a different model than the one
// I get automatically", with nothing in the UI to explain it.
type keyedPreset struct {
	env   string
	url   string
	Model string
	API   string
	Label string
}

func keyedPresets() []keyedPreset {
	return []keyedPreset{
		{"OPENCODE_API_KEY", "https://opencode.ai/zen/v1", "nemotron-3-ultra-free", config.APIResponses, "OpenCode Zen"},
		{"GROQ_API_KEY", "https://api.groq.com/openai/v1", "qwen/qwen3-32b", config.APIResponses, "Groq"},
		{"GITHUB_TOKEN", "https://models.github.ai/inference", "openai/gpt-4.1-mini", config.APIResponses, "GitHub Models"},
		{"MISTRAL_API_KEY", "https://api.mistral.ai/v1", "codestral-latest", config.APIResponses, "Mistral"},
		{"OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", "deepseek/deepseek-chat-v3.1:free", config.APIChat, "OpenRouter"},
		{"KILO_API_KEY", "https://api.kilo.ai/api/gateway", "kilo-auto/free", config.APIChat, "Kilo"},
		// The four below issue a working key on signup with no card attached, and
		// each was probed live before being listed: /v1/models answers, so a key
		// from the signup page is enough to reach the agent.
		{"CEREBRAS_API_KEY", "https://api.cerebras.ai/v1", "qwen-3.8-27b", config.APIChat, "Cerebras"},
		{"NVIDIA_API_KEY", "https://integrate.api.nvidia.com/v1", "z-ai/glm-5.3", config.APIChat, "NVIDIA NIM"},
		{"SAMBANOVA_API_KEY", "https://api.sambanova.ai/v1", "Meta-Llama-3.3-70B-Instruct", config.APIChat, "SambaNova"},
		{"HF_TOKEN", "https://router.huggingface.co/v1", "Qwen/Qwen3-Coder-30B-A3B-Instruct", config.APIChat, "Hugging Face"},
	}
}

// detectProviders returns the session's pool in preference order.
//
// A configured endpoint is always first and always alone: the user chose it, and
// promoting a free local server over a paid key they set up would be a
// surprise. When nothing is configured, every working free endpoint is returned
// rather than the first hit, so a host that dies mid-session has somewhere to
// go without a fresh probe.
func DetectProviders() ([]config.Provider, error) {
	BaseURL := os.Getenv("OPENAI_BASE_URL")
	APIKey := os.Getenv("OPENAI_API_KEY")
	modelName := os.Getenv("DMCODE_MODEL")

	// An explicit endpoint always wins: the user configured it on purpose. The
	// key may legitimately be absent — keyless hosts and a local Ollama need
	// none, and requiring one would push those users to write a dummy secret
	// into .env. DMCODE_API=chat forces the /chat/completions wire for
	// endpoints that speak both, or that only speak chat.
	if BaseURL != "" {
		return []config.Provider{{
			BaseURL: BaseURL,
			APIKey:  APIKey,
			Model:   config.OrDefaultModel(modelName),
			API:     config.EnvAPI(),
			Label:   i18n.T("OpenAI-compatible"),
		}}, nil
	}

	for _, p := range keyedPresets() {
		if key := os.Getenv(p.env); key != "" {
			return []config.Provider{{
				BaseURL: p.url,
				APIKey:  key,
				Model:   config.OrDefaultModel(config.FirstNonEmpty(modelName, p.Model)),
				API:     p.API,
				Label:   p.Label,
			}}, nil
		}
	}

	// Nothing configured: look for something free that already works, so a
	// fresh checkout is usable with no setup at all.
	if pool := DiscoverFreeProviders(); len(pool) > 0 {
		return pool, nil
	}

	if err := config.SetupWizard(); err != nil {
		return nil, err
	}
	BaseURL = os.Getenv("OPENAI_BASE_URL")
	APIKey = os.Getenv("OPENAI_API_KEY")
	if BaseURL == "" || APIKey == "" {
		return nil, fmt.Errorf("no provider configured — %s", config.FreeProviderHint())
	}
	return []config.Provider{{
		BaseURL:   BaseURL,
		APIKey:    APIKey,
		Model:     config.OrDefaultModel(os.Getenv("DMCODE_MODEL")),
		API:       config.EnvAPI(),
		Label:     i18n.T("OpenAI-compatible"),
		Reasoning: os.Getenv("DMCODE_REASONING_EFFORT"),
	}}, nil
}
