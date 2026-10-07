package config

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all editor configuration.
type Config struct {
	Editor  EditorConfig
	UI      UIConfig
	Plugins PluginsConfig
	LSP     LSPConfig
}

// LSPConfig holds language-server integration settings.
//
// The master switch (Enabled) disables every language server at once; the
// Disabled map turns individual languages off by their LSP language id
// ("go", "python", "typescript", ...). Servers that are not installed on
// PATH are skipped regardless, so these are purely opt-out toggles.
type LSPConfig struct {
	Enabled  bool            // master switch; default true = LSP on everywhere
	Disabled map[string]bool // language id => LSP switched off for it
}

// EditorConfig holds editor-related settings.
type EditorConfig struct {
	TabWidth    int
	SyntaxTheme string
	LineNumbers bool
	WordWrap    bool
	SkippedDirs []string
}

// UIConfig holds UI-related settings.
type UIConfig struct {
	TreeWidth int
	Lang      string
	// Ascii controls glyph rendering on terminals that cannot display the
	// Unicode UI safely: "auto" (default) detects, "on" forces ASCII,
	// "off" forces Unicode.
	Ascii string
}

// PluginsConfig configures the remote plugin store. Plugins are listed and
// installed from a GitHub repo's plugins/ directory.
type PluginsConfig struct {
	Repo   string // "owner/repo"
	Dir    string // path inside the repo holding .lua plugins
	Branch string // branch to read from
}

// Defaults returns the default configuration.
func Defaults() Config {
	return Config{
		Editor: EditorConfig{
			TabWidth:    4,
			SyntaxTheme: "monokai",
			LineNumbers: true,
			WordWrap:    false,
			SkippedDirs: []string{".git", "node_modules"},
		},
		UI: UIConfig{
			TreeWidth: 25,
			Lang:      "en",
			Ascii:     "auto",
		},
		Plugins: PluginsConfig{
			Repo:   "dedomorozoff/dmed",
			Dir:    "plugins",
			Branch: "main",
		},
		LSP: LSPConfig{
			Enabled:  true,
			Disabled: map[string]bool{},
		},
	}
}

// Load reads configuration from disk and applies environment variable overrides.
// Priority: defaults < global config < project config < env vars.
//
// The legacy dmed names (~/.dmed.conf, .dmed.conf) are honored when the dmcode
// file is absent, so an existing setup survives the merge without a rewrite.
func Load(projectRoot string) Config {
	cfg := Defaults()

	// Load global config
	if home, err := os.UserHomeDir(); err == nil {
		globalPath := filepath.Join(home, ".dmcode", "editor.conf")
		if _, err := os.Stat(globalPath); err != nil {
			globalPath = filepath.Join(home, ".dmed.conf")
		}
		loadFile(globalPath, &cfg)
	}

	// Load project config (overrides global)
	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".dmcode.conf")
		if _, err := os.Stat(projectPath); err != nil {
			projectPath = filepath.Join(projectRoot, ".dmed.conf")
		}
		loadFile(projectPath, &cfg)
	}

	// Environment variable overrides. The DMCODE_ names are the merged
	// project's; the DMED_ ones are the dmed spellings, still honored.
	// DMCODE_SHELL/DMED_SHELL is deliberately not read here: Shell is not part
	// of Config, it is stored separately in the editor, which owns that override.
	if v := firstEnv("DMCODE_LANG", "DMED_LANG"); v != "" {
		cfg.UI.Lang = v
	}
	if v := firstEnv("DMCODE_PLUGIN_REPO", "DMED_PLUGIN_REPO"); v != "" {
		cfg.Plugins.Repo = v
	}

	return cfg
}

// firstEnv returns the value of the first set variable, or the empty string.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// WriteLang sets the `[ui] lang` value in the INI file at path, preserving all
// other content. If the file or the [ui] section is missing it is appended.
func WriteLang(path, lang string) error {
	data, err := os.ReadFile(path)
	var lines []string
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}

	var out []string
	inUI := false
	uiPresent := false
	wrote := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			inUI = strings.EqualFold(name, "ui")
			if inUI {
				uiPresent = true
			}
		}
		if inUI {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				if strings.ToLower(strings.TrimSpace(line[:idx])) == "lang" {
					out = append(out, "lang = "+lang)
					wrote = true
					continue
				}
			}
		}
		out = append(out, line)
	}

	if !wrote {
		if !uiPresent {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, "[ui]")
		}
		out = append(out, "lang = "+lang)
	}

	content := strings.Join(out, "\n") + "\n"
	if len(content) == 1 {
		content = ""
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ConfigPath returns the path to the global config file, under dmcode's own
// directory.
func ConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".dmcode", "editor.conf")
	}
	return "editor.conf"
}

// ProjectConfigPath returns the path to the project-level config file.
func ProjectConfigPath(root string) string {
	if root != "" {
		return filepath.Join(root, ".dmcode.conf")
	}
	return ""
}

func loadFile(path string, cfg *Config) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sections := parseINI(f)

	// [editor]
	if s, ok := sections["editor"]; ok {
		if v, ok := s["tab_width"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Editor.TabWidth = n
			}
		}
		if v, ok := s["syntax_theme"]; ok {
			cfg.Editor.SyntaxTheme = v
		}
		if v, ok := s["line_numbers"]; ok {
			cfg.Editor.LineNumbers = parseBool(v)
		}
		if v, ok := s["word_wrap"]; ok {
			cfg.Editor.WordWrap = parseBool(v)
		}
		if v, ok := s["skipped_dirs"]; ok {
			cfg.Editor.SkippedDirs = parseList(v)
		}
	}

	// [ui]
	if s, ok := sections["ui"]; ok {
		if v, ok := s["tree_width"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.UI.TreeWidth = n
			}
		}
		if v, ok := s["lang"]; ok {
			cfg.UI.Lang = v
		}
		if v, ok := s["ascii"]; ok {
			switch strings.ToLower(v) {
			case "on", "1", "true", "yes":
				cfg.UI.Ascii = "on"
			case "off", "0", "false", "no":
				cfg.UI.Ascii = "off"
			case "auto":
				cfg.UI.Ascii = "auto"
			}
		}
	}

	// [plugins]
	if s, ok := sections["plugins"]; ok {
		if v, ok := s["repo"]; ok {
			cfg.Plugins.Repo = v
		}
		if v, ok := s["dir"]; ok {
			cfg.Plugins.Dir = v
		}
		if v, ok := s["branch"]; ok {
			cfg.Plugins.Branch = v
		}
	}

	// [lsp] — master switch plus per-language opt-out toggles.
	if s, ok := sections["lsp"]; ok {
		if v, ok := s["enabled"]; ok {
			cfg.LSP.Enabled = parseBool(v)
		}
		if cfg.LSP.Disabled == nil {
			cfg.LSP.Disabled = map[string]bool{}
		}
		for k, v := range s {
			if k == "enabled" {
				continue
			}
			if parseBool(v) {
				cfg.LSP.Disabled[k] = true
			}
		}
	}
}

// parseINI reads an INI file and returns section -> key -> value.
func parseINI(r io.Reader) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	current := ""

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		// Section header
		if line[0] == '[' && line[len(line)-1] == ']' {
			current = strings.ToLower(line[1 : len(line)-1])
			if _, ok := sections[current]; !ok {
				sections[current] = make(map[string]string)
			}
			continue
		}
		// Key = value
		if idx := strings.IndexByte(line, '='); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			// Strip quotes
			if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
			if current == "" {
				current = "_"
				sections[current] = make(map[string]string)
			}
			sections[current][strings.ToLower(key)] = val
		}
	}
	return sections
}

func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "true" || s == "yes" || s == "1"
}

// parseList splits a comma separated config value into trimmed, non-empty
// entries (used for skipped_dirs, tools_enabled, tools_disabled).
func parseList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeSection(path, section string, known [][2]string) (int, error) {
	data, err := os.ReadFile(path)
	var lines []string
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}

	var out []string
	inSec := false
	secPresent := false
	replaced := make(map[string]bool, len(known))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			inSec = strings.EqualFold(name, section)
			if inSec {
				secPresent = true
			}
		}
		if inSec {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				key := strings.ToLower(strings.TrimSpace(line[:idx]))
				matched := false
				for _, k := range known {
					if k[0] == key {
						out = append(out, key+" = "+k[1])
						replaced[key] = true
						matched = true
						break
					}
				}
				if matched {
					continue
				}
			}
		}
		out = append(out, line)
	}

	var missing []string
	for _, k := range known {
		if !replaced[k[0]] {
			missing = append(missing, k[0]+" = "+k[1])
		}
	}
	if !secPresent {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "["+section+"]")
		out = append(out, missing...)
	} else if len(missing) > 0 {
		for i := len(out) - 1; i >= 0; i-- {
			t := strings.TrimSpace(out[i])
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") &&
				strings.EqualFold(strings.TrimSpace(t[1:len(t)-1]), section) {
				tail := append([]string{}, out[i+1:]...)
				out = append(append(out[:i+1], missing...), tail...)
				break
			}
		}
	}

	content := strings.Join(out, "\n") + "\n"
	if len(content) == 1 {
		content = ""
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return 0, err
	}
	return len(replaced) + len(missing), nil
}

// WriteLSP merges the LSP settings into the INI file at path, updating the
// [lsp] section in place. Every known language id is written so a toggled-off
// server stays off across edits; unknown ids already present are kept.
func WriteLSP(path string, l LSPConfig) (int, error) {
	if l.Disabled == nil {
		l.Disabled = map[string]bool{}
	}
	known := make([][2]string, 0, len(LSPLanguages)+1)
	known = append(known, [2]string{"enabled", boolStr(l.Enabled)})
	seen := map[string]bool{"enabled": true}
	for _, lang := range LSPLanguages {
		val := "false"
		if l.Disabled[lang] {
			val = "true"
		}
		known = append(known, [2]string{lang, val})
		seen[lang] = true
	}
	// Preserve any user-added unknown language keys in the written state.
	for k, v := range l.Disabled {
		if !seen[k] {
			known = append(known, [2]string{k, boolStr(v)})
			seen[k] = true
		}
	}
	return writeSection(path, "lsp", known)
}

// LSPLanguages lists every language id the editor knows how to power through
// a language server, in a stable display order. Used by the [lsp] config
// writer and the LSP settings wizard.
var LSPLanguages = []string{
	"go", "python", "typescript", "rust", "c", "cpp", "lua",
	"ruby", "php", "json", "yaml", "css", "html", "zig",
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
