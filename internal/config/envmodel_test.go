package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dedomorozoff/dmcode/internal/settings"
)

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
// that decides that is the model field in settings.json.
func TestSaveModelReachesTheNextStart(t *testing.T) {
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))

	p := Provider{BaseURL: "https://openrouter.ai/api/v1", Model: "old/model"}
	if err := SaveModel(p, "deepseek/deepseek-chat-v3.1:free"); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}

	s, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if s.Model != "deepseek/deepseek-chat-v3.1:free" {
		t.Errorf("settings.Model = %q, want %q", s.Model, "deepseek/deepseek-chat-v3.1:free")
	}
}

// TestSaveModelReplacesAPreviousChoice verifies that saving a second model
// replaces the first rather than accumulating.
func TestSaveModelReplacesAPreviousChoice(t *testing.T) {
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))

	p := Provider{BaseURL: "https://openrouter.ai/api/v1", Model: "old/model"}
	if err := SaveModel(p, "old/model"); err != nil {
		t.Fatalf("SaveModel(old): %v", err)
	}
	if err := SaveModel(p, "new/model"); err != nil {
		t.Fatalf("SaveModel(new): %v", err)
	}

	s, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if s.Model != "new/model" {
		t.Errorf("settings.Model = %q, want %q", s.Model, "new/model")
	}
}

// TestLoadSettingsAppliesModelFromSettings verifies that LoadSettings sets
// DMCODE_MODEL from settings.json, making the saved model the one the next
// start uses.
func TestLoadSettingsAppliesModelFromSettings(t *testing.T) {
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv(EnvModelKey, "")

	p := Provider{BaseURL: "https://openrouter.ai/api/v1", Model: "old/model"}
	if err := SaveModel(p, "qwen/qwen3-32b"); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	LoadSettings()

	if got := os.Getenv(EnvModelKey); got != "qwen/qwen3-32b" {
		t.Errorf("DMCODE_MODEL = %q after LoadSettings, want %q", got, "qwen/qwen3-32b")
	}
}

// TestWriteDotEnvIsAtomicAndPrivate: the file holds a provider key, and a write
// interrupted half way would leave a session with no configuration at all.
func TestWriteDotEnvIsAtomicAndPrivate(t *testing.T) {
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	writeEnv(t, "OLD=1\n")

	if err := WriteDotEnv([]string{"NEW=2", "# kept"}); err != nil {
		t.Fatal(err)
	}
	if got := readEnv(t); !strings.Contains(got, "NEW=2") || !strings.Contains(got, "# kept") {
		t.Errorf("the file was not replaced as asked:\n%s", got)
	}
	if _, err := os.Stat(".env.tmp"); !os.IsNotExist(err) {
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
