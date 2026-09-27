package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
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
}

// setupOptions is the single list of providers offered by both the interactive
// setup wizard and the TUI's /setup command, so the two can never drift apart.
//
// A keyless option deliberately names no key variable. Writing a placeholder
// into OPENAI_API_KEY would destroy a real key the user already had, and dmcode
// talks to keyless hosts without any key anyway.
func SetupOptions() []SetupOption {
	return []SetupOption{
		{Label: "Без ключа — Pollinations (OpenAI-совместимый, анонимно)", BaseURL: "https://text.pollinations.ai/openai", Model: "openai-fast", API: APIChat, Keyless: true, Reasoning: "low"},
		{Label: "Локально — Ollama (http://127.0.0.1:11434/v1)", BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:7b", API: APIChat, Keyless: true},
		{Label: "Unsloth (локально) — ключ из Settings → API, URL и модель из консоли", Signup: "https://unsloth.ai/docs/basics/api", EnvKey: "OPENAI_API_KEY", API: APIChat},
		{Label: "OpenRouter — бесплатные модели (deepseek и др.)", Signup: "https://openrouter.ai/keys", BaseURL: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-chat-v3.1:free", EnvKey: "OPENAI_API_KEY", API: APIChat},
		{Label: "Kilo — шлюз с бесплатными моделями (kilo-auto/free, ключ аккаунта)", Signup: "https://app.kilo.ai/profile", BaseURL: "https://api.kilo.ai/api/gateway", Model: "kilo-auto/free", EnvKey: "KILO_API_KEY", API: APIChat},
		{Label: "OpenCode Zen — бесплатные модели (nemotron, mimo, big-pickle)", Signup: "https://opencode.ai/auth", BaseURL: "https://opencode.ai/zen/v1", Model: "nemotron-3-ultra-free", EnvKey: "OPENCODE_API_KEY", API: APIResponses},
		{Label: "Groq — бесплатно, быстро, tool calling работает", Signup: "https://console.groq.com/keys", BaseURL: "https://api.groq.com/openai/v1", Model: "qwen/qwen3-32b", EnvKey: "GROQ_API_KEY", API: APIResponses},
		{Label: "Свой OpenAI-совместимый endpoint", EnvKey: "OPENAI_API_KEY", API: APIChat},
	}
}

// setupVars is the .env content a chosen option produces. It is shared by the
// stdin wizard and /setup so the two cannot write different things.
func SetupVars(opt SetupOption, key string) map[string]string {
	vars := map[string]string{"DMCODE_API": opt.API}
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
	fmt.Fprintf(w, "\nБесплатный провайдер не настроен. Выбери:\n")
	for i, o := range opts {
		fmt.Fprintf(w, "  %d) %s\n", i+1, o.Label)
	}
	fmt.Fprint(w, "номер [1]: ")
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("не удалось прочитать выбор (stdin не терминал?): %s", FreeProviderHint)
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
			fmt.Fprintln(w, "Возьми ключ тут:", opt.Signup)
		}
		fmt.Fprint(w, "ключ: ")
		keyLine, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(keyLine) == "" {
			return fmt.Errorf("ключ не введён")
		}
		key = strings.TrimSpace(keyLine)
		if key == "" {
			return fmt.Errorf("ключ не введён")
		}
	}

	// кастомный endpoint: доп. ввод
	if opt.BaseURL == "" {
		fmt.Fprint(w, "base URL (напр. http://localhost:1234/v1): ")
		urlLine, _ := reader.ReadString('\n')
		opt.BaseURL = strings.TrimSpace(urlLine)
		if opt.BaseURL == "" {
			return fmt.Errorf("base URL не введён")
		}
		fmt.Fprint(w, "модель: ")
		mLine, _ := reader.ReadString('\n')
		opt.Model = strings.TrimSpace(mLine)
		if opt.Model == "" {
			return fmt.Errorf("модель не введена")
		}
	}

	vars := SetupVars(opt, key)
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

const FreeProviderHint = "подними локально Ollama (ollama serve) или LM Studio — dmcode подхватит её сам; " +
	"либо выполни /setup в приложении и выбери провайдера"
