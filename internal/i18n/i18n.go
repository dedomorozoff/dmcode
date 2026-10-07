// Package i18n holds dmCode's user-facing strings.
//
// English is the source language: every call site passes the English text as
// the lookup key, so a missing translation is a no-op rather than a blank
// label. A Russian catalog sits alongside it, and DMCODE_LANG=ru (or the
// /lang command) selects it.
package i18n

import (
	"os"
	"strings"
	"sync"

	"github.com/dedomorozoff/dmcode/internal/settings"
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
	return settings.Update(func(s *settings.S) {
		s.Lang = string(l)
	})
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
	if s, err := settings.Load(); err == nil && s.Lang != "" {
		if l, ok := Parse(s.Lang); ok {
			mu.Lock()
			current = l
			mu.Unlock()
		}
	}
}
