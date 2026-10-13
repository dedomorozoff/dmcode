// Package settings owns ~/.dmcode/settings.json — the global user preferences
// that survive across workspaces and restarts.
//
// What belongs here vs. .env:
//   - .env holds provider credentials and endpoint config — per-project secrets.
//   - settings.json holds user preferences that are independent of any project:
//     the interface language, the proxy, and the chosen model.
//
// The write is atomic (temp file + rename) and the directory is created on
// demand, so a missing ~/.dmcode never prevents a save.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// S is the on-disk shape. Every field has omitempty so an unset field is not
// written, which keeps the file readable and allows future fields to be added
// without invalidating existing ones.
type S struct {
	Lang        string `json:"lang,omitempty"`
	Proxy       string `json:"proxy,omitempty"`
	NoProxy     string `json:"no_proxy,omitempty"`
	Model       string `json:"model,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	APIKey      string `json:"api_key,omitempty"`
	APIKeyVar   string `json:"api_key_var,omitempty"`
	API         string `json:"api,omitempty"`
	Reasoning   string `json:"reasoning,omitempty"`
	GGUFPath    string `json:"gguf_path,omitempty"`
	LlamaServer string `json:"llama_server,omitempty"`
	LlamaArgs   string `json:"llama_args,omitempty"`
}

var (
	mu      sync.Mutex
	cached  *S
	pathEnv = "DMCODE_SETTINGS_PATH"
)

// Path returns ~/.dmcode/settings.json, or the path from DMCODE_SETTINGS_PATH
// when that is set (used by tests and users who keep state beside a project).
func Path() (string, error) {
	if p := os.Getenv(pathEnv); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dmcode", "settings.json"), nil
}

// Load reads the settings file and caches the result. Missing file is not an
// error — it just means a fresh install with no saved preferences.
func Load() (S, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

// load is the unsynchronised read, called with mu held.
func load() (S, error) {
	path, err := Path()
	if err != nil {
		return S{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return S{}, nil
		}
		return S{}, err
	}
	var s S
	if err := json.Unmarshal(data, &s); err != nil {
		return S{}, err
	}
	return s, nil
}

// Save writes s to disk atomically and clears the in-memory cache so the next
// Load reads the freshly written file.
func Save(s S) error {
	mu.Lock()
	defer mu.Unlock()
	cached = nil
	return save(s)
}

// save is the unsynchronised write, called with mu held.
func save(s S) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
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

// Update loads the current settings, applies fn, and saves the result.
// It is the safe way to change one field without clobbering the others.
func Update(fn func(*S)) error {
	mu.Lock()
	defer mu.Unlock()
	s, err := load()
	if err != nil {
		return err
	}
	fn(&s)
	cached = nil
	return save(s)
}
