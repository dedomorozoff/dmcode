package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// mapFixture builds a small project on disk and points the boundary at it.
func mapFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("go.mod", "module example\n")
	mk("main.go", "package main\n")
	mk("README.md", "# example\n")
	mk("AGENTS.md", "rules\n")
	mk("internal/ui/ui.go", "package ui\n")
	mk("internal/ui/render.go", "package ui\n")
	mk("internal/ui/panels/panel.go", "package panels\n")
	mk("internal/ui/rope/rope.go", "package rope\n")
	mk("internal/tools/tools.go", "package tools\n")
	mk("web/index.php", "<?php\n")
	mk("node_modules/left-pad/index.js", "module.exports=1\n")
	mk(".git/config", "[core]\n")
	withRoot(t, root)
	return root
}

// hasLine reports whether the map has a line whose trimmed content is exactly
// want. The tree prints a directory's own name on its own line, so "ui/" is a
// substring of "internal/ui/" only by accident — testing the text of a line is
// what makes this a check about the map rather than about Go's strings package.
func hasLine(m, want string) bool {
	for _, line := range strings.Split(m, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// The point of the tool is the shape of the tree, so the test looks at the shape
// rather than at the fact that it returned something.
func TestTheMapShowsTheTreeAndNamesTheMarkers(t *testing.T) {
	mapFixture(t)
	// Depth 3 is what it takes to see a file inside internal/ui, and the depth
	// argument is where the off-by-one lives: root counts as level 1, so a
	// directory at level 3 needs a depth of 3 to be opened rather than named.
	res, err := projectMap(nil, projectMapArgs{Depth: 3})
	if err != nil {
		t.Fatalf("projectMap: %v", err)
	}

	if !hasLine(res.Map, "ui/") {
		t.Errorf("the map does not descend into internal/ui:\n%s", res.Map)
	}
	if !hasLine(res.Map, "ui.go") {
		t.Errorf("the map does not name a file it opened:\n%s", res.Map)
	}
	// Indentation is what makes the tree a tree. A map whose lines are all flush
	// left is a list, and the model reads it as one.
	for _, line := range strings.Split(res.Map, "\n") {
		if strings.TrimSpace(line) == "ui.go" && !strings.HasPrefix(line, "      ") {
			t.Errorf("a nested file is not indented under its directory: %q\n%s", line, res.Map)
		}
	}
	// The build file is the line that says what this is. It was once cut by the
	// per-directory display cap, because counting and displaying were one loop —
	// go.mod is the eleventh entry alphabetically.
	if !strings.Contains(res.Markers, "go.mod") {
		t.Errorf("the map went past go.mod without naming it: %q", res.Markers)
	}
	if !strings.Contains(res.Markers, "AGENTS.md") {
		t.Errorf("the map did not report the project's own manual: %q", res.Markers)
	}
	// Noise directories are not the project's structure.
	for _, noise := range []string{"node_modules", ".git/"} {
		if strings.Contains(res.Map, noise) {
			t.Errorf("the map shows %s:\n%s", noise, res.Map)
		}
	}
}

// The language share covers the whole tree even when the tree shown does not,
// because the share of a depth-limited walk is a share of the repository's
// README files. Getting this wrong is what made dmcode's own project report
// "go 44%" — computed from main.go and two lockfiles.
func TestTheLanguageShareCoversTheWholeTreeNotTheRenderedOne(t *testing.T) {
	root := mapFixture(t)
	// Twelve Go files below the level the default map renders.
	for i := 0; i < 12; i++ {
		p := filepath.Join(root, "internal", "deep", "nested", "pkg", "f"+string(rune('a'+i))+".go")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := projectMap(nil, projectMapArgs{})
	if err != nil {
		t.Fatal(err)
	}
	// The default depth stops above pkg/, so none of those twelve are drawn.
	if strings.Contains(res.Map, "pkg/") {
		t.Fatalf("the fixture was visible at the default depth, so this test proves nothing:\n%s", res.Map)
	}
	if !strings.HasPrefix(res.Languages, "go ") {
		t.Errorf("languages = %q, want go to dominate off the whole tree", res.Languages)
	}
	if !strings.Contains(res.Languages, "%") {
		t.Errorf("languages = %q carries no share", res.Languages)
	}
}

// One file is not a language of the project. dmcode's own survey came out as
// "go 98%, lua 0%, php 0%" until this was caught, and a reader takes a 0% line
// as a claim about the stack.
func TestAOneFileLanguageIsNotReportedAsOne(t *testing.T) {
	mapFixture(t)
	res, err := projectMap(nil, projectMapArgs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(res.Languages, ",") {
		part = strings.TrimSpace(part)
		if strings.HasSuffix(part, "0%") && !strings.HasSuffix(part, " 0%") {
			continue
		}
		if strings.HasSuffix(part, " 0%") {
			t.Errorf("languages = %q reports %q, which is a single file", res.Languages, part)
		}
	}
}

// The depth argument is the model's to set, and an out-of-range one has to be
// answered rather than refused: depth 0 means the default, and a depth past the
// cap is capped, not an error. What has to hold at every depth is that asking for
// more never shows less.
func TestDepthIsClampedAndDefaults(t *testing.T) {
	mapFixture(t)
	ui := filepath.Join(Root(), "internal", "ui")

	one, err := projectMap(nil, projectMapArgs{Path: ui, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one.Map, "ui.go") {
		t.Errorf("depth 1 did not show the root's own files:\n%s", one.Map)
	}
	if strings.Contains(one.Map, "panel.go") {
		t.Errorf("depth 1 opened a subdirectory:\n%s", one.Map)
	}
	// It still names them: a directory whose subdirectories are invisible at the
	// depth limit would look empty, which is a different wrong answer.
	if !strings.Contains(one.Map, "panels/") {
		t.Errorf("depth 1 hid the subdirectories instead of naming them:\n%s", one.Map)
	}

	// 99 is past maxMapDepth and must be clamped, not refused and not taken
	// literally.
	huge, err := projectMap(nil, projectMapArgs{Path: ui, Depth: 99})
	if err != nil {
		t.Fatalf("depth 99 was refused rather than clamped: %v", err)
	}
	if !strings.Contains(huge.Map, "panels/") {
		t.Errorf("a clamped depth did not descend:\n%s", huge.Map)
	}

	// 0 is the default, and it must agree with the default rather than showing
	// nothing: the bare call is the one the tool is built for.
	zero, err := projectMap(nil, projectMapArgs{Path: ui})
	if err != nil {
		t.Fatal(err)
	}
	def, err := projectMap(nil, projectMapArgs{Path: ui, Depth: defaultMapDepth})
	if err != nil {
		t.Fatal(err)
	}
	if zero.Map != def.Map {
		t.Errorf("depth 0 is not the default:\n%s\n---\n%s", zero.Map, def.Map)
	}
}

// A map that hit a bound says so. The alternative is a count that reads as a
// measurement of the whole project and is not one — and a model that trusts it
// concludes the repository has four directories.
func TestABoundedMapSaysItStopped(t *testing.T) {
	root := mapFixture(t)
	for i := 0; i <= maxMapDirs; i++ {
		if err := os.MkdirAll(filepath.Join(root, "pkg", string(rune('a'+i/26)), string(rune('a'+i%26))), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	res, err := projectMap(nil, projectMapArgs{Depth: maxMapDepth})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Errorf("a map that stopped at a bound does not say so: dirs=%d files=%d", res.Dirs, res.Files)
	}
	if !strings.Contains(res.Note, "directories") {
		t.Errorf("the note does not say which bound was hit: %q", res.Note)
	}
}

// A directory of a hundred files is a listing, not orientation. The map shows a
// page and names the rest, because a count that silently drops files is a claim
// that they are not there.
func TestALongDirectoryIsShownInPartAndSaysHowManyAreLeft(t *testing.T) {
	root := mapFixture(t)
	for i := 0; i < maxFilesPerDir+5; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+string(rune('a'+i))+".go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := projectMap(nil, projectMapArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Map, "more files") {
		t.Errorf("a long directory is not reported as cut:\n%s", res.Map)
	}
	// The count covers the whole directory, not the page that was drawn.
	if res.Files < maxFilesPerDir+5 {
		t.Errorf("files = %d; the count follows the display cap instead of the directory", res.Files)
	}
}

// The tool's arguments are its contract with the model, and a required argument
// is a shape models get wrong: the call that matters most is the bare one, on the
// workspace root, with nothing filled in.
func TestProjectMapNeedsNoArguments(t *testing.T) {
	tl, err := makeProjectMapTool()
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{}}
	pt, ok := tl.(interface {
		ProcessRequest(agent.Context, *model.LLMRequest) error
	})
	if !ok {
		t.Fatal("the tool does not pack a declaration")
	}
	if err := pt.ProcessRequest(nil, req); err != nil {
		t.Fatal(err)
	}
	var decl *genai.FunctionDeclaration
	for _, tool := range req.Config.Tools {
		for _, d := range tool.FunctionDeclarations {
			if d.Name == "project_map" {
				decl = d
			}
		}
	}
	if decl == nil {
		t.Fatal("no declaration for project_map")
	}
	b, err := json.Marshal(decl.ParametersJsonSchema)
	if err != nil {
		t.Fatal(err)
	}
	schema := string(b)
	if strings.Contains(schema, `"required"`) {
		t.Errorf("project_map has required arguments: %s", schema)
	}
	// Every field needs its description. A bare {"type":"string"} tells the model
	// nothing, and the field it most needs explaining is depth — "deeper" to a
	// model means either one level or everything.
	for _, field := range []string{"path", "depth", "description"} {
		if !strings.Contains(schema, field) {
			t.Errorf("the schema does not describe %q: %s", field, schema)
		}
	}
}

// A project with nothing dmcode recognises must say "unknown" rather than
// inventing a stack from extensions it has no table for.
func TestAnUnrecognisedProjectSaysUnknown(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	withRoot(t, root)
	res, err := projectMap(nil, projectMapArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages != "unknown" {
		t.Errorf("Languages = %q, want unknown", res.Languages)
	}
	if res.Markers != "none found" {
		t.Errorf("Markers = %q, want none found", res.Markers)
	}
}

// The map's reason for existing is that the model must be able to reach it. It
// is in the full set and — the part that is easy to forget — in the read-only
// set too, because a plan built without knowing what the project is is the same
// mistake in a different mode.
func TestProjectMapIsReachableInBothToolSets(t *testing.T) {
	all, err := MakeTools()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tl := range all {
		if tl.Name() == "project_map" {
			found = true
		}
	}
	if !found {
		t.Error("project_map is missing from the full tool set")
	}

	ro, err := MakeReadOnlyTools()
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, tl := range ro {
		if tl.Name() == "project_map" {
			found = true
		}
	}
	if !found {
		t.Error("project_map is missing from the read-only set, so plan mode cannot orient itself")
	}
}

// mustRoot is the boundary the fixture set, for the cases that need an absolute
// path of their own.
func mustRoot(t *testing.T) string {
	t.Helper()
	r := Root()
	if r == "" {
		t.Fatal("no root is set")
	}
	return r
}
