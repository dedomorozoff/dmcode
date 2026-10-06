package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileUniqueAndAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("aaa bbb aaa\n"), 0o644)

	if _, err := editFile(nil, editFileArgs{Path: p, OldString: "aaa", NewString: "X"}); err == nil {
		t.Fatal("expected ambiguity error")
	}
	res, err := editFile(nil, editFileArgs{Path: p, OldString: "bbb", NewString: "B"})
	if err != nil || res.Replacements != 1 {
		t.Fatalf("unique edit failed: %v %+v", err, res)
	}
	res, err = editFile(nil, editFileArgs{Path: p, OldString: "aaa", NewString: "Y", ReplaceAll: true})
	if err != nil || res.Replacements != 2 {
		t.Fatalf("replace_all failed: %v %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "Y B Y\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestGrepRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(sub, "b.txt"), []byte("hello world\n"), 0o644)

	res, err := grep(nil, grepArgs{Pattern: "hello", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Matches, "a.go:2") || !strings.Contains(res.Matches, "b.txt:1") {
		t.Fatalf("missing matches: %q", res.Matches)
	}
	res, err = grep(nil, grepArgs{Pattern: "hello", Path: dir, Glob: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Matches, "b.txt") {
		t.Fatalf("glob filter failed: %q", res.Matches)
	}
}

func TestReadFileOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("1\n2\n3\n4\n"), 0o644)
	res, err := readFile(nil, readFileArgs{Path: p, Offset: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "2\n3" || res.TotalLines != 5 || !res.Truncated {
		t.Fatalf("unexpected: %+v", res)
	}
}

// TestReadFilePagesPastTheByteCap pins the capability read_file advertises:
// offset_line must reach lines beyond the byte cap, and total_lines must count
// the real file, not the truncated tail. Before this test existed, a file over
// the cap was cut before paging, so "offset_line for large files" could never
// read past its first 256 KB — a promise the tool made for exactly the files
// it did not keep.
func TestReadFilePagesPastTheByteCap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 0; i < 10_000; i++ {
		b.WriteString("a fairly long line that pushes the file past the cap\n")
	}
	content := b.String()
	if len(content) <= maxReadBytes {
		t.Fatalf("fixture is only %d bytes, need more than %d", len(content), maxReadBytes)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := readFile(nil, readFileArgs{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	// The trailing newline is a trailing empty element, the same accounting
	// TestReadFileOffsetLimit pins: total counts the split, not the lines.
	want := len(strings.Split(content, "\n"))
	if res.TotalLines != want {
		t.Errorf("TotalLines = %d, want %d (the real file, not the cap)", res.TotalLines, want)
	}
	if !res.Truncated {
		t.Error("a file over the byte cap did not report truncation")
	}

	// The tail of the file is reachable: the first 256 KB cover far fewer than
	// 9000 lines, so a window there lands on content the old code could not
	// produce at any offset.
	res, err = readFile(nil, readFileArgs{Path: p, Offset: 9000, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalLines != want || !strings.Contains(res.Content, "long line") {
		t.Errorf("the tail page is wrong: %+v", res)
	}
}

func TestRunCommandEcho(t *testing.T) {
	res, err := runCommand(nil, runCommandArgs{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "hi") {
		t.Fatalf("unexpected output: %q", res.Output)
	}
}

func TestEditFileCRLF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "crlf.txt")
	os.WriteFile(p, []byte("line1\r\nline2\r\nline3\r\n"), 0o644)

	// Replacing with Unix \n in OldString should still succeed and preserve CRLF
	res, err := editFile(nil, editFileArgs{Path: p, OldString: "line2\n", NewString: "modified\n"})
	if err != nil || res.Replacements != 1 {
		t.Fatalf("editFile with newline mismatch failed: %v, %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "line1\r\nmodified\r\nline3\r\n" {
		t.Fatalf("unexpected content after CRLF edit: %q", data)
	}
}

// TestEditFileFuzzyIndent covers the fallback the exact path cannot do: the
// model quotes the snippet with spaces where the file uses tabs, and with a
// shallower indentation. The words match, the edit must land, and new_string
// must adopt the file's own indentation.
func TestEditFileFuzzyIndent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.go")
	src := "func main() {\n\tif x {\n\t\tdoThing()\n\t}\n}\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "if x {\n    doThing()\n}" // spaces instead of tabs
	new := "if x {\n    doOther()\n}"
	res, err := editFile(nil, editFileArgs{Path: p, OldString: old, NewString: new})
	if err != nil || res.Replacements != 1 {
		t.Fatalf("fuzzy indent edit failed: %v %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	want := "func main() {\n\tif x {\n\t\tdoOther()\n\t}\n}\n"
	if string(data) != want {
		t.Errorf("fuzzy edit produced %q, want %q", data, want)
	}
}

// TestEditFileFuzzyInternalSpacing matches lines whose internal runs of spaces
// differ, which is how hand-copied snippets usually drift.
func TestEditFileFuzzyInternalSpacing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("alpha    =   1\nbeta = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := editFile(nil, editFileArgs{Path: p, OldString: "alpha = 1", NewString: "alpha = 42"})
	if err != nil || res.Replacements != 1 {
		t.Fatalf("internal-spacing edit failed: %v %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "alpha = 42\nbeta = 2\n" {
		t.Errorf("unexpected content: %q", data)
	}
}

// TestEditFileFuzzyExpandsLine grows one file line into several: the followers
// inherit the indentation of the line they came from. The two-space old_string
// fails the exact path, so the fuzzy one is what does the work.
func TestEditFileFuzzyExpandsLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.go")
	if err := os.WriteFile(p, []byte("\treturn nil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := editFile(nil, editFileArgs{
		Path:      p,
		OldString: "  return nil",
		NewString: "if err != nil {\nreturn err\n}\nreturn nil",
	})
	if err != nil || res.Replacements != 1 {
		t.Fatalf("expanding edit failed: %v %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	want := "\tif err != nil {\n\treturn err\n\t}\n\treturn nil\n"
	if string(data) != want {
		t.Errorf("expansion produced %q, want %q", data, want)
	}
}

// TestEditFileFuzzyAmbiguousAndAll mirrors the exact-path ambiguity rules: a
// fuzzy match found twice must refuse a single replacement and accept
// replace_all.
func TestEditFileFuzzyAmbiguousAndAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	// The spacing differs from old_string, so the exact path misses and the
	// fuzzy one does the work.
	if err := os.WriteFile(p, []byte("x  =  1\nmid\nx  =  1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := editFile(nil, editFileArgs{Path: p, OldString: "x = 1", NewString: "y", ReplaceAll: true})
	if err != nil || res.Replacements != 2 {
		t.Fatalf("fuzzy replace_all failed: %v %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "y\nmid\ny\n" {
		t.Errorf("replace_all produced %q", data)
	}
}

// TestEditFileFuzzyAmbiguityRefused: two fuzzy matches without replace_all is
// an error, not a silent first-hit replacement.
func TestEditFileFuzzyAmbiguityRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("a = 1\nb\na = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := editFile(nil, editFileArgs{Path: p, OldString: "a=1", NewString: "z"}); err == nil {
		t.Fatal("ambiguous fuzzy match was silently replaced")
	}
}

// TestEditFileFuzzyKeepsCRLF: a CRLF file edited through the fuzzy path must
// stay CRLF, and new_string adopts the file's indentation. The over-deep
// old_string keeps the exact path out of it.
func TestEditFileFuzzyKeepsCRLF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("one\r\n    two\r\nthree\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := editFile(nil, editFileArgs{Path: p, OldString: "        two", NewString: "deux"})
	if err != nil || res.Replacements != 1 {
		t.Fatalf("CRLF fuzzy edit failed: %v %+v", err, res)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "one\r\n    deux\r\nthree\r\n" {
		t.Errorf("CRLF file damaged: %q", data)
	}
}

// TestWriteFileAtomicOverwrite: the atomic writer must replace an existing
// file, since os.Rename semantics differ across platforms.
func TestWriteFileAtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(p, []byte("new content")); err != nil {
		t.Fatalf("overwrite failed: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "new content" {
		t.Errorf("content = %q, want %q", data, "new content")
	}
}

func TestGlobDoubleStar(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "pkg", "sub")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "root.go"), []byte("package root"), 0o644)
	os.WriteFile(filepath.Join(sub, "deep.go"), []byte("package sub"), 0o644)
	os.WriteFile(filepath.Join(sub, "note.txt"), []byte("note"), 0o644)

	oldWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(oldWd)

	res, err := glob(nil, globArgs{Pattern: "**/*.go"})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !strings.Contains(res.Files, "root.go") || !strings.Contains(res.Files, "pkg/sub/deep.go") {
		t.Fatalf("glob **/*.go missing matches: %q", res.Files)
	}
	if strings.Contains(res.Files, "note.txt") {
		t.Fatalf("glob **/*.go included non-go file: %q", res.Files)
	}
}

func TestListDirDotfiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.tmp"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("KEY=VAL"), 0o644)
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0o644)

	res, err := listDir(nil, listDirArgs{Path: dir})
	if err != nil {
		t.Fatalf("listDir failed: %v", err)
	}
	if !strings.Contains(res.Entries, ".gitignore") || !strings.Contains(res.Entries, ".env") {
		t.Fatalf("expected config dotfiles to be visible: %q", res.Entries)
	}
	if strings.Contains(res.Entries, ".git/") {
		t.Fatalf(".git directory should be ignored: %q", res.Entries)
	}
}
