package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFollowsOpenCodeSourcesAndPrecedence(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	configHome := t.TempDir()

	write(t, filepath.Join(home, ".claude", "skills", "global", "SKILL.md"), `---
name: Global
description: a global skill
---
global body
`)
	write(t, filepath.Join(root, ".opencode", "skills", "review", "SKILL.md"), `---
name: Review
description: the project review checklist
---
project body
`)
	write(t, filepath.Join(root, ".opencode", "skills", "review", "notes.md"), "supporting notes\n")
	write(t, filepath.Join(root, ".dmcode", "skills", "review", "SKILL.md"), `---
name: Review override
description: the native project review checklist
---
native body
`)
	write(t, filepath.Join(root, ".dmcode", "skills", "review", "notes.md"), "supporting notes\n")
	write(t, filepath.Join(root, ".opencode", "skills", "flat.md"), `---
description: a flat skill
---
flat body
`)
	write(t, filepath.Join(root, ".opencode", "skills", "hidden", "SKILL.md"), `---
name: Hidden
description: only loaded by id
metadata:
  opencode/autoinvoke: false
---
hidden body
`)
	write(t, filepath.Join(root, ".opencode", "skills", "manual", "SKILL.md"), `---
name: Manual
description: also only loaded by id
disable-model-invocation: true
---
manual body
`)
	write(t, filepath.Join(root, ".opencode", "skills", "broken", "SKILL.md"), `---
description: [unterminated
---
broken body
`)

	c := DiscoverIn(root, home, configHome)

	for _, id := range []string{"global", "review", "flat", "hidden", "manual"} {
		if _, ok := c.ByID(id); !ok {
			t.Errorf("skill %q was not discovered; got %v", id, c.IDs())
		}
	}
	if _, ok := c.ByID("broken"); ok {
		t.Error("a malformed skill was loaded")
	}
	if len(c.Notes) == 0 {
		t.Error("a malformed skill was not reported")
	}

	review, _ := c.ByID("review")
	if review.Description != "the native project review checklist" || review.Body != "native body" {
		t.Errorf("review was not overridden by the higher-precedence source: %+v", review)
	}
	if !review.Nested {
		t.Error("a directory-form skill is not marked nested")
	}
	if len(review.Files) != 1 || review.Files[0] != "notes.md" {
		t.Errorf("supporting files = %v, want [notes.md]", review.Files)
	}

	adv := c.Advertised()
	ids := map[string]bool{}
	for _, s := range adv {
		ids[s.ID] = true
	}
	if !ids["review"] || !ids["global"] || !ids["flat"] {
		t.Errorf("advertised skills = %v, want review/global/flat", ids)
	}
	if ids["hidden"] || ids["manual"] {
		t.Errorf("a skill that opted out was advertised: %v", ids)
	}

	body, err := c.Load("review", "")
	if err != nil || !strings.Contains(body, "native body") {
		t.Fatalf("Load review = %q, %v", body, err)
	}
	file, err := c.Load("review", "notes.md")
	if err != nil || !strings.Contains(file, "supporting notes") {
		t.Fatalf("Load supporting file = %q, %v", file, err)
	}
	if _, err := c.Load("review", "../secret"); err == nil {
		t.Error("a supporting file escaped the skill directory")
	}
	if _, err := c.Load("missing", ""); err == nil {
		t.Error("an unknown skill id was accepted")
	}

	tool, err := MakeTool(c)
	if err != nil {
		t.Fatal(err)
	}
	desc := tool.Description()
	if !strings.Contains(desc, "review") || !strings.Contains(desc, "global") {
		t.Errorf("tool description does not advertise the visible skills: %q", desc)
	}
	if strings.Contains(desc, "hidden") || strings.Contains(desc, "manual") {
		t.Errorf("tool description advertises an opted-out skill: %q", desc)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		fm   string
		body string
	}{
		{"normal", "---\nname: x\n---\nbody\n", "name: x", "body\n"},
		{"empty", "---\n---\nbody\n", "", "body\n"},
		{"none", "body only\n", "", "body only\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm, body := splitFrontmatter(tc.in)
			if fm != tc.fm || body != tc.body {
				t.Errorf("splitFrontmatter(%q) = %q, %q; want %q, %q", tc.in, fm, body, tc.fm, tc.body)
			}
		})
	}
}
