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
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/tool"
)

func detectProvider() (provider, error) {
	baseURL := os.Getenv("OPENAI_BASE_URL")
	apiKey := os.Getenv("OPENAI_API_KEY")
	modelName := os.Getenv("DMCODE_MODEL")

	type preset struct {
		env   string
		url   string
		model string
	}
	for _, p := range []preset{
		{"OPENCODE_API_KEY", "https://opencode.ai/zen/v1", "nemotron-3-ultra-free"},
		{"GROQ_API_KEY", "https://api.groq.com/openai/v1", "qwen/qwen3-32b"},
		{"GITHUB_TOKEN", "https://models.github.ai/inference", "openai/gpt-4.1-mini"},
		{"MISTRAL_API_KEY", "https://api.mistral.ai/v1", "codestral-latest"},
	} {
		if baseURL == "" && apiKey == "" {
			if key := os.Getenv(p.env); key != "" {
				apiKey, baseURL = key, p.url
				if modelName == "" {
					modelName = p.model
				}
			}
		}
	}
	if baseURL == "" && apiKey == "" {
		if err := setupWizard(); err != nil {
			return provider{}, err
		}
		baseURL = os.Getenv("OPENAI_BASE_URL")
		apiKey = os.Getenv("OPENAI_API_KEY")
		modelName = os.Getenv("DMCODE_MODEL")
		if baseURL == "" || apiKey == "" {
			return provider{}, fmt.Errorf("провайдер не настроен — запусти ещё раз и введи ключ")
		}
	}
	if modelName == "" {
		modelName = "qwen2.5-coder:7b"
	}
	return provider{baseURL: baseURL, apiKey: apiKey, model: modelName}, nil
}

type setupOption struct {
	label   string
	signup  string
	baseURL string
	model   string
	envKey  string
}

func setupWizard() error {
	opts := []setupOption{
		{"OpenCode Zen — бесплатные модели (nemotron, mimo, big-pickle)", "https://opencode.ai/auth", "https://opencode.ai/zen/v1", "nemotron-3-ultra-free", "OPENCODE_API_KEY"},
		{"Groq — бесплатно, быстрый, tool calling работает", "https://console.groq.com/keys", "https://api.groq.com/openai/v1", "qwen/qwen3-32b", "GROQ_API_KEY"},
		{"OpenRouter — бесплатные модели (deepseek и др.)", "https://openrouter.ai/keys", "https://openrouter.ai/api/v1", "deepseek/deepseek-chat-v3.1:free", "OPENAI_API_KEY"},
		{"Mistral — Codestral, есть free tier", "https://console.mistral.ai/api-keys", "https://api.mistral.ai/v1", "codestral-latest", "MISTRAL_API_KEY"},
		{"Свой OpenAI-совместимый endpoint", "", "", "", "OPENAI_API_KEY"},
	}
	fmt.Println("\nБесплатный провайдер не настроен. Выбери:")
	for i, o := range opts {
		fmt.Printf("  %d) %s\n", i+1, o.label)
	}
	fmt.Print("номер [1]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("не удалось прочитать выбор (stdin не терминал?)")
	}
	line = strings.TrimSpace(line)
	idx := 1
	if line != "" {
		if _, err := fmt.Sscanf(line, "%d", &idx); err != nil || idx < 1 || idx > len(opts) {
			idx = 1
		}
	}
	opt := opts[idx-1]
	if opt.signup != "" {
		fmt.Println("Возьми ключ тут:", opt.signup)
	}
	fmt.Print("ключ: ")
	keyLine, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(keyLine) == "" {
		return fmt.Errorf("ключ не введён")
	}
	key := strings.TrimSpace(keyLine)
	if key == "" {
		return fmt.Errorf("ключ не введён")
	}

	vars := map[string]string{opt.envKey: key}
	if opt.baseURL != "" {
		vars["OPENAI_BASE_URL"] = opt.baseURL
		vars["DMCODE_MODEL"] = opt.model
	}
	if opt.envKey == "OPENAI_API_KEY" {
		vars["OPENAI_API_KEY"] = key
	}
	if opt.envKey == "OPENCODE_API_KEY" {
		vars["OPENCODE_API_KEY"] = key
	}

	// кастомный endpoint: доп. ввод
	if opt.baseURL == "" && idx == len(opts) {
		fmt.Print("base URL (напр. http://localhost:1234/v1): ")
		urlLine, _ := reader.ReadString('\n')
		if u := strings.TrimSpace(urlLine); u != "" {
			vars["OPENAI_BASE_URL"] = u
		}
		fmt.Print("модель: ")
		mLine, _ := reader.ReadString('\n')
		if m := strings.TrimSpace(mLine); m != "" {
			vars["DMCODE_MODEL"] = m
		}
	}

	var sb strings.Builder
	for k, v := range vars {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
		sb.WriteString(k + "=" + v + "\n")
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

type provider struct {
	baseURL string
	apiKey  string
	model   string
}

func buildAgent(ctx context.Context, p provider, tools []tool.Tool) (agent.Agent, error) {
	m, err := openaimodel.NewModel(ctx, p.model, &openaimodel.ClientConfig{
		APIKey:  p.apiKey,
		BaseURL: p.baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openai-compatible model: %w", err)
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
