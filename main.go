// dmcode — coding agent built on google/adk-go.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/tool"
)

func detectProvider() (provider, error) {
	baseURL := os.Getenv("OPENAI_BASE_URL")
	apiKey := os.Getenv("OPENAI_API_KEY")
	modelName := os.Getenv("DMCODE_MODEL")

	// An explicit endpoint always wins: the user configured it on purpose. The
	// key may legitimately be absent — keyless hosts and a local Ollama need
	// none, and requiring one would push those users to write a dummy secret
	// into .env. DMCODE_API=chat forces the /chat/completions wire for
	// endpoints that speak both, or that only speak chat.
	if baseURL != "" {
		return provider{
			baseURL: baseURL,
			apiKey:  apiKey,
			model:   orDefaultModel(modelName),
			api:     envAPI(),
			label:   "OpenAI-совместимый",
		}, nil
	}

	type preset struct {
		env   string
		url   string
		model string
		api   string
		label string
	}
	for _, p := range []preset{
		{"OPENCODE_API_KEY", "https://opencode.ai/zen/v1", "nemotron-3-ultra-free", apiResponses, "OpenCode Zen"},
		{"GROQ_API_KEY", "https://api.groq.com/openai/v1", "qwen/qwen3-32b", apiResponses, "Groq"},
		{"GITHUB_TOKEN", "https://models.github.ai/inference", "openai/gpt-4.1-mini", apiResponses, "GitHub Models"},
		{"MISTRAL_API_KEY", "https://api.mistral.ai/v1", "codestral-latest", apiResponses, "Mistral"},
		{"OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", "deepseek/deepseek-chat-v3.1:free", apiChat, "OpenRouter"},
	} {
		if key := os.Getenv(p.env); key != "" {
			return provider{
				baseURL: p.url,
				apiKey:  key,
				model:   orDefaultModel(firstNonEmpty(modelName, p.model)),
				api:     p.api,
				label:   p.label,
			}, nil
		}
	}

	// Nothing configured: look for something free that already works, so a
	// fresh checkout is usable with no setup at all.
	if p, ok := discoverFreeProvider(); ok {
		return p, nil
	}

	if err := setupWizard(); err != nil {
		return provider{}, err
	}
	baseURL = os.Getenv("OPENAI_BASE_URL")
	apiKey = os.Getenv("OPENAI_API_KEY")
	if baseURL == "" || apiKey == "" {
		return provider{}, fmt.Errorf("провайдер не настроен — %s", freeProviderHint)
	}
	return provider{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   orDefaultModel(os.Getenv("DMCODE_MODEL")),
		api:     envAPI(),
		label:   "OpenAI-совместимый",
	}, nil
}

func envAPI() string {
	if strings.EqualFold(os.Getenv("DMCODE_API"), apiChat) {
		return apiChat
	}
	return apiResponses
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func orDefaultModel(m string) string {
	if m != "" {
		return m
	}
	return "qwen2.5-coder:7b"
}

type setupOption struct {
	label   string
	signup  string
	baseURL string
	model   string
	envKey  string
	api     string // apiResponses or apiChat
	// keyless marks options that need no API key at all.
	keyless bool
}

// setupOptions is the single list of providers offered by both the interactive
// setup wizard and the TUI's /setup command, so the two can never drift apart.
//
// A keyless option deliberately names no key variable. Writing a placeholder
// into OPENAI_API_KEY would destroy a real key the user already had, and dmcode
// talks to keyless hosts without any key anyway.
func setupOptions() []setupOption {
	return []setupOption{
		{label: "Без ключа — Pollinations (OpenAI-совместимый, анонимно)", baseURL: "https://text.pollinations.ai/openai", model: "openai-fast", api: apiChat, keyless: true},
		{label: "Локально — Ollama (http://127.0.0.1:11434/v1)", baseURL: "http://127.0.0.1:11434/v1", model: "qwen2.5-coder:7b", api: apiChat, keyless: true},
		{label: "OpenRouter — бесплатные модели (deepseek и др.)", signup: "https://openrouter.ai/keys", baseURL: "https://openrouter.ai/api/v1", model: "deepseek/deepseek-chat-v3.1:free", envKey: "OPENAI_API_KEY", api: apiChat},
		{label: "OpenCode Zen — бесплатные модели (nemotron, mimo, big-pickle)", signup: "https://opencode.ai/auth", baseURL: "https://opencode.ai/zen/v1", model: "nemotron-3-ultra-free", envKey: "OPENCODE_API_KEY", api: apiResponses},
		{label: "Groq — бесплатно, быстро, tool calling работает", signup: "https://console.groq.com/keys", baseURL: "https://api.groq.com/openai/v1", model: "qwen/qwen3-32b", envKey: "GROQ_API_KEY", api: apiResponses},
		{label: "Свой OpenAI-совместимый endpoint", envKey: "OPENAI_API_KEY", api: apiChat},
	}
}

// setupVars is the .env content a chosen option produces. It is shared by the
// stdin wizard and /setup so the two cannot write different things.
func setupVars(opt setupOption, key string) map[string]string {
	vars := map[string]string{"DMCODE_API": opt.api}
	if opt.baseURL != "" {
		vars["OPENAI_BASE_URL"] = opt.baseURL
		vars["DMCODE_MODEL"] = opt.model
	}
	if !opt.keyless && opt.envKey != "" {
		vars[opt.envKey] = key
	}
	return vars
}

func setupWizard() error {
	return setupWizardWith(os.Stdin, os.Stdout)
}

// setupWizardWith is the wizard with its input and output injected, so the
// prompt sequence can be tested without a terminal. The validation below is the
// only guard against writing a half-filled custom endpoint, so it has to be
// reachable from a test.
func setupWizardWith(r io.Reader, w io.Writer) error {
	opts := setupOptions()
	fmt.Fprintf(w, "\nБесплатный провайдер не настроен. Выбери:\n")
	for i, o := range opts {
		fmt.Fprintf(w, "  %d) %s\n", i+1, o.label)
	}
	fmt.Fprint(w, "номер [1]: ")
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("не удалось прочитать выбор (stdin не терминал?): %s", freeProviderHint)
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
	if !opt.keyless {
		if opt.signup != "" {
			fmt.Fprintln(w, "Возьми ключ тут:", opt.signup)
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
	if opt.baseURL == "" {
		fmt.Fprint(w, "base URL (напр. http://localhost:1234/v1): ")
		urlLine, _ := reader.ReadString('\n')
		opt.baseURL = strings.TrimSpace(urlLine)
		if opt.baseURL == "" {
			return fmt.Errorf("base URL не введён")
		}
		fmt.Fprint(w, "модель: ")
		mLine, _ := reader.ReadString('\n')
		opt.model = strings.TrimSpace(mLine)
		if opt.model == "" {
			return fmt.Errorf("модель не введена")
		}
	}

	vars := setupVars(opt, key)
	// Merge rather than truncate: picking a free provider must not delete a real
	// key the user already had for a paid one.
	existing, err := readDotEnv()
	if err != nil {
		return err
	}
	merged, _ := mergeDotEnv(existing, vars)

	var sb strings.Builder
	for _, l := range merged {
		sb.WriteString(l + "\n")
	}
	for _, k := range sortedKeys(vars) {
		if err := os.Setenv(k, vars[k]); err != nil {
			return err
		}
	}
	return os.WriteFile(".env", []byte(sb.String()), 0o600)
}

const instruction = `You are dmcode, an autonomous terminal-based AI coding assistant working directly on the user's filesystem.

Your primary mission: analyze requests, inspect the codebase, implement robust changes, and verify your work.

Workflow:
1. EXPLORE: Use list_dir and glob to discover the project structure. Do not guess filenames or directory layouts.
2. RESEARCH: Use grep to search for patterns and read_file to inspect code before attempting modifications. Always understand the surrounding context.
3. MODIFY:
   - Use edit_file for targeted, surgical changes in existing files (provide enough unique context in old_string).
   - Use write_file only when creating brand new files or completely rewriting small configs.
4. VERIFY: Always run relevant build and test commands via run_command (e.g. go test ./..., npm test, pytest, cargo test) to ensure changes compile and pass tests.
5. RECOVER: If a command or build fails, read the output and compiler errors, analyze root causes, and fix them before concluding the turn.
6. REPORT: Provide a concise, clear summary of what files were changed and how they were verified.

Safety & Coding Guidelines:
- Never assume file contents without reading them first.
- Match existing project code conventions, indentations, and naming styles.
- Be careful with path separators and line endings on Windows/Unix.
- Keep edits minimal and focused on the user's explicit request. Do not introduce unnecessary refactoring or style drift.
- Never delete or modify files outside the workspace unless explicitly instructed.`

// api selects which OpenAI-compatible wire protocol the endpoint speaks.
const (
	apiResponses = "responses" // /v1/responses — OpenAI, OpenCode Zen, GitHub Models
	apiChat      = "chat"      // /chat/completions — Ollama, LM Studio, Pollinations
)

type provider struct {
	baseURL string
	apiKey  string
	model   string
	api     string // apiResponses (default) or apiChat
	label   string // human-readable provider name for the UI
}

func (p provider) wire() string {
	if p.api == "" {
		return apiResponses
	}
	return p.api
}

func buildAgent(ctx context.Context, p provider, tools []tool.Tool) (agent.Agent, error) {
	var m model.LLM
	if p.wire() == apiChat {
		m = newChatModel(p.baseURL, p.apiKey, p.model)
	} else {
		om, err := openaimodel.NewModel(ctx, p.model, &openaimodel.ClientConfig{
			APIKey:  p.apiKey,
			BaseURL: p.baseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create openai-compatible model: %w", err)
		}
		m = om
	}
	return llmagent.New(llmagent.Config{
		Name:        "dmcode",
		Model:       m,
		Description: "Autonomous coding agent that reads, writes, and builds code.",
		Instruction: instruction,
		Tools:       tools,
	})
}

func listModels(p provider) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(p.baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(b), 160))
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
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func main() {
	loadDotEnv()
	ctx := context.Background()

	p, err := detectProvider()
	if err != nil {
		log.Fatal(err)
	}

	tools, err := makeTools()
	if err != nil {
		log.Fatal(err)
	}

	var toolNames []string
	for _, t := range tools {
		toolNames = append(toolNames, t.Name())
	}
	if err := runTUI(ctx, p, tools, toolNames); err != nil {
		log.Fatal(err)
	}
}
