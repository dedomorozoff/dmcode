package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/settings"
)

// TestASwitchedModelIsWrittenToSettings drives the seam the model switch reports
// through. A settings.json that names the new model is what "the model I picked
// is the model I get back" means after the migration from .env.
func TestASwitchedModelIsWrittenToSettings(t *testing.T) {
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))

	m := &uiModel{prov: config.Provider{BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-x"}}
	m.persistModelChoice("deepseek/deepseek-chat-v3.1:free")

	s, err := settings.Load()
	if err != nil {
		t.Fatalf("settings.Load: %v", err)
	}
	if s.Model != "deepseek/deepseek-chat-v3.1:free" {
		t.Errorf("settings.Model = %q, want %q", s.Model, "deepseek/deepseek-chat-v3.1:free")
	}
	if os.Getenv(config.EnvModelKey) != "deepseek/deepseek-chat-v3.1:free" {
		t.Error("the running process disagrees with settings.json it just wrote")
	}
	if m.statusText == "" {
		t.Error("nothing was said about where the model went")
	}
}

// TestASwitchedModelDoesNotTouchDotEnv ensures credentials in .env are never
// modified when a model is switched — model preference now lives in settings.json.
func TestASwitchedModelDoesNotTouchDotEnv(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))

	m := &uiModel{prov: config.Provider{BaseURL: "https://openrouter.ai/api/v1"}}
	m.persistModelChoice("deepseek/deepseek-chat-v3.1:free")

	if _, err := os.Stat(".env"); !os.IsNotExist(err) {
		t.Error("persistModelChoice created .env; it must only write settings.json")
	}
}
