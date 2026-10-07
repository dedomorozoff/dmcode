package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// inEnvDir moves into a temp directory so a test that writes .env cannot touch
// the real one in the repository. (setup_test.go has its own inTempDir.)
func inEnvDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	return dir
}

func writeEnv(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(".env", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readEnv(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSaveModelReachesTheNextStart is the whole point: a model chosen in the
// running session has to be the one the next start picks, and the only thing
// that decides that is DMCODE_MODEL in .env.
func TestSaveModelReachesTheNextStart(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "# my notes\nOPENAI_BASE_URL=https://openrouter.ai/api/v1\nOPENAI_API_KEY=sk-x\nDMCODE_MODEL=old/model\n")

	p := Provider{BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-x"}
	if err := SaveModel(p, "deepseek/deepseek-chat-v3.1:free"); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}

	got := readEnv(t)
	if !strings.Contains(got, "DMCODE_MODEL=deepseek/deepseek-chat-v3.1:free") {
		t.Errorf("the model is not in .env:\n%s", got)
	}
	// Everything else in the file is somebody's configuration, and a comment is
	// somebody's own words.
	for _, keep := range []string{"# my notes", "OPENAI_BASE_URL=https://openrouter.ai/api/v1", "OPENAI_API_KEY=sk-x"} {
		if !strings.Contains(got, keep) {
			t.Errorf("writing the model lost %q:\n%s", keep, got)
		}
	}
	if n := strings.Count(got, "DMCODE_MODEL="); n != 1 {
		t.Errorf("DMCODE_MODEL appears %d times:\n%s", n, got)
	}
}

// TestSaveModelAddsTheLineWhenTheFileNeverHadOne is the other shape: the
// endpoint is configured, the model was left at whatever the provider's default
// is, and nothing on disk mentions a model yet.
func TestSaveModelAddsTheLineWhenTheFileNeverHadOne(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "GROQ_API_KEY=gsk_x\n")

	if err := SaveModel(Provider{BaseURL: "https://api.groq.com/openai/v1"}, "qwen/qwen3-32b"); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	if !strings.Contains(readEnv(t), "DMCODE_MODEL=qwen/qwen3-32b") {
		t.Errorf("the model was not added:\n%s", readEnv(t))
	}
}

// TestSaveModelRefusesWhenTheFileDoesNotNameThisEndpoint. The refusal is the
// whole design: a model id means nothing without the endpoint that serves it,
// and the free-endpoint discovery path ignores DMCODE_MODEL entirely — so a
// line written into a file that does not describe this provider would either do
// nothing today or be applied to a different endpoint tomorrow.
func TestSaveModelRefusesWhenTheFileDoesNotNameThisEndpoint(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "OPENAI_BASE_URL=https://api.groq.com/openai/v1\nOPENAI_API_KEY=sk-x\n")

	err := SaveModel(Provider{BaseURL: "https://openrouter.ai/api/v1"}, "deepseek/deepseek-chat-v3.1:free")
	if !errors.Is(err, ErrProviderNotInEnv) {
		t.Fatalf("SaveModel returned %v, want ErrProviderNotInEnv", err)
	}
	if strings.Contains(readEnv(t), "DMCODE_MODEL") {
		t.Errorf("a model was written for an endpoint .env does not configure:\n%s", readEnv(t))
	}
}

func TestSaveModelRefusesWithNoFileAtAll(t *testing.T) {
	inEnvDir(t)
	if err := SaveModel(Provider{BaseURL: "http://127.0.0.1:11434/v1"}, "qwen2.5-coder:7b"); !errors.Is(err, ErrProviderNotInEnv) {
		t.Errorf("SaveModel with no .env = %v, want ErrProviderNotInEnv", err)
	}
	if _, err := os.Stat(".env"); !os.IsNotExist(err) {
		t.Error("a .env was created out of nothing; the file has to be the user's")
	}
}

// TestEnvDescribesProvider reads the two ways the startup path is told, and
// refuses a line that names somewhere else.
func TestEnvDescribesProvider(t *testing.T) {
	groq := Provider{BaseURL: "https://api.groq.com/openai/v1"}
	for _, tc := range []struct {
		name  string
		lines []string
		want  bool
	}{
		{"explicit base url", []string{"OPENAI_BASE_URL=https://api.groq.com/openai/v1"}, true},
		{"with a trailing slash", []string{"OPENAI_BASE_URL=https://api.groq.com/openai/v1/"}, true},
		{"a preset's key", []string{"GROQ_API_KEY=gsk_x"}, true},
		{"the same host under http", []string{"OPENAI_BASE_URL=http://api.groq.com/openai/v1"}, true},
		{"another endpoint", []string{"OPENAI_BASE_URL=https://openrouter.ai/api/v1"}, false},
		{"only a comment", []string{"# OPENAI_BASE_URL=https://api.groq.com/openai/v1"}, false},
		{"an empty file", nil, false},
		{"no host at all", []string{"DMCODE_MODEL=qwen"}, false},
	} {
		if got := EnvDescribesProvider(tc.lines, groq); got != tc.want {
			t.Errorf("%s: EnvDescribesProvider = %v, want %v", tc.name, got, tc.want)
		}
	}
	if EnvDescribesProvider([]string{"OPENAI_BASE_URL=https://api.groq.com/openai/v1"}, Provider{}) {
		t.Error("a provider with no endpoint was described by the file")
	}
}

// TestWriteDotEnvIsAtomicAndPrivate: the file holds a provider key, and a write
// interrupted half way would leave a session with no configuration at all.
func TestWriteDotEnvIsAtomicAndPrivate(t *testing.T) {
	dir := inEnvDir(t)
	writeEnv(t, "OLD=1\n")

	if err := WriteDotEnv([]string{"NEW=2", "# kept"}); err != nil {
		t.Fatal(err)
	}
	if got := readEnv(t); !strings.Contains(got, "NEW=2") || !strings.Contains(got, "# kept") {
		t.Errorf("the file was not replaced as asked:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.tmp")); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
	// Windows keeps no permission bits: os.Chmod there only toggles the
	// read-only attribute, so the mode of a file written 0600 still reads 0666.
	// The assertion is the same one memsession skips for the same reason.
	if runtime.GOOS == "windows" {
		t.Log("the permission mode is not expressible on Windows; skipped")
		return
	}
	if fi, err := os.Stat(".env"); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf(".env is %v; a provider key must not be readable by others", fi.Mode().Perm())
	}
}
