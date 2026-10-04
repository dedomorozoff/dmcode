package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

type SetupOption struct {
	Label   string
	Signup  string
	BaseURL string
	Model   string
	EnvKey  string
	API     string // apiResponses or apiChat
	// keyless marks options that need no API key at all.
	Keyless bool
	// reasoning is written as DMCODE_REASONING_EFFORT when the endpoint needs a
	// capped reasoning channel (see provider.reasoning).
	Reasoning string
	// GGUF marks the option that runs a local .gguf file through llama.cpp's
	// llama-server. There is no endpoint until the file is loaded, so the path
	// is collected by the wizard (both of them) into GGUFPath, and dmcode
	// starts the server itself.
	GGUF     bool
	GGUFPath string
}

// setupOptions is the single list of providers offered by both the interactive
// setup wizard and the TUI's /setup command, so the two can never drift apart.
//
// A keyless option deliberately names no key variable. Writing a placeholder
// into OPENAI_API_KEY would destroy a real key the user already had, and dmcode
// talks to keyless hosts without any key anyway.
func SetupOptions() []SetupOption {
	return []SetupOption{
		{Label: i18n.T("No key — Pollinations (OpenAI-compatible, anonymous)"), BaseURL: "https://text.pollinations.ai/openai", Model: "openai-fast", API: APIChat, Keyless: true, Reasoning: "low"},
		{Label: i18n.T("Local — Ollama (http://127.0.0.1:11434/v1)"), BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:7b", API: APIChat, Keyless: true},
		{Label: i18n.T("Unsloth (local) — key from Settings → API, URL and model from your console"), Signup: "https://unsloth.ai/docs/basics/api", EnvKey: "OPENAI_API_KEY", API: APIChat},
		{Label: i18n.T("Local GGUF — llama.cpp runs a .gguf file (llama-server)"), Keyless: true, API: APIChat, GGUF: true},
		{Label: i18n.T("OpenRouter — free models (deepseek and others)"), Signup: "https://openrouter.ai/keys", BaseURL: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-chat-v3.1:free", EnvKey: "OPENAI_API_KEY", API: APIChat},
		{Label: i18n.T("Kilo — gateway with free models (kilo-auto/free, account key)"), Signup: "https://app.kilo.ai/profile", BaseURL: "https://api.kilo.ai/api/gateway", Model: "kilo-auto/free", EnvKey: "KILO_API_KEY", API: APIChat},
		{Label: i18n.T("OpenCode Zen — free models (nemotron, mimo, big-pickle)"), Signup: "https://opencode.ai/auth", BaseURL: "https://opencode.ai/zen/v1", Model: "big-pickle", EnvKey: "OPENCODE_API_KEY", API: APIResponses},
		{Label: i18n.T("Cline — gateway to Anthropic, OpenAI and Google models"), Signup: "https://app.cline.bot/settings/api-keys", BaseURL: "https://api.cline.bot/api/v1", Model: "anthropic/claude-sonnet-4-6", EnvKey: "CLINE_API_KEY", API: APIChat},
		{Label: i18n.T("Groq — free, fast, tool calling works"), Signup: "https://console.groq.com/keys", BaseURL: "https://api.groq.com/openai/v1", Model: "qwen/qwen3-32b", EnvKey: "GROQ_API_KEY", API: APIResponses},
		{Label: i18n.T("GitHub Models — free with a GitHub token"), Signup: "https://github.com/settings/tokens", BaseURL: "https://models.github.ai/inference", Model: "openai/gpt-4.1-mini", EnvKey: "GITHUB_TOKEN", API: APIResponses},
		{Label: i18n.T("Mistral — codestral, paid tier has a free slice"), Signup: "https://console.mistral.ai/api-keys", BaseURL: "https://api.mistral.ai/v1", Model: "codestral-latest", EnvKey: "MISTRAL_API_KEY", API: APIResponses},
		{Label: i18n.T("Cerebras — free tier, no card, very fast"), Signup: "https://inference.cerebras.ai", BaseURL: "https://api.cerebras.ai/v1", Model: "qwen-3.8-27b", EnvKey: "CEREBRAS_API_KEY", API: APIChat},
		{Label: i18n.T("NVIDIA NIM — free credits, many coding models"), Signup: "https://build.nvidia.com", BaseURL: "https://integrate.api.nvidia.com/v1", Model: "z-ai/glm-5.3", EnvKey: "NVIDIA_API_KEY", API: APIChat},
		{Label: i18n.T("SambaNova — free key, fast OpenAI-compatible"), Signup: "https://cloud.sambanova.ai", BaseURL: "https://api.sambanova.ai/v1", Model: "Meta-Llama-3.3-70B-Instruct", EnvKey: "SAMBANOVA_API_KEY", API: APIChat},
		{Label: i18n.T("Hugging Face — free credits, OpenAI-compatible router"), Signup: "https://huggingface.co/settings/tokens", BaseURL: "https://router.huggingface.co/v1", Model: "Qwen/Qwen3-Coder-30B-A3B-Instruct", EnvKey: "HF_TOKEN", API: APIChat},
		{Label: i18n.T("Your own OpenAI-compatible endpoint"), EnvKey: "OPENAI_API_KEY", API: APIChat},
	}
}

// SetupVars is the .env content a chosen option produces. It is shared by the
// stdin wizard and /setup so the two cannot write different things.
//
// The GGUF option persists only what is already known: the path arrives from
// the wizard's prompt and lands in the map through opt.GGUFPath. Everything
// else — the port, the model id — is decided when the server actually starts.
func SetupVars(opt SetupOption, key string) map[string]string {
	vars := map[string]string{"DMCODE_API": opt.API}
	if opt.GGUF {
		if opt.GGUFPath != "" {
			vars["DMCODE_GGUF"] = opt.GGUFPath
		}
		return vars
	}
	if opt.BaseURL != "" {
		vars["OPENAI_BASE_URL"] = opt.BaseURL
		vars["DMCODE_MODEL"] = opt.Model
	}
	if opt.Reasoning != "" {
		vars["DMCODE_REASONING_EFFORT"] = opt.Reasoning
	}
	if !opt.Keyless && opt.EnvKey != "" {
		vars[opt.EnvKey] = key
	}
	return vars
}

func SetupWizard() error {
	return SetupWizardWith(os.Stdin, os.Stdout)
}

// setupWizardWith is the wizard with its input and output injected, so the
// prompt sequence can be tested without a terminal. The validation below is the
// only guard against writing a half-filled custom endpoint, so it has to be
// reachable from a test.
func SetupWizardWith(r io.Reader, w io.Writer) error {
	opts := SetupOptions()
	fmt.Fprintf(w, "%s", i18n.T("\nNo free provider is configured. Pick one:\n"))
	for i, o := range opts {
		fmt.Fprintf(w, "  %d) %s\n", i+1, o.Label)
	}
	fmt.Fprint(w, i18n.T("number [1]: "))
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("%s: %s", i18n.T("could not read the choice (stdin not a terminal?)"), FreeProviderHint())
	}
	line = strings.TrimSpace(line)
	idx := 1
	if line != "" {
		if _, err := fmt.Sscanf(line, "%d", &idx); err != nil || idx < 1 || idx > len(opts) {
			idx = 1
		}
	}
	opt := opts[idx-1]

	key := ""
	if !opt.Keyless {
		if opt.Signup != "" {
			fmt.Fprintln(w, i18n.T("Get a key here:"), opt.Signup)
		}
		fmt.Fprint(w, i18n.T("key: "))
		keyLine, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(keyLine) == "" {
			return fmt.Errorf("%s", i18n.T("no key entered"))
		}
		key = strings.TrimSpace(keyLine)
		if key == "" {
			return fmt.Errorf("%s", i18n.T("no key entered"))
		}
	}

	// A GGUF file needs a path, not a key: llama-server runs it locally and
	// needs no credentials. The binary question has an enter-only default so a
	// user with llama-server on PATH can skip it.
	// extras carries what the prompts below collect that SetupVars cannot know.
	extras := map[string]string{}
	if opt.GGUF {
		fmt.Fprint(w, i18n.T("path to the .gguf file: "))
		pathLine, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(pathLine) == "" {
			return fmt.Errorf("%s", i18n.T("no path to the .gguf file entered"))
		}
		opt.GGUFPath = strings.TrimSpace(pathLine)
		if opt.GGUFPath == "" {
			return fmt.Errorf("%s", i18n.T("no path to the .gguf file entered"))
		}
		fmt.Fprint(w, i18n.T("llama-server binary (enter = llama-server on PATH): "))
		binLine, _ := reader.ReadString('\n')
		bin := strings.TrimSpace(binLine)
		if bin != "" {
			extras["DMCODE_LLAMA_SERVER"] = bin
		}
	} else if opt.BaseURL == "" {
		fmt.Fprint(w, i18n.T("base URL (e.g. http://localhost:1234/v1): "))
		urlLine, _ := reader.ReadString('\n')
		opt.BaseURL = strings.TrimSpace(urlLine)
		if opt.BaseURL == "" {
			return fmt.Errorf("%s", i18n.T("no base URL entered"))
		}
		fmt.Fprint(w, i18n.T("model: "))
		mLine, _ := reader.ReadString('\n')
		opt.Model = strings.TrimSpace(mLine)
		if opt.Model == "" {
			return fmt.Errorf("%s", i18n.T("no model entered"))
		}
	}

	vars := SetupVars(opt, key)
	for k, v := range extras {
		vars[k] = v
	}
	// Merge rather than truncate: picking a free provider must not delete a real
	// key the user already had for a paid one.
	existing, err := ReadDotEnv()
	if err != nil {
		return err
	}
	merged, _ := MergeDotEnv(existing, vars)

	var sb strings.Builder
	for _, l := range merged {
		sb.WriteString(l + "\n")
	}
	for _, k := range SortedKeys(vars) {
		if err := os.Setenv(k, vars[k]); err != nil {
			return err
		}
	}
	return os.WriteFile(".env", []byte(sb.String()), 0o600)
}

// FreeProviderHint is the advice shown when no endpoint is reachable. It is a
// function, not a const, because it is translated at call time.
func FreeProviderHint() string {
	return i18n.T("run Ollama locally (ollama serve) or LM Studio — dmcode picks it up on its own; " +
		"or run /setup inside the app and choose a provider")
}
