// Package i18n holds dmCode's user-facing strings.
//
// English is the source language: every call site passes the English text as
// the lookup key, so a missing translation is a no-op rather than a blank
// label. A Russian catalog sits alongside it, and DMCODE_LANG=ru (or the
// /lang command) selects it.
package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Lang is a supported interface language.
type Lang string

const (
	// English is the default: an unset DMCODE_LANG stays here.
	English Lang = "en"
	// Russian is selectable at runtime and persists to the settings file.
	Russian Lang = "ru"
)

// Langs lists the selectable languages in menu order.
var Langs = []Lang{English, Russian}

var (
	mu      sync.RWMutex
	current = English
)

// Current reports the active language.
func Current() Lang {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// T translates an English key into the active language. An unknown key, or the
// active language being English, returns the key unchanged, so the interface
// degrades to readable English rather than to an empty string.
func T(key string) string {
	if key == "" {
		return key
	}
	mu.RLock()
	lang := current
	mu.RUnlock()
	if lang == English {
		return key
	}
	if tr, ok := catalog[lang][key]; ok {
		return tr
	}
	return key
}

// Parse maps a user- or env-supplied name onto a Lang. Anything unrecognised
// falls back to English, which is the documented default.
func Parse(s string) (Lang, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "en", "english":
		return English, true
	case "ru", "russian", "русский":
		return Russian, true
	}
	return English, false
}

// Set switches the active language and persists the choice so the next start
// comes up in the same language. It reports whether the file could be written;
// a read-only home directory should not stop the switch from taking effect for
// this session.
func Set(l Lang) error {
	mu.Lock()
	current = l
	mu.Unlock()
	os.Setenv("DMCODE_LANG", string(l))
	return save()
}

// Init picks the language at startup: the DMCODE_LANG environment variable if
// set, otherwise the persisted choice, otherwise English.
func Init() {
	if v := os.Getenv("DMCODE_LANG"); v != "" {
		if l, ok := Parse(v); ok {
			mu.Lock()
			current = l
			mu.Unlock()
			return
		}
	}
	if v, err := read(); err == nil && v != "" {
		if l, ok := Parse(v); ok {
			mu.Lock()
			current = l
			mu.Unlock()
		}
	}
}

// settingsPath is ~/.dmcode/settings.json, next to the prompt history. The
// override exists for tests and for users who keep state beside their project.
func settingsPath() (string, error) {
	if p := os.Getenv("DMCODE_SETTINGS_PATH"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dmcode", "settings.json"), nil
}

// settings is the on-disk shape. It is a struct rather than a bare map so a
// future key can be added without invalidating existing files.
type settings struct {
	Lang string `json:"lang,omitempty"`
}

func read() (string, error) {
	path, err := settingsPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	return s.Lang, nil
}

// save writes the settings file, creating the directory. The write is atomic
// (temp file plus rename) so an interrupted run cannot leave a half-written
// file that fails to parse on the next start.
func save() error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(settings{Lang: string(Current())})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
