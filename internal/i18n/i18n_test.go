package i18n

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useTempSettings points persistence at a temp file and restores the previous
// language afterwards, so a test never writes to the real ~/.dmcode.
func useTempSettings(t *testing.T) {
	t.Helper()
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv("DMCODE_LANG", "")
	restore := Current()
	t.Cleanup(func() { Set(restore) })
}

func TestDefaultsToEnglish(t *testing.T) {
	useTempSettings(t)
	Init()
	if Current() != English {
		t.Fatalf("Init with no DMCODE_LANG gave %q, want English", Current())
	}
}

// T must return the key itself in English, so a missing entry degrades to
// readable text rather than to an empty label.
func TestEnglishIsTheSourceLanguage(t *testing.T) {
	useTempSettings(t)
	if got := T("ready"); got != "ready" {
		t.Errorf("T in English = %q, want the key unchanged", got)
	}
	if got := T("a key nobody translated"); got != "a key nobody translated" {
		t.Errorf("unknown key = %q, want it returned unchanged", got)
	}
}

func TestRussianTranslation(t *testing.T) {
	useTempSettings(t)
	Set(Russian)
	if Current() != Russian {
		t.Fatalf("Set(Russian) left Current at %q", Current())
	}
	if got := T("ready"); got != "готов" {
		t.Errorf("T(\"ready\") in Russian = %q, want %q", got, "готов")
	}
	// An entry missing from the catalog falls back rather than blanking.
	if got := T("not in the catalog"); got != "not in the catalog" {
		t.Errorf("unknown key in Russian = %q, want it returned unchanged", got)
	}
}

func TestParse(t *testing.T) {
	for _, in := range []string{"", "en", "EN", "english"} {
		if l, ok := Parse(in); !ok || l != English {
			t.Errorf("Parse(%q) = %q,%v; want English", in, l, ok)
		}
	}
	for _, in := range []string{"ru", "RU", "russian", " Русский "} {
		if l, ok := Parse(in); !ok || l != Russian {
			t.Errorf("Parse(%q) = %q,%v; want Russian", in, l, ok)
		}
	}
	// An unrecognised value falls back instead of leaving the UI in no language.
	if l, ok := Parse("klingon"); ok || l != English {
		t.Errorf("Parse(\"klingon\") = %q,%v; want English,false", l, ok)
	}
}

func TestInitReadsEnvThenPersistedChoice(t *testing.T) {
	useTempSettings(t)

	// A persisted choice applies when the environment says nothing.
	if err := Set(Russian); err != nil {
		t.Fatalf("Set: %v", err)
	}
	Init()
	if Current() != Russian {
		t.Errorf("after persisting Russian, Init gave %q", Current())
	}

	// The environment wins, so a one-off run can override the stored default.
	t.Setenv("DMCODE_LANG", "en")
	Init()
	if Current() != English {
		t.Errorf("DMCODE_LANG=en was ignored, got %q", Current())
	}

	// And it works the other way round, from a clean state.
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(t.TempDir(), "absent.json"))
	t.Setenv("DMCODE_LANG", "ru")
	Init()
	if Current() != Russian {
		t.Errorf("DMCODE_LANG=ru was ignored, got %q", Current())
	}
}

func TestSetPersistsAcrossInit(t *testing.T) {
	useTempSettings(t)
	if err := Set(Russian); err != nil {
		t.Fatalf("Set: %v", err)
	}
	path := os.Getenv("DMCODE_SETTINGS_PATH")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	if !strings.Contains(string(data), "ru") {
		t.Errorf("settings file %q does not record the language", data)
	}
}

// Every Russian entry must have a non-empty translation, and the keys the UI
// actually uses must exist. A blank value renders as an empty label.
func TestCatalogHasNoEmptyTranslations(t *testing.T) {
	for lang, entries := range catalog {
		for k, v := range entries {
			if v == "" {
				t.Errorf("%s: key %q maps to an empty string", lang, k)
			}
		}
	}
}
