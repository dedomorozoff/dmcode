package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"DMCODE_MODEL=" + SetupOptions()[0].Model,
		"DMCODE_API=" + APIChat,
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
		if o.BaseURL != "" && o.Model == "" {
			// A preset endpoint that names no model would send an empty model
			// field, because the wizard only prompts when baseURL is empty.
			t.Errorf("%q has a baseURL but no model", o.Label)
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
	inTempDir(t)
	idx := 0
	for i, o := range SetupOptions() {
		if o.GGUF {
			idx = i + 1
		}
	}
	in := fmt.Sprintf("%d\nmodels\\qwen.gguf\n\n", idx)
	if err := SetupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
		t.Fatalf("the GGUF wizard rejected a complete answer: %v", err)
	}
	data, _ := os.ReadFile(".env")
	text := string(data)
	for _, want := range []string{
		"DMCODE_GGUF=models\\qwen.gguf",
		"DMCODE_API=chat",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "DMCODE_LLAMA_SERVER") {
		t.Errorf("an empty binary answer wrote a variable:\n%s", text)
	}
	if strings.Contains(text, "API_KEY") {
		t.Errorf("a key was written for the GGUF option:\n%s", text)
	}

	// A named binary has to be recorded too.
	in = fmt.Sprintf("%d\nmodels\\qwen.gguf\nC:\\llama\\llama-server.exe\n", idx)
	if err := SetupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
		t.Fatalf("the GGUF wizard rejected a named binary: %v", err)
	}
	data, _ = os.ReadFile(".env")
	if !strings.Contains(string(data), "DMCODE_LLAMA_SERVER=C:\\llama\\llama-server.exe") {
		t.Errorf("the binary was not persisted:\n%s", data)
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

// Picking Unsloth has to ask for a key and an endpoint, and a blank answer to
// either must not be written out as a working config.
func TestUnslothPromptsForKeyAndEndpoint(t *testing.T) {
	inTempDir(t)
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

	// key, then blank endpoint: rejected.
	err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\n\nmodel-x\n", idx)), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Errorf("a blank endpoint gave %v, want a complaint about the URL", err)
	}
	if _, serr := os.Stat(".env"); serr == nil {
		t.Error(".env was written despite the missing endpoint")
	}

	// key, endpoint, blank Model: rejected.
	err = SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\nhttp://127.0.0.1:8888/v1\n\n", idx)), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("a blank model gave %v, want a complaint about the model", err)
	}

	// All three supplied: persisted exactly as given, which is the whole point
	// of asking, since Unsloth's port and model id are not guessable.
	if err := SetupWizardWith(strings.NewReader(fmt.Sprintf("%d\nsk-unsloth-abc\nhttp://127.0.0.1:8888/v1\nqwen3-27b\n", idx)), &strings.Builder{}); err != nil {
		t.Fatalf("a complete Unsloth answer was rejected: %v", err)
	}
	data, _ := os.ReadFile(".env")
	for _, want := range []string{
		"OPENAI_API_KEY=sk-unsloth-abc",
		"OPENAI_BASE_URL=http://127.0.0.1:8888/v1",
		"DMCODE_MODEL=qwen3-27b",
		"DMCODE_API=chat",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q:\n%s", want, data)
		}
	}
}

// reach the same .env as /setup and reject the same half-typed answers.
func TestStdinWizardWritesKeylessChoice(t *testing.T) {
	inTempDir(t)
	var out strings.Builder
	if err := SetupWizardWith(strings.NewReader("1\n"), &out); err != nil {
		t.Fatalf("choosing the keyless option failed: %v", err)
	}
	data, _ := os.ReadFile(".env")
	want := SetupOptions()[0]
	for _, w := range []string{
		"OPENAI_BASE_URL=" + want.BaseURL,
		"DMCODE_MODEL=" + want.Model,
		"DMCODE_API=chat",
	} {
		if !strings.Contains(string(data), w) {
			t.Errorf("missing %q:\n%s", w, data)
		}
	}
	if strings.Contains(string(data), "API_KEY") {
		t.Errorf("a key was written for a keyless provider:\n%s", data)
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
			err := SetupWizardWith(strings.NewReader(in), &strings.Builder{})
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
	err := SetupWizardWith(strings.NewReader("3\n\n"), &strings.Builder{})
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
	in := fmt.Sprintf("%d\nsk-abc\nhttp://127.0.0.1:1234/v1\nmy-model\n", len(SetupOptions()))
	if err := SetupWizardWith(strings.NewReader(in), &strings.Builder{}); err != nil {
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

// TestSetupVarsForKilo pins the Kilo option: it must write the free routing
// model and its own key variable, and must never clobber a real OpenAI key.
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
	if vars["DMCODE_MODEL"] != "kilo-auto/free" || vars["KILO_API_KEY"] != "kilo-key" || vars["DMCODE_API"] != APIChat {
		t.Errorf("SetupVars for Kilo = %v", vars)
	}
	if vars["OPENAI_API_KEY"] != "" {
		t.Errorf("Kilo must not clobber OPENAI_API_KEY: %v", vars)
	}
}
