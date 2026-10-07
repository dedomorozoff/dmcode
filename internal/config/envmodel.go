package config

import (
	"os"

	"github.com/dedomorozoff/dmcode/internal/settings"
)

// EnvModelKey is the environment variable that names the model to run.
const EnvModelKey = "DMCODE_MODEL"

// SaveModel records the provider configuration in ~/.dmcode/settings.json.
// It takes a Provider and model string, extracting the fields to save.
func SaveModel(p Provider, model string) error {
	return settings.Update(func(s *settings.S) {
		s.BaseURL = p.BaseURL
		s.APIKey = p.APIKey
		s.APIKeyVar = p.APIKeyVar
		s.API = p.API
		s.Model = model
		s.Reasoning = p.Reasoning
	})
}

// SaveSettings persists provider configuration to ~/.dmcode/settings.json.
func SaveSettings(baseURL, apiKeyVar, apiKey, api, model, reasoning, ggufPath, llamaServer, llamaArgs string) error {
	return settings.Update(func(s *settings.S) {
		s.BaseURL = baseURL
		s.APIKey = apiKey
		s.APIKeyVar = apiKeyVar
		s.API = api
		s.Model = model
		s.Reasoning = reasoning
		s.GGUFPath = ggufPath
		s.LlamaServer = llamaServer
		s.LlamaArgs = llamaArgs
	})
}

// LoadSettings applies ~/.dmcode/settings.json onto the environment.
func LoadSettings() {
	s, err := settings.Load()
	if err != nil {
		return
	}
	// Provider configuration: endpoint, key, and API choice.
	if s.BaseURL != "" && os.Getenv("OPENAI_BASE_URL") == "" {
		os.Setenv("OPENAI_BASE_URL", s.BaseURL)
	}
	if s.APIKey != "" && s.APIKeyVar != "" && os.Getenv(s.APIKeyVar) == "" {
		os.Setenv(s.APIKeyVar, s.APIKey)
	}
	if s.API != "" && os.Getenv("DMCODE_API") == "" {
		os.Setenv("DMCODE_API", s.API)
	}
	if s.Reasoning != "" && os.Getenv("DMCODE_REASONING_EFFORT") == "" {
		os.Setenv("DMCODE_REASONING_EFFORT", s.Reasoning)
	}
	// Model preference — global, not per-project.
	if s.Model != "" {
		os.Setenv(EnvModelKey, s.Model)
	}
	// Proxy settings — global, not per-project.
	if s.Proxy != "" && os.Getenv(EnvHTTPProxy) == "" && os.Getenv("http_proxy") == "" {
		os.Setenv(EnvHTTPProxy, s.Proxy)
		os.Setenv(EnvHTTPSProxy, s.Proxy)
		os.Setenv("http_proxy", s.Proxy)
		os.Setenv("https_proxy", s.Proxy)
	}
	if s.NoProxy != "" && os.Getenv(EnvNoProxy) == "" && os.Getenv("no_proxy") == "" {
		os.Setenv(EnvNoProxy, s.NoProxy)
		os.Setenv("no_proxy", s.NoProxy)
	}
	// GGUF path — global preference for local models.
	if s.GGUFPath != "" && os.Getenv("DMCODE_GGUF") == "" {
		os.Setenv("DMCODE_GGUF", s.GGUFPath)
	}
	if s.LlamaServer != "" && os.Getenv("DMCODE_LLAMA_SERVER") == "" {
		os.Setenv("DMCODE_LLAMA_SERVER", s.LlamaServer)
	}
	if s.LlamaArgs != "" && os.Getenv("DMCODE_LLAMA_ARGS") == "" {
		os.Setenv("DMCODE_LLAMA_ARGS", s.LlamaArgs)
	}
}
