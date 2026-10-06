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
	BaseURL string
	APIKey  string
	Model   string
	API     string // apiResponses (default) or apiChat
	Label   string // human-readable provider name for the UI
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
	client := Client(15 * time.Second)
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
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
