package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The hint text used to point at a command that did not exist. Whatever it
// names has to be reachable, or a blocked user has nowhere to go.
func TestFreeHintNamesRealCommand(t *testing.T) {
	// Pull out the /command token rather than guessing at word counts.
	var named []string
	for _, f := range strings.Fields(freeProviderHint) {
		if strings.HasPrefix(f, "/") {
			named = append(named, strings.Trim(f, "/.,;"))
		}
	}
	if len(named) == 0 {
		t.Fatalf("freeProviderHint names no command at all: %q", freeProviderHint)
	}

	names := map[string]bool{}
	for _, c := range (&uiModel{}).commands() {
		names[c.name] = true
	}
	help := hintCommandsLine()
	for _, cmd := range named {
		if !names[cmd] {
			t.Errorf("freeProviderHint points at /%s, which is not a command; known: %v", cmd, names)
		}
		if !strings.Contains(help, "/"+cmd) {
			t.Errorf("/%s is missing from the /help listing: %q", cmd, help)
		}
	}
}

// hintCommandsLine is the command list /help prints.
func hintCommandsLine() string {
	var m uiModel
	for _, c := range m.commands() {
		if c.name != "help" {
			continue
		}
		c.run(&m)
		for _, l := range m.history {
			if l.kind == kindSys && strings.Contains(l.text, "/quit") {
				return l.text
			}
		}
	}
	return ""
}

// A keyless option must not write a placeholder into a key variable: that would
// destroy a real key the user already had for a paid provider.
func TestKeylessSetupWritesNoKey(t *testing.T) {
	for _, o := range setupOptions() {
		if !o.keyless {
			continue
		}
		vars := setupVars(o, "")
		for k := range vars {
			if strings.Contains(k, "KEY") || strings.Contains(k, "TOKEN") {
				t.Errorf("keyless option %q writes key variable %s", o.label, k)
			}
		}
		if o.envKey != "" {
			t.Errorf("keyless option %q must not name a key variable, got %q", o.label, o.envKey)
		}
	}
}

// A key-based option must still record the key, or the wizard would save a
// provider that cannot authenticate.
func TestKeyedSetupRecordsKey(t *testing.T) {
	for _, o := range setupOptions() {
		if o.keyless {
			continue
		}
		vars := setupVars(o, "secret-value")
		if vars[o.envKey] != "secret-value" {
			t.Errorf("option %q wrote %s=%q, want the entered key", o.label, o.envKey, vars[o.envKey])
		}
	}
}

func TestMergeDotEnvPreservesForeignKeys(t *testing.T) {
	existing := []string{
		"# мой комментарий",
		"OPENCODE_API_KEY=real-secret",
		"export OPENAI_BASE_URL=https://old.example/v1",
		"MY_OWN=1",
		"",
	}
	vars := setupVars(setupOptions()[0], "") // keyless Pollinations
	merged, vals := mergeDotEnv(existing, vars)

	text := strings.Join(merged, "\n")

	// The invariant is about the file: a real key for a paid provider must
	// survive choosing the free one, in place and unchanged.
	for _, want := range []string{
		"# мой комментарий",
		"MY_OWN=1",
		"OPENCODE_API_KEY=real-secret",
		"OPENAI_BASE_URL=" + setupOptions()[0].baseURL,
		"DMCODE_MODEL=" + setupOptions()[0].model,
		"DMCODE_API=" + apiChat,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("merged .env lost %q:\n%s", want, text)
		}
	}
	// vals carries only what the wizard wrote; foreign keys stay untouched.
	if _, leaked := vals["OPENCODE_API_KEY"]; leaked {
		t.Error("the wizard claimed ownership of a key it does not manage")
	}
	// Every written line must still be parseable, or the next load breaks.
	for _, l := range merged {
		if _, _, ok := dotEnvPair(l); ok {
			if strings.TrimSpace(l) == "" {
				t.Error("blank line was written as an assignment")
			}
		}
	}
}

func TestDotEnvPair(t *testing.T) {
	cases := []struct {
		line string
		key  string
		val  string
		ok   bool
	}{
		{"A=1", "A", "1", true},
		{"  A = 1  ", "A", "1", true},
		{"export A=1", "A", "1", true},
		{`A="quoted"`, "A", "quoted", true},
		{"# A=1", "", "", false},
		{"", "", "", false},
		{"not an assignment", "", "", false},
	}
	for _, tc := range cases {
		k, v, ok := dotEnvPair(tc.line)
		if ok != tc.ok || k != tc.key || v != tc.val {
			t.Errorf("dotEnvPair(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.line, k, v, ok, tc.key, tc.val, tc.ok)
		}
	}
}

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

	p, err := detectProvider()
	if err != nil {
		t.Fatalf("detectProvider: %v", err)
	}
	if p.baseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("baseURL = %q, want the configured endpoint", p.baseURL)
	}
	if p.model != "qwen2.5-coder:7b" {
		t.Errorf("model = %q", p.model)
	}
}

// Every option the wizard offers must have somewhere to put its key, unless it
// is explicitly keyless.
func TestSetupOptionsAreWellFormed(t *testing.T) {
	names := map[string]bool{}
	for i, o := range setupOptions() {
		if o.label == "" {
			t.Errorf("option %d has no label", i)
		}
		if names[o.label] {
			t.Errorf("%q is listed twice", o.label)
		}
		names[o.label] = true

		if o.api != apiChat && o.api != apiResponses {
			t.Errorf("%q has api=%q, want %q or %q", o.label, o.api, apiChat, apiResponses)
		}
		if !o.keyless && o.envKey == "" {
			t.Errorf("%q needs a key but names no envKey", o.label)
		}
		if o.keyless && o.envKey != "" {
			// A keyless option that still names a key variable will overwrite a
			// real key the user already had, with nothing useful in its place.
			t.Errorf("%q needs no key but would still write %s", o.label, o.envKey)
		}
		if o.baseURL != "" && o.model == "" {
			// A preset endpoint that names no model would send an empty model
			// field, because the wizard only prompts when baseURL is empty.
			t.Errorf("%q has a baseURL but no model", o.label)
		}
		if o.baseURL != "" && !strings.HasPrefix(o.baseURL, "http") {
			t.Errorf("%q has a malformed baseURL %q", o.label, o.baseURL)
		}
		if o.signup == "" && !o.keyless && o.baseURL != "" {
			// A preset host the user has never heard of needs a signup link.
			// A custom endpoint does not: the user brought their own.
			t.Errorf("%q demands a key but never says where to get one", o.label)
		}
	}
	// The list has to open with something that works with zero configuration,
	// or a fresh checkout cannot do anything at all.
	if first := setupOptions()[0]; !first.keyless {
		t.Errorf("the first option (%q) needs a key", first.label)
	}
}

// Whatever /setup and the stdin wizard agree on has to be writable as-is: an
// empty value would be persisted and only fail later, at request time, far from
// the prompt that caused it.
func TestSetupVarsNeverEmpty(t *testing.T) {
	for _, o := range setupOptions() {
		for k, v := range setupVars(o, "k") {
			if v == "" {
				t.Errorf("%q produces an empty %s", o.label, k)
			}
		}
	}
	// The custom option has no defaults: they are asked for at run time.
	opts := setupOptions()
	custom := opts[len(opts)-1]
	if got := setupVars(custom, "k"); len(got) != 2 {
		t.Errorf("custom option wrote %v before the endpoint was even known", got)
	}
}

// setupLabel is shown in the sidebar, so it must not leak a full URL with a
// secret-bearing path, and must not be empty.
func TestSetupLabel(t *testing.T) {
	for _, o := range setupOptions() {
		l := setupLabel(o)
		if l == "" {
			t.Errorf("%q produced an empty label", o.label)
		}
		if strings.Contains(l, "://") {
			t.Errorf("%q produced %q, which still carries the scheme", o.label, l)
		}
	}
	if got := setupLabel(setupOptions()[0]); got != "text.pollinations.ai" {
		t.Errorf("Pollinations label = %q, want %q", got, "text.pollinations.ai")
	}
}

func TestReadDotEnvMissingFile(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	lines, err := readDotEnv()
	if err != nil {
		t.Fatalf("a missing .env must not be an error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("missing .env returned %d lines", len(lines))
	}
	_ = filepath.Join(dir, ".env")
}

// toollessServer answers /models happily but replies with prose, ignoring the
// tools array. This is the exact shape of provider the check has to catch: it
// looks healthy until dmcode fails to touch a file.
func toollessServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"prose-only"}]}`)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n",
			`{"choices":[{"delta":{"content":"К сожалению, я не могу вызвать инструменты."}}]}`)
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifyToolsAcceptsToolCalling(t *testing.T) {
	srv := fakeChatServer(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"probe_ok","arguments":"{}"}}]}}]}`,
	}, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	ok, conclusive := verifyTools(srv.URL+"/v1", "", "test-coder", 5*time.Second)
	if !ok || !conclusive {
		t.Errorf("verifyTools = (ok=%v, conclusive=%v), want (true, true)", ok, conclusive)
	}
}

// The whole point of the check: a provider that ignores tools must be reported
// as a definitive "no" so the user learns about it instead of watching every
// turn come back as prose.
func TestVerifyToolsRejectsToollessProvider(t *testing.T) {
	srv := toollessServer(t)

	ok, conclusive := verifyTools(srv.URL+"/v1", "", "prose-only", 5*time.Second)
	if ok {
		t.Error("a provider that never calls the tool was reported as OK")
	}
	if !conclusive {
		t.Error("a complete prose answer must be conclusive, not inconclusive")
	}
}

// An unreachable or silent endpoint proves nothing about tool support. Calling it
// inconclusive is what stops a slow local server being discarded.
func TestVerifyToolsInconclusiveOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	ok, conclusive := verifyTools(srv.URL, "", "whatever", 3*time.Second)
	if ok {
		t.Error("a failing endpoint was reported as tool-capable")
	}
	if conclusive {
		t.Error("a transport failure must be inconclusive, or a good but slow provider gets rejected")
	}
}

func TestVerifyToolsInconclusiveOnDeadHost(t *testing.T) {
	ok, conclusive := verifyTools("http://127.0.0.1:1", "", "x", 2*time.Second)
	if ok || conclusive {
		t.Errorf("verifyTools on a dead host = (%v, %v), want (false, false)", ok, conclusive)
	}
}

// The check is only meaningful on the chat wire: probing a /v1/responses
// provider with a /chat/completions request would report a false failure.
func TestToolCheckSkipsResponsesWire(t *testing.T) {
	m := &uiModel{prov: provider{
		baseURL: "https://example.invalid/v1",
		model:   "some-model",
		api:     apiResponses,
	}}
	if cmd := m.toolCheckCmd(); cmd != nil {
		t.Error("a responses-wire provider must not be probed by the chat client")
	}

	m.prov.api = apiChat
	if cmd := m.toolCheckCmd(); cmd == nil {
		t.Error("a chat-wire provider must be probed")
	}
}

// A failed check is worth showing; a passed or undecided one is noise.
func TestHandleToolCheckReporting(t *testing.T) {
	base := func() *uiModel {
		return &uiModel{prov: provider{label: "Pollinations (без ключа)", model: "openai-fast", api: apiChat}}
	}

	m := base()
	m.handleToolCheck(toolCheckMsg{ok: true, conclusive: true, model: m.prov.model, label: m.prov.label})
	if len(m.history) != 0 {
		t.Errorf("a passing check produced %d transcript lines", len(m.history))
	}

	m = base()
	m.handleToolCheck(toolCheckMsg{ok: false, conclusive: false, model: m.prov.model, label: m.prov.label})
	if len(m.history) != 0 {
		t.Errorf("an inconclusive check produced %d transcript lines", len(m.history))
	}

	m = base()
	m.handleToolCheck(toolCheckMsg{ok: false, conclusive: true, model: m.prov.model, label: m.prov.label})
	if len(m.history) != 1 {
		t.Fatalf("a failed check produced %d lines, want 1", len(m.history))
	}
	if !strings.Contains(m.history[0].text, "/setup") {
		t.Errorf("the advisory does not offer a way out: %q", m.history[0].text)
	}

	// A result for a model the user has since switched away from is dropped.
	m = base()
	m.handleToolCheck(toolCheckMsg{ok: false, conclusive: true, model: "old-model", label: m.prov.label})
	if len(m.history) != 0 {
		t.Error("a stale check result was reported against the current model")
	}
}

// The polling default is the point of the whole feature, so keep it honest: the
// hosted list must contain a provider that needs no key at all.
func TestHostedCandidatesAreKeyless(t *testing.T) {
	if len(hostedCandidates) == 0 {
		t.Fatal("no hosted free candidate: a fresh checkout would need setup")
	}
	for _, c := range hostedCandidates {
		if !strings.HasPrefix(c.baseURL, "https://") {
			t.Errorf("%s uses %q, which is not TLS", c.name, c.baseURL)
		}
		if c.local {
			t.Errorf("%s is listed as hosted but marked local", c.name)
		}
		if len(c.models) == 0 {
			t.Errorf("%s names no model to fall back on", c.name)
		}
	}
}

// The stdin wizard is the fallback when there is no terminal, so it has to
// reach the same .env as /setup and reject the same half-typed answers.
func TestStdinWizardWritesKeylessChoice(t *testing.T) {
	inTempDir(t)
	var out strings.Builder
	if err := setupWizardWith(strings.NewReader("1\n"), &out); err != nil {
		t.Fatalf("choosing the keyless option failed: %v", err)
	}
	data, _ := os.ReadFile(".env")
	want := setupOptions()[0]
	for _, w := range []string{
		"OPENAI_BASE_URL=" + want.baseURL,
		"DMCODE_MODEL=" + want.model,
		"DMCODE_API=chat",
	} {
		if !strings.Contains(string(data), w) {
			t.Errorf("missing %q:\n%s", w, data)
		}
	}
	if strings.Contains(string(data), "API_KEY") {
		t.Errorf("a key was written for a keyless provider:\n%s", data)
	}
	if !strings.Contains(out.String(), want.label) {
		t.Error("the prompt did not offer the free option")
	}
}

// The custom endpoint has no defaults, so a half-typed answer used to be
// persisted as an empty model and only fail later, at request time.
func TestStdinWizardRejectsIncompleteCustomEndpoint(t *testing.T) {
	custom := len(setupOptions()) // 1-based index of the last option
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"no url", "sk-1\n\nmy-model\n", "base URL не введён"},
		{"no model", "sk-1\nhttp://127.0.0.1:1234/v1\n\n", "модель не введена"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inTempDir(t)
			in := fmt.Sprintf("%d\n%s", custom, tc.input)
			err := setupWizardWith(strings.NewReader(in), &strings.Builder{})
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			if _, serr := os.Stat(".env"); serr == nil {
				data, _ := os.ReadFile(".env")
				t.Errorf("a broken config was written anyway:\n%s", data)
			}
		})
	}
}

func TestStdinWizardRejectsEmptyKey(t *testing.T) {
	inTempDir(t)
	// Option 3 is OpenRouter, which needs a key.
	err := setupWizardWith(strings.NewReader("3\n\n"), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "ключ не введён") {
		t.Errorf("an empty key gave %v, want a complaint about the key", err)
	}
	if _, serr := os.Stat(".env"); serr == nil {
		t.Error(".env was written without a key")
	}
}

// A custom endpoint that is filled in properly must be persisted.
func TestStdinWizardAcceptsCompleteCustomEndpoint(t *testing.T) {
	inTempDir(t)
	in := fmt.Sprintf("%d\nsk-abc\nhttp://127.0.0.1:1234/v1\nmy-model\n", len(setupOptions()))
	if err := setupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
		t.Fatalf("a complete custom endpoint was rejected: %v", err)
	}
	data, _ := os.ReadFile(".env")
	for _, w := range []string{
		"OPENAI_BASE_URL=http://127.0.0.1:1234/v1",
		"DMCODE_MODEL=my-model",
		"OPENAI_API_KEY=sk-abc",
	} {
		if !strings.Contains(string(data), w) {
			t.Errorf("missing %q:\n%s", w, data)
		}
	}
}
func TestCandidateModelsAreDistinct(t *testing.T) {
	for _, c := range append(append([]freeCandidate{}, localCandidates...), hostedCandidates...) {
		seen := map[string]bool{}
		for _, mdl := range c.models {
			if seen[mdl] {
				t.Errorf("%s lists %q twice", c.name, mdl)
			}
			seen[mdl] = true
		}
	}
}
