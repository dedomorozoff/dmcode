package main

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
