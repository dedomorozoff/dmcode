// Package config holds the provider configuration: the endpoint description,
// the .env file, and the setup options shared by the stdin wizard and /setup.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// api selects which OpenAI-compatible wire protocol the endpoint speaks.
const (
	APIResponses = "responses" // /v1/responses — OpenAI, OpenCode Zen, GitHub Models
	APIChat      = "chat"      // /chat/completions — Ollama, LM Studio, Pollinations
)

type Provider struct {
	BaseURL   string
	APIKey    string
	APIKeyVar string // environment variable name for API key (e.g., OPENAI_API_KEY)
	Model     string
	API       string // apiResponses (default) or apiChat
	Label     string // human-readable provider name for the UI
	// Context is the model's context window in tokens as the endpoint itself
	// reported it (/v1/models), zero when the endpoint said nothing. It wins
	// over the heuristic table because it is the server's own answer, which
	// knows its actual cap (an Ollama num_ctx, a vLLM limit, a proxy ceiling)
	// where a name-to-tokens guess can only be right some of the time.
	Context int
	// reasoning is the OpenAI `reasoning_effort` to request on the chat wire.
	// Empty means the field is not sent. Reasoning models spend the output
	// budget on their analysis channel first, which on endpoints with a modest
	// cap truncates long tool arguments mid-JSON; "low" prevents that.
	Reasoning string
}

func (p Provider) Wire() string {
	if p.API == "" {
		return APIResponses
	}
	return p.API
}

func EnvAPI() string {
	if strings.EqualFold(os.Getenv("DMCODE_API"), APIChat) {
		return APIChat
	}
	return APIResponses
}

func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func OrDefaultModel(m string) string {
	if m != "" {
		return m
	}
	return "qwen2.5-coder:7b"
}

// SessionsDir is where conversations are kept between runs, defaulting to
// ~/.dmcode/sessions. DMCODE_SESSIONS_DIR overrides it, which is what the
// tests use and what a user sets to keep sessions beside a project instead of
// in their home directory.
//
// An unresolvable home is reported rather than fatal: the session store falls
// back to memory, and losing history on exit is better than not starting.
func SessionsDir() string {
	if p := os.Getenv("DMCODE_SESSIONS_DIR"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".dmcode", "sessions")
}

// Version is the dmcode build version, stamped by main from the -ldflags
// value and read by the sidebar's brand block. A plain "go build" leaves it
// at "dev", which is the honest answer for a binary nobody tagged.
var Version = "dev"

// ContextWindow is how many prompt tokens the model will accept, which is the
// number a compaction threshold and a usage bar are both fractions of.
//
// It is an estimate, and is treated as one everywhere it is used: no endpoint
// dmcode talks to is asked. DMCODE_CONTEXT states the real figure for a model
// the heuristic gets wrong, which is why an override beats the table rather
// than the other way round. Zero means the model is not recognised, and a
// caller must then show the token count without a percentage rather than
// divide by a guess.
func ContextWindow(model string) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DMCODE_CONTEXT"))); err == nil && v > 0 {
		return v
	}
	name := strings.ToLower(model)
	if name == "" {
		return 0
	}
	// Longest name first: "gpt-4o-mini" has to reach the 4o rule rather than
	// stopping at a shorter prefix that happens to match another family.
	for _, r := range []struct {
		needle string
		tokens int
	}{
		{"claude", 200_000},
		{"gpt-5", 400_000},
		{"gpt-4.1", 1_000_000},
		{"gpt-oss", 128_000},
		{"gpt-4o", 128_000},
		{"gpt-4", 128_000},
		{"gpt-3.5", 16_385},
		{"o1", 200_000},
		{"o3", 200_000},
		{"deepseek", 65_536},
		{"qwen", 32_768},
		{"llama", 32_768},
		{"mistral", 32_768},
		{"gemma", 8_192},
		{"phi", 16_384},
	} {
		if strings.Contains(name, r.needle) {
			return r.tokens
		}
	}
	return 0
}

// DefaultContextWindow is the window assumed for a model this build does not
// recognise, used for display only.
//
// It is not used to decide when to compact. Guessing a compaction threshold is
// a decision with consequences — a wrong one either compacts a conversation
// that was never near full, or fails to compact one that is — while guessing a
// number for the meter only has to be visibly a guess. Showing a denominator
// marked "~" answers "out of how much?" far better than showing a bare count
// with no denominator at all, which is what an unknown window used to produce.
//
// 128k is the middle of the range real models sit in: high enough that the
// meter does not read as nearly full on a short conversation, low enough that
// a small local model does not look like it has room to spare.
const DefaultContextWindow = 128_000

// ContextWindowForDisplay is the denominator the meter divides by: the real
// figure when it is known, and DefaultContextWindow marked as the guess it is
// when it is not.
//
// The two answers are deliberately different types. A caller that decides
// something — when to compact, when to warn — wants the exact value and must
// check it is non-zero. A caller that draws a number wants a denominator, and
// is handed a flag telling it to mark the guess.
func ContextWindowForDisplay(model string) (window int, approximate bool) {
	if w := ContextWindow(model); w > 0 {
		return w, false
	}
	return DefaultContextWindow, true
}

// WindowFor is the context window for a provider: the size the endpoint itself
// reported in /v1/models when it reported one, and the heuristic table's guess
// for the model name otherwise. The endpoint's figure wins because it is the
// answer to the question the meter and the compaction threshold actually ask —
// "how much will this host accept" — which no name-to-tokens guess can know
// (an Ollama num_ctx and a vLLM limit are set by the operator, not the model).
//
// Zero means neither source knows, and a caller must then show a bare count
// rather than divide by a guess.
func WindowFor(p Provider) int {
	if p.Context > 0 {
		return p.Context
	}
	return ContextWindow(p.Model)
}

// WindowForDisplay is the denominator the meter divides by, provider-aware:
// the endpoint's own number when it gave one, the table's when it did not, and
// DefaultContextWindow marked as the guess it is when even the table has no
// answer.
func WindowForDisplay(p Provider) (window int, approximate bool) {
	if p.Context > 0 {
		return p.Context, false
	}
	return ContextWindowForDisplay(p.Model)
}

// AskTimeout is how long a question from the agent waits for an answer before
// the recommended option is chosen for the user. DMCODE_ASK_TIMEOUT sets it in
// seconds.
//
// Zero means off, which is the default: an agent that picks for a user who is
// still reading is worse than one that waits, so the timer is something a user
// turns on for themselves rather than something that happens to them.
func AskTimeout() time.Duration {
	v, err := strconv.Atoi(os.Getenv("DMCODE_ASK_TIMEOUT"))
	if err != nil || v <= 0 {
		return 0
	}
	return time.Duration(v) * time.Second
}

func ListModels(p Provider) ([]string, error) {
	served, err := fetchServedModels(p.BaseURL, p.APIKey, 15*time.Second)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(served))
	for _, m := range served {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

// ModelContext asks the endpoint how many prompt tokens it will accept for one
// model id. It is the authoritative window: the server that enforces the limit
// is the one that states it. Zero when the endpoint cannot or does not say,
// which is most of them — only some servers (vLLM among them) report a context
// length on /v1/models — and a caller then falls back to the heuristic table.
//
// Best-effort by construction: the meter and the compaction threshold both have
// a table fallback, so a probe that fails must not fail the call.
func ModelContext(baseURL, apiKey, model string) int {
	if model == "" {
		return 0
	}
	served, err := fetchServedModels(baseURL, apiKey, 5*time.Second)
	if err != nil {
		return 0
	}
	for _, m := range served {
		if m.ID == model {
			return m.Context
		}
	}
	return 0
}

// servedModel is one entry of an OpenAI-compatible /v1/models listing, with the
// context window the server stated for it. Different servers name the field
// differently, so several aliases are read rather than betting on one.
type servedModel struct {
	ID      string
	Context int
}

// fetchServedModels lists what one endpoint serves and, where it says so, how
// many tokens each model accepts. The window fields are best-effort: a server
// that omits them yields entries with Context zero, which the callers treat as
// "unknown" and resolve from the heuristic table instead.
func fetchServedModels(baseURL, apiKey string, timeout time.Duration) ([]servedModel, error) {
	client := Client(timeout)
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
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
			ID               string `json:"id"`
			MaxModelLen      int    `json:"max_model_len"`
			ContextLength    int    `json:"context_length"`
			MaxContextLength int    `json:"max_context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]servedModel, 0, len(body.Data))
	for _, m := range body.Data {
		ctx := m.MaxModelLen
		if ctx <= 0 {
			ctx = m.ContextLength
		}
		if ctx <= 0 {
			ctx = m.MaxContextLength
		}
		out = append(out, servedModel{ID: m.ID, Context: ctx})
	}
	return out, nil
}

func LoadDotEnv() {
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

func ReadDotEnv() ([]string, error) {
	data, err := os.ReadFile(".env")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), nil
}

// WriteDotEnv writes the .env file with the permissions it needs — it holds
// provider keys — and in the order that cannot lose it: a temporary file in the
// same directory, then a rename over the original. A plain write truncates the
// file first, so a crash or a full disk in between leaves a session with no
// configuration at all and no way back to the one it had.
func WriteDotEnv(lines []string) error {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	if err := os.WriteFile(".env.tmp", []byte(sb.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(".env.tmp", ".env"); err != nil {
		os.Remove(".env.tmp")
		return err
	}
	return nil
}

// mergeDotEnv overlays vars onto existing .env lines. Keys the wizard does not
// manage are preserved verbatim, so a real provider key is never clobbered and
// switching back to it later costs nothing. Lines that are not assignments are
// kept as they are.
//
// It returns the merged file in order, plus the values that were actually
// written. The values are needed to build the provider, and the ordering is
// needed to rewrite the file without shuffling the user's comments around.
func MergeDotEnv(lines []string, vars map[string]string) ([]string, map[string]string) {
	out := make([]string, 0, len(lines)+len(vars))
	vals := make(map[string]string, len(vars))
	applied := make(map[string]bool, len(vars))

	for _, line := range lines {
		k, _, ok := DotEnvPair(line)
		if !ok {
			out = append(out, line)
			continue
		}
		if v, managed := vars[k]; managed {
			out = append(out, k+"="+v)
			vals[k] = v
			applied[k] = true
			continue
		}
		out = append(out, line)
	}
	for _, k := range SortedKeys(vars) {
		if !applied[k] {
			out = append(out, k+"="+vars[k])
			vals[k] = vars[k]
		}
	}
	return out, vals
}

func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// dotEnvPair extracts a key from a .env line, reporting false for comments and
// anything that is not an assignment.
func DotEnvPair(line string) (string, string, bool) {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
	if t == "" || strings.HasPrefix(t, "#") {
		return "", "", false
	}
	k, v, ok := strings.Cut(t, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`), true
}

// truncate shortens s to at most n bytes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
