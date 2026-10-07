package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Editor.WordWrap {
		t.Error("word_wrap should default to false")
	}
	if cfg.Editor.TabWidth != 4 {
		t.Errorf("tab_width = %d, want 4", cfg.Editor.TabWidth)
	}
	if cfg.Editor.SyntaxTheme != "monokai" {
		t.Errorf("syntax_theme = %q, want monokai", cfg.Editor.SyntaxTheme)
	}
	if !cfg.Editor.LineNumbers {
		t.Error("line_numbers should default to true")
	}
	if cfg.UI.TreeWidth != 25 {
		t.Errorf("tree_width = %d, want 25", cfg.UI.TreeWidth)
	}
	if cfg.UI.Lang != "en" {
		t.Errorf("lang = %q, want en", cfg.UI.Lang)
	}
}

func TestParseINI(t *testing.T) {
	input := `[editor]
tab_width = 2
syntax_theme = dracula
line_numbers = false
word_wrap = true
skipped_dirs = .git,node_modules,vendor

[ui]
tree_width = 30
lang = ru
ascii = on
`
	sections := parseINI(strings.NewReader(input))

	if sections["editor"]["tab_width"] != "2" {
		t.Errorf("editor.tab_width = %q, want 2", sections["editor"]["tab_width"])
	}
	if sections["editor"]["word_wrap"] != "true" {
		t.Errorf("editor.word_wrap = %q, want true", sections["editor"]["word_wrap"])
	}
	if sections["editor"]["syntax_theme"] != "dracula" {
		t.Errorf("editor.syntax_theme = %q, want dracula", sections["editor"]["syntax_theme"])
	}
	if sections["ui"]["tree_width"] != "30" {
		t.Errorf("ui.tree_width = %q", sections["ui"]["tree_width"])
	}
	if sections["ui"]["lang"] != "ru" {
		t.Errorf("ui.lang = %q, want ru", sections["ui"]["lang"])
	}
	if sections["ui"]["ascii"] != "on" {
		t.Errorf("ui.ascii = %q, want on", sections["ui"]["ascii"])
	}
}

func TestParseINIComments(t *testing.T) {
	input := `# this is a comment
; so is this
[editor]
tab_width = 8
`
	sections := parseINI(strings.NewReader(input))
	if sections["editor"]["tab_width"] != "8" {
		t.Errorf("tab_width = %q, want 8", sections["editor"]["tab_width"])
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmcode.conf")
	content := `[editor]
tab_width = 2
syntax_theme = dracula
word_wrap = true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Editor.TabWidth != 2 {
		t.Errorf("tab_width = %d, want 2", cfg.Editor.TabWidth)
	}
	if !cfg.Editor.WordWrap {
		t.Errorf("word_wrap = %v, want true", cfg.Editor.WordWrap)
	}
	if cfg.Editor.SyntaxTheme != "dracula" {
		t.Errorf("syntax_theme = %q, want dracula", cfg.Editor.SyntaxTheme)
	}
	// Defaults should be preserved for unset values
	if cfg.UI.TreeWidth != 25 {
		t.Errorf("tree_width = %d, want 25 (default)", cfg.UI.TreeWidth)
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, ".dmcode.conf")
	os.WriteFile(globalPath, []byte("[editor]\ntab_width = 2\n"), 0o644)

	projectDir := t.TempDir()
	projectPath := filepath.Join(projectDir, ".dmcode.conf")
	os.WriteFile(projectPath, []byte("[editor]\ntab_width = 8\n"), 0o644)

	cfg := Defaults()
	loadFile(globalPath, &cfg)
	loadFile(projectPath, &cfg)

	if cfg.Editor.TabWidth != 8 {
		t.Errorf("tab_width = %d, want 8 (project overrides global)", cfg.Editor.TabWidth)
	}
}

func TestPluginsDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Plugins.Repo != "dedomorozoff/dmed" || cfg.Plugins.Dir != "plugins" || cfg.Plugins.Branch != "main" {
		t.Errorf("default plugins = %+v", cfg.Plugins)
	}
}

func TestLoadPluginsSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmcode.conf")
	content := "[plugins]\nrepo = someone/else\ndir = lua\nbranch = dev\n"
	os.WriteFile(path, []byte(content), 0o644)

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Plugins.Repo != "someone/else" || cfg.Plugins.Dir != "lua" || cfg.Plugins.Branch != "dev" {
		t.Errorf("plugins = %+v", cfg.Plugins)
	}
}

func TestLoadASCIISetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmcode.conf")
	if err := os.WriteFile(path, []byte("[ui]\nascii = off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.UI.Ascii != "off" {
		t.Fatalf("ascii = %q, want off", cfg.UI.Ascii)
	}
	if err := os.WriteFile(path, []byte("[ui]\nascii = bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = Load(dir)
	if cfg.UI.Ascii != "auto" {
		t.Fatalf("ascii = %q, want default auto for invalid value", cfg.UI.Ascii)
	}
}

func TestWriteLang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmcode.conf")
	if err := os.WriteFile(path, []byte("[editor]\ntab_width = 2\n[ui]\ntree_width = 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteLang(path, "ru"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "lang = ru") {
		t.Errorf("lang not set:\n%s", s)
	}
	if !strings.Contains(s, "tab_width = 2") || !strings.Contains(s, "tree_width = 30") {
		t.Errorf("existing content clobbered:\n%s", s)
	}

	// Updating the value must not duplicate the key.
	if err := WriteLang(path, "en"); err != nil {
		t.Fatal(err)
	}
	s2, _ := os.ReadFile(path)
	if strings.Count(string(s2), "lang =") != 1 {
		t.Errorf("lang key duplicated:\n%s", s2)
	}
}

func TestWriteLangCreatesSectionInEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmcode.conf")
	if err := WriteLang(path, "ru"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "[ui]") || !strings.Contains(s, "lang = ru") {
		t.Errorf("missing [ui] lang:\n%s", s)
	}
}
