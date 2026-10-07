package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// TestASwitchedModelIsWrittenToDotEnv drives the seam the model switch reports
// through, because that is where "the model I picked is the model I get back"
// is either true or not. A .env that still names the old id is the failure the
// user reports as "the model is not saved": everything works, right up to the
// next start.
func TestASwitchedModelIsWrittenToDotEnv(t *testing.T) {
	chdir(t, t.TempDir())
	if err := os.WriteFile(".env",
		[]byte("# keep me\nOPENAI_BASE_URL=https://openrouter.ai/api/v1\nOPENAI_API_KEY=sk-x\nDMCODE_MODEL=old/model\n"),
		0o600); err != nil {
		t.Fatal(err)
	}

	m := &uiModel{prov: config.Provider{BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-x"}}
	m.persistModelChoice("deepseek/deepseek-chat-v3.1:free")

	b, err := os.ReadFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "DMCODE_MODEL=deepseek/deepseek-chat-v3.1:free") {
		t.Errorf("the picked model did not reach .env:\n%s", got)
	}
	if strings.Contains(got, "old/model") {
		t.Errorf(".env still names the old model:\n%s", got)
	}
	if !strings.Contains(got, "# keep me") {
		t.Errorf("writing the model dropped the user's comment:\n%s", got)
	}
	if os.Getenv(config.EnvModelKey) != "deepseek/deepseek-chat-v3.1:free" {
		t.Error("the running process disagrees with the file it just wrote")
	}
	if m.statusText == "" {
		t.Error("nothing was said about where the model went")
	}
}

// TestASwitchIsNotSavedTwiceOnDisk is the cheap half of the same guarantee: the
// running process is told, so anything that rebuilds a provider from the
// environment later in this session agrees with the file rather than putting the
// old model back.
func TestASwitchIsNotSavedTwiceOnDisk(t *testing.T) {
	chdir(t, t.TempDir())
	if err := os.WriteFile(".env",
		[]byte("OPENAI_BASE_URL=http://127.0.0.1:11434/v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := &uiModel{prov: config.Provider{BaseURL: "http://127.0.0.1:11434/v1"}}
	m.persistModelChoice("qwen2.5-coder:14b")

	b, _ := os.ReadFile(".env")
	if strings.Count(string(b), "DMCODE_MODEL=") != 1 {
		t.Errorf("the model line is not written once:\n%s", b)
	}
}

// TestAModelThatCannotBeSavedSaysSo covers the case a user would otherwise read
// as "saved": an endpoint that .env does not describe. Silence would be a lie,
// and the reason has to name what to do about it.
func TestAModelThatCannotBeSavedSaysSo(t *testing.T) {
	chdir(t, t.TempDir())
	if err := os.WriteFile(".env",
		[]byte("OPENAI_BASE_URL=https://api.groq.com/openai/v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := &uiModel{prov: config.Provider{BaseURL: "https://openrouter.ai/api/v1"}}
	m.persistModelChoice("deepseek/deepseek-chat-v3.1:free")

	if !strings.Contains(m.statusText, "/setup") {
		t.Errorf("the status line does not say what to do about it: %q", m.statusText)
	}
	b, _ := os.ReadFile(".env")
	if strings.Contains(string(b), "DMCODE_MODEL") {
		t.Errorf("a model was written for an endpoint the file does not configure:\n%s", b)
	}
}
