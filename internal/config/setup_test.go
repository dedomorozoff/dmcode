package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dedomorozoff/dmcode/internal/settings"
)

// inTempDir runs the body in a scratch directory, since the wizard writes .env
// into the working directory and a test must never touch the real one.
func inTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

// A keyless option must not write a placeholder into a key variable: that would
// destroy a real key the user already had for a paid provider.
func TestKeylessSetupWritesNoKey(t *testing.T) {
	for _, o := range SetupOptions() {
		if !o.Keyless {
			continue
		}
		vars := SetupVars(o, "")
		for k := range vars {
			if strings.Contains(k, "KEY") || strings.Contains(k, "TOKEN") {
				t.Errorf("keyless option %q writes key variable %s", o.Label, k)
			}
		}
		if o.EnvKey != "" {
			t.Errorf("keyless option %q must not name a key variable, got %q", o.Label, o.EnvKey)
		}
	}
}

// A key-based option must still record the key, or the wizard would save a
// provider that cannot authenticate.
func TestKeyedSetupRecordsKey(t *testing.T) {
	for _, o := range SetupOptions() {
		if o.Keyless {
			continue
		}
		vars := SetupVars(o, "secret-value")
		if vars[o.EnvKey] != "secret-value" {
			t.Errorf("option %q wrote %s=%q, want the entered key", o.Label, o.EnvKey, vars[o.EnvKey])
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
	vars := SetupVars(SetupOptions()[0], "") // keyless Pollinations
	merged, vals := MergeDotEnv(existing, vars)

	text := strings.Join(merged, "\n")

	// The invariant is about the file: a real key for a paid provider must
	// survive choosing the free one, in place and unchanged.
	for _, want := range []string{
		"# мой комментарий",
		"MY_OWN=1",
		"OPENCODE_API_KEY=real-secret",
		"OPENAI_BASE_URL=" + SetupOptions()[0].BaseURL,
		"DMCODE_API=" + APIChat,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("merged .env lost %q:\n%s", want, text)
		}
	}
	// DMCODE_MODEL must NOT be in .env: the model is now in settings.json.
	if strings.Contains(text, "DMCODE_MODEL=") {
		t.Errorf("DMCODE_MODEL leaked into .env (it belongs in settings.json):\n%s", text)
	}
	// vals carries only what the wizard wrote; foreign keys stay untouched.
	if _, leaked := vals["OPENCODE_API_KEY"]; leaked {
		t.Error("the wizard claimed ownership of a key it does not manage")
	}
	// Every written line must still be parseable, or the next load breaks.
	for _, l := range merged {
		if _, _, ok := DotEnvPair(l); ok {
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
		k, v, ok := DotEnvPair(tc.line)
		if ok != tc.ok || k != tc.key || v != tc.val {
			t.Errorf("dotEnvPair(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.line, k, v, ok, tc.key, tc.val, tc.ok)
		}
	}
}

// TestSetupPinsTheMeasuredEndpoints pins the provider facts the live audit of
// 2026-11-17 measured, where a wrong entry breaks the provider outright rather
// than degrading it. GitHub Models serves only /chat/completions — its
// /responses path answers 404 (microsoft/apm#605 says the same) — so a
// responses wire would 404 every turn. Groq retired qwen/qwen3-32b in August
// 2026 and recommends openai/gpt-oss-120b in its place. Cline's gateway spells
// Claude ids with a dot: the live catalogue serves anthropic/claude-sonnet-4.6
// and has no claude-sonnet-4-6. When one of these fails, re-measure the
// endpoint first and update the pin together with the preset in
// internal/discover — the drift test holds the two lists together.
func TestSetupPinsTheMeasuredEndpoints(t *testing.T) {
	byLabel := func(prefix string) SetupOption {
		t.Helper()
		for _, o := range SetupOptions() {
			if strings.HasPrefix(o.Label, prefix) {
				return o
			}
		}
		t.Fatalf("no /setup option starting with %q", prefix)
		return SetupOption{}
	}
	if o := byLabel("GitHub Models"); o.API != APIChat {
		t.Errorf("GitHub Models wire = %q, want %q: the endpoint answers 404 to /responses", o.API, APIChat)
	}
	if o := byLabel("Groq"); o.Model != "openai/gpt-oss-120b" {
		t.Errorf("Groq model = %q, want openai/gpt-oss-120b: qwen/qwen3-32b was retired in August 2026", o.Model)
	}
	if o := byLabel("Cline"); o.Model != "anthropic/claude-sonnet-4.6" {
		t.Errorf("Cline model = %q, want anthropic/claude-sonnet-4.6: the catalogue spells Claude ids with a dot", o.Model)
	}
	if o := byLabel("Unsloth"); o.BaseURL != "http://localhost:8888/v1" || o.EnvKey != "UNSLOTH_API_KEY" || o.Model != "" {
		t.Errorf("Unsloth row = %q/%s/%q, want the documented localhost:8888 endpoint, its own key variable, and no model (the wizard picks one from what the server serves)",
			o.BaseURL, o.EnvKey, o.Model)
	}
}

// Every option the wizard offers must have somewhere to put its key, unless it
// is explicitly keyless.
func TestSetupOptionsAreWellFormed(t *testing.T) {
	names := map[string]bool{}
	for i, o := range SetupOptions() {
		if o.Label == "" {
			t.Errorf("option %d has no label", i)
		}
		if names[o.Label] {
			t.Errorf("%q is listed twice", o.Label)
		}
		names[o.Label] = true

		if o.API != APIChat && o.API != APIResponses {
			t.Errorf("%q has api=%q, want %q or %q", o.Label, o.API, APIChat, APIResponses)
		}
		if !o.Keyless && o.EnvKey == "" {
			t.Errorf("%q needs a key but names no envKey", o.Label)
		}
		if o.Keyless && o.EnvKey != "" {
			// A keyless option that still names a key variable will overwrite a
			// real key the user already had, with nothing useful in its place.
			t.Errorf("%q needs no key but would still write %s", o.Label, o.EnvKey)
		}
		// A preset endpoint that names no model may not send an empty model
		// field — unless the endpoint is this machine, whose model list the
		// wizard fetches instead of guessing an id. A remote catalogue with no
		// default id is the shape the provider audit showed roubles a provider
		// outright, so it stays an error.
		if o.BaseURL != "" && o.Model == "" && !isLoopback(o.BaseURL) {
			t.Errorf("%q has a baseURL but no model, and it is not local", o.Label)
		}
		if o.BaseURL != "" && !strings.HasPrefix(o.BaseURL, "http") {
			t.Errorf("%q has a malformed baseURL %q", o.Label, o.BaseURL)
		}
		if o.Signup == "" && !o.Keyless && o.BaseURL != "" {
			// A preset host the user has never heard of needs a signup link.
			// A custom endpoint does not: the user brought their own.
			t.Errorf("%q demands a key but never says where to get one", o.Label)
		}
	}
	// The list has to open with something that works with zero configuration,
	// or a fresh checkout cannot do anything at all.
	if first := SetupOptions()[0]; !first.Keyless {
		t.Errorf("the first option (%q) needs a key", first.Label)
	}
}

// Whatever /setup and the stdin wizard agree on has to be writable as-is: an
// empty value would be persisted and only fail later, at request time, far from
// the prompt that caused it.
func TestSetupVarsNeverEmpty(t *testing.T) {
	for _, o := range SetupOptions() {
		for k, v := range SetupVars(o, "k") {
			if v == "" {
				t.Errorf("%q produces an empty %s", o.Label, k)
			}
		}
	}
	// Options with no preset endpoint (the custom one, Unsloth) collect their
	// URL and model at run time, so they must not try to persist a guess. The
	// GGUF option is the same until its path is typed in.
	for _, o := range SetupOptions() {
		if o.BaseURL != "" || o.GGUF {
			continue
		}
		if got := SetupVars(o, "k"); len(got) != 2 {
			t.Errorf("%q wrote %v before the endpoint was even known", o.Label, got)
		}
	}
}

// The GGUF option is keyless, so it must never write a key, and it persists
// the file path only once the wizard actually collected one.
func TestSetupVarsForGGUF(t *testing.T) {
	var gguf *SetupOption
	for i := range SetupOptions() {
		if SetupOptions()[i].GGUF {
			gguf = &SetupOptions()[i]
		}
	}
	if gguf == nil {
		t.Fatal("no GGUF option in SetupOptions")
	}
	if got := SetupVars(*gguf, ""); len(got) != 1 || got["DMCODE_API"] != APIChat {
		t.Errorf("a GGUF option with no path yet wrote %v, want only the wire", got)
	}
	withPath := *gguf
	withPath.GGUFPath = `C:\models\qwen2.5-coder-7b-q4_k_m.gguf`
	vars := SetupVars(withPath, "")
	if vars["DMCODE_GGUF"] != withPath.GGUFPath {
		t.Errorf("SetupVars lost the .gguf path: %v", vars)
	}
	for k := range vars {
		if strings.Contains(k, "KEY") || strings.Contains(k, "TOKEN") {
			t.Errorf("the GGUF option wrote key variable %s", k)
		}
	}
}

// The stdin wizard must walk a GGUF pick through the path prompt and persist
// both the file and the optional server binary, still without any key.
func TestStdinWizardGGUFPersistsPathAndBinary(t *testing.T) {
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	inTempDir(t)
	idx := 0
	for i, o := range SetupOptions() {
		if o.GGUF {
			idx = i + 1
		}
	}
	in := fmt.Sprintf("%d\nmodels\\qwen.gguf\n\n", idx)
	if _, err := SetupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
		t.Fatalf("the GGUF wizard rejected a complete answer: %v", err)
	}
	st, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if st.GGUFPath != "models\\qwen.gguf" {
		t.Errorf("settings.GGUFPath = %q, want models\\qwen.gguf", st.GGUFPath)
	}
	if st.API != "chat" {
		t.Errorf("settings.API = %q, want chat", st.API)
	}
	if st.LlamaServer != "" {
		t.Errorf("settings.LlamaServer = %q after empty answer, want empty", st.LlamaServer)
	}
	if st.APIKey != "" {
		t.Errorf("settings.APIKey = %q for GGUF option, want empty", st.APIKey)
	}

	// A named binary has to be recorded too.
	in = fmt.Sprintf("%d\nmodels\\qwen.gguf\nC:\\llama\\llama-server.exe\n", idx)
	if _, err := SetupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
		t.Fatalf("the GGUF wizard rejected a named binary: %v", err)
	}
	st, err = settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if st.LlamaServer != "C:\\llama\\llama-server.exe" {
		t.Errorf("settings.LlamaServer = %q, want C:\\llama\\llama-server.exe", st.LlamaServer)
	}
}

func TestReadDotEnvMissingFile(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadDotEnv()
	if err != nil {
		t.Fatalf("a missing .env must not be an error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("missing .env returned %d lines", len(lines))
	}
	_ = filepath.Join(dir, ".env")
}

// Picking Unsloth has to ask for a key and then a model — the endpoint is the
// documented localhost:8888 default and is never asked for — and a blank model
// must not be written out as a working config.
func TestUnslothAsksForKeyAndModel(t *testing.T) {
	inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	opts := SetupOptions()
	idx := 0
	for i, o := range opts {
		if strings.Contains(o.Label, "Unsloth") {
			idx = i + 1
		}
	}
	if idx == 0 {
		t.Fatal("Unsloth is not in the option list")
	}
	// The wizard's fetch goes through the seam, so no request leaves the
	// machine — even on a box where Unsloth Studio is actually listening on
	// 8888, where a live probe would answer the blank-model case itself and
	// desynchronise every input line below.
	fetchModels = func(Provider) ([]string, error) { return nil, errors.New("connection refused") }
	t.Cleanup(func() { fetchModels = ListModels })

	// key, then blank model: rejected. There is no URL prompt to be blank at.
	_, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\n\n", idx)), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("a blank model gave %v, want a complaint about the model", err)
	}
	if _, serr := os.Stat(".env"); serr == nil {
		t.Error(".env was written despite the missing model")
	}

	// key and model: persisted, with the key under the name Unsloth's own docs
	// use — so configuring Unsloth cannot overwrite a working OPENAI_API_KEY
	// some other option already wrote.
	if _, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\nqwen3-27b\n", idx)), &strings.Builder{}); err != nil {
		t.Fatalf("a complete Unsloth answer was rejected: %v", err)
	}
	st, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if st.APIKey != "sk-unsloth-abc" {
		t.Errorf("settings.APIKey = %q, want sk-unsloth-abc", st.APIKey)
	}
	if st.BaseURL != "http://localhost:8888/v1" {
		t.Errorf("settings.BaseURL = %q, want the documented default", st.BaseURL)
	}
	if st.Model != "qwen3-27b" {
		t.Errorf("settings.Model = %q, want qwen3-27b", st.Model)
	}
	if st.API != "chat" {
		t.Errorf("settings.API = %q, want chat", st.API)
	}
	// .env should not be created — all config goes to settings.json.
	if _, serr := os.Stat(".env"); serr == nil {
		t.Error(".env was created; all config should go to settings.json")
	}
}

// TestUnslothPicksFromTheServedList drives the stdin wizard's model choice.
// The fetch goes through the fetchModels seam, so no request leaves the
// machine; the picker mirrors the TUI's: a number picks a row, enter takes the
// first, and an endpoint that serves nothing falls back to the typed prompt.
func TestUnslothPicksFromTheServedList(t *testing.T) {
	inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	opts := SetupOptions()
	idx := 0
	for i, o := range opts {
		if strings.Contains(o.Label, "Unsloth") {
			idx = i + 1
		}
	}
	if idx == 0 {
		t.Fatal("Unsloth is not in the option list")
	}
	ids := []string{"unsloth/Qwen3-Coder-30B", "unsloth/Qwen3-VL-32B"}
	fetchModels = func(Provider) ([]string, error) { return ids, nil }
	t.Cleanup(func() { fetchModels = ListModels })

	// A number picks that row of the served list.
	var out strings.Builder
	if _, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\n2\n", idx)), &out); err != nil {
		t.Fatalf("a served list was rejected: %v", err)
	}
	if st, err := settings.Load(); err != nil || st.Model != ids[1] {
		t.Errorf("model = %q (%v), want %q — the row the number named", st.Model, err, ids[1])
	}
	if !strings.Contains(out.String(), ids[0]) {
		t.Errorf("the wizard did not show the served ids:\n%s", out.String())
	}

	// enter on the choice takes the first row, the way the TUI picker does.
	if _, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\n\n", idx)), &strings.Builder{}); err != nil {
		t.Fatalf("enter on the served list was rejected: %v", err)
	}
	if st, err := settings.Load(); err != nil || st.Model != ids[0] {
		t.Errorf("model = %q (%v), want %q — the highlighted row", st.Model, err, ids[0])
	}

	// An endpoint that answers nothing falls back to the typed prompt.
	fetchModels = func(Provider) ([]string, error) { return nil, errors.New("connection refused") }
	if _, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\nqwen3-27b\n", idx)), &strings.Builder{}); err != nil {
		t.Fatalf("the typed fallback was rejected: %v", err)
	}
	if st, err := settings.Load(); err != nil || st.Model != "qwen3-27b" {
		t.Errorf("model = %q (%v), want the typed qwen3-27b", st.Model, err)
	}
}

// The wizard has to hand back a provider the caller can start on, because the
// environment cannot carry one. Two things used to go missing on the way, and
// both were invisible until a session refused to start:
//
//   - A keyless option writes no OPENAI_API_KEY — on purpose, so it cannot
//     clobber a real one — so a caller reading the key back found it empty and
//     called that "no provider configured". That was every free option in the
//     list, and the first one the wizard offers.
//   - DMCODE_MODEL is deliberately kept out of .env, being a user preference
//     rather than part of the endpoint, so the chosen model did not come back
//     either and OrDefaultModel substituted qwen2.5-coder:7b — an Ollama model
//     id, sent to whichever host the user had just picked.
//
// An empty APIKey here is therefore the correct answer for a keyless option and
// not a missing one, and it is what the chat adapter's placeholder bearer token
// is for.
func TestAWizardAnswerIsUsableWithoutReadingTheEnvironmentBack(t *testing.T) {
	inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))

	keyless := SetupOptions()[0]
	if !keyless.Keyless {
		t.Fatalf("option 1 (%q) is not keyless, so this covers nothing", keyless.Label)
	}
	p, err := SetupWizardWith(strings.NewReader("1\n"), &strings.Builder{})
	if err != nil {
		t.Fatalf("the keyless answer was rejected: %v", err)
	}
	if p.BaseURL != keyless.BaseURL || p.Model != keyless.Model ||
		p.API != keyless.API || p.Reasoning != keyless.Reasoning {
		t.Errorf("got %+v, want the option's endpoint, model, wire and reasoning", p)
	}
	if p.APIKey != "" {
		t.Errorf("a keyless option returned the key %q", p.APIKey)
	}
	if p.APIKeyVar != "" {
		t.Errorf("a keyless option named the key variable %q; it would overwrite a real key on load", p.APIKeyVar)
	}

	// The same must hold for a keyed option, or the fix only covered the half
	// that was already broken loudly.
	idx := 0
	for i, o := range SetupOptions() {
		if !o.Keyless && !o.GGUF && o.BaseURL != "" {
			idx = i + 1
			break
		}
	}
	if idx == 0 {
		t.Fatal("no option that asks for a key")
	}
	k, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-from-the-wizard\n", idx)), &strings.Builder{})
	if err != nil {
		t.Fatalf("the keyed answer was rejected: %v", err)
	}
	want := SetupOptions()[idx-1]
	if k.APIKey != "sk-from-the-wizard" {
		t.Errorf("api key = %q, want the one that was typed", k.APIKey)
	}
	if k.APIKeyVar != want.EnvKey {
		t.Errorf("api key var = %q, want %q", k.APIKeyVar, want.EnvKey)
	}
	if k.Model != want.Model || k.BaseURL != want.BaseURL {
		t.Errorf("got %+v, want %s at %s", k, want.Model, want.BaseURL)
	}
}

// TestStdinWizardWritesKeylessChoice verifies the keyless option writes all
// its config to settings.json.
func TestStdinWizardWritesKeylessChoice(t *testing.T) {
	inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	var out strings.Builder
	if _, err := SetupWizardWith(strings.NewReader("1\n"), &out); err != nil {
		t.Fatalf("choosing the keyless option failed: %v", err)
	}
	// Check settings.json has all the values.
	want := SetupOptions()[0]
	st, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if st.BaseURL != want.BaseURL {
		t.Errorf("settings.BaseURL = %q, want %q", st.BaseURL, want.BaseURL)
	}
	if st.Model != want.Model {
		t.Errorf("settings.Model = %q, want %q", st.Model, want.Model)
	}
	if st.API != want.API {
		t.Errorf("settings.API = %q, want %q", st.API, want.API)
	}
	if st.Reasoning != want.Reasoning {
		t.Errorf("settings.Reasoning = %q, want %q", st.Reasoning, want.Reasoning)
	}
	// .env should not be created — all config goes to settings.json.
	if _, err := os.Stat(".env"); err == nil {
		t.Error(".env was created; all config should go to settings.json")
	}
	if !strings.Contains(out.String(), want.Label) {
		t.Error("the prompt did not offer the free option")
	}
}

// The custom endpoint has no defaults, so a half-typed answer used to be
// persisted as an empty model and only fail later, at request time.
func TestStdinWizardRejectsIncompleteCustomEndpoint(t *testing.T) {
	custom := len(SetupOptions()) // 1-based index of the last option
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"no url", "sk-1\n\nmy-model\n", "no base URL entered"},
		{"no model", "sk-1\nhttp://127.0.0.1:1234/v1\n\n", "no model entered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inTempDir(t)
			in := fmt.Sprintf("%d\n%s", custom, tc.input)
			_, err := SetupWizardWith(strings.NewReader(in), &strings.Builder{})
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
	// The option is found by what it asks for, not by where it sits: /setup's
	// order is free-first and moves as providers are added, and a test pinned to
	// a row number ends up prompting for the wrong thing — a .gguf path — and
	// reports a failure about the key that never happened.
	idx := 0
	for i, o := range SetupOptions() {
		if !o.Keyless && !o.GGUF && o.BaseURL != "" {
			idx = i + 1
			break
		}
	}
	if idx == 0 {
		t.Fatal("no option that asks for a key")
	}
	_, err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\n\n", idx)), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "no key entered") {
		t.Errorf("an empty key gave %v, want a complaint about the key", err)
	}
	if _, serr := os.Stat(".env"); serr == nil {
		t.Error(".env was written without a key")
	}
}

// A custom endpoint that is filled in properly must be persisted.
func TestStdinWizardAcceptsCompleteCustomEndpoint(t *testing.T) {
	inTempDir(t)
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	in := fmt.Sprintf("%d\nsk-abc\nhttp://127.0.0.1:1234/v1\nmy-model\n", len(SetupOptions()))
	if _, err := SetupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
		t.Fatalf("a complete custom endpoint was rejected: %v", err)
	}
	// Check settings.json has all the values.
	st, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if st.BaseURL != "http://127.0.0.1:1234/v1" {
		t.Errorf("settings.BaseURL = %q, want http://127.0.0.1:1234/v1", st.BaseURL)
	}
	if st.APIKey != "sk-abc" {
		t.Errorf("settings.APIKey = %q, want sk-abc", st.APIKey)
	}
	if st.Model != "my-model" {
		t.Errorf("settings.Model = %q, want my-model", st.Model)
	}
	// .env should not be created — all config goes to settings.json.
	if _, err := os.Stat(".env"); err == nil {
		t.Error(".env was created; all config should go to settings.json")
	}
}

// TestSetupVarsForKilo pins the Kilo option: it must write its own key variable
// and must never clobber a real OpenAI key. The model goes to settings.json.
func TestSetupVarsForKilo(t *testing.T) {
	var kilo *SetupOption
	for i := range SetupOptions() {
		if SetupOptions()[i].EnvKey == "KILO_API_KEY" {
			kilo = &SetupOptions()[i]
		}
	}
	if kilo == nil {
		t.Fatal("no Kilo option in SetupOptions")
	}
	vars := SetupVars(*kilo, "kilo-key")
	if vars["KILO_API_KEY"] != "kilo-key" || vars["DMCODE_API"] != APIChat {
		t.Errorf("SetupVars for Kilo = %v", vars)
	}
	if _, ok := vars["DMCODE_MODEL"]; ok {
		t.Errorf("DMCODE_MODEL must not be in SetupVars (belongs in settings.json): %v", vars)
	}
	if vars["OPENAI_API_KEY"] != "" {
		t.Errorf("Kilo must not clobber OPENAI_API_KEY: %v", vars)
	}
}
