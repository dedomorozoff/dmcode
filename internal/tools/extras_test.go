package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func useRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := Root()
	SetRoot(root)
	t.Cleanup(func() { SetRoot(old) })
	ResetChanges()
	return root
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFilesReportsEachFile(t *testing.T) {
	root := useRoot(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "alpha\nbeta\n")
	writeTestFile(t, filepath.Join(root, "long.txt"), "one\ntwo\nthree\nfour\n")

	res, err := readFiles(nil, readFilesArgs{
		Paths:      []string{"a.txt", "missing.txt", "long.txt"},
		LimitLines: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 3 {
		t.Fatalf("got %d files, want 3", len(res.Files))
	}
	if res.Files[0].Content != "alpha\nbeta" || res.Files[0].Error != "" {
		t.Errorf("a.txt = %+v", res.Files[0])
	}
	if res.Files[1].Error == "" {
		t.Errorf("missing.txt did not report an error: %+v", res.Files[1])
	}
	if !res.Files[2].Truncated || res.Files[2].NextOffset != 3 {
		t.Errorf("long.txt paging = %+v", res.Files[2])
	}
}

func TestApplyPatchModifiesCreatesDeletesAndUndoes(t *testing.T) {
	root := useRoot(t)
	writeTestFile(t, filepath.Join(root, "hello.txt"), "one\nthree\n")
	writeTestFile(t, filepath.Join(root, "old.txt"), "bye\n")
	BeginTurn()

	patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1,2 +1,3 @@
 one
+two
 three
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+alpha
+beta
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
`
	res, err := applyPatch(nil, applyPatchArgs{Patch: patch})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 3 {
		t.Fatalf("applied files = %v, want 3", res.Files)
	}
	hello, _ := os.ReadFile(filepath.Join(root, "hello.txt"))
	if string(hello) != "one\ntwo\nthree\n" {
		t.Errorf("hello.txt = %q", hello)
	}
	newFile, _ := os.ReadFile(filepath.Join(root, "new.txt"))
	if string(newFile) != "alpha\nbeta\n" {
		t.Errorf("new.txt = %q", newFile)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old.txt still exists: %v", err)
	}

	UndoTurn()
	hello, _ = os.ReadFile(filepath.Join(root, "hello.txt"))
	if string(hello) != "one\nthree\n" {
		t.Errorf("hello.txt after undo = %q", hello)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt survived the undo: %v", err)
	}
	if old, err := os.ReadFile(filepath.Join(root, "old.txt")); err != nil || string(old) != "bye\n" {
		t.Errorf("old.txt after undo = %q, %v", old, err)
	}
}

func TestApplyPatchRefusesAConflictBeforeWriting(t *testing.T) {
	root := useRoot(t)
	writeTestFile(t, filepath.Join(root, "hello.txt"), "one\nthree\n")
	patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1,2 +1,3 @@
 one
+two
 not-the-file
`
	if _, err := applyPatch(nil, applyPatchArgs{Patch: patch}); err == nil {
		t.Fatal("a conflicting patch was accepted")
	}
	body, _ := os.ReadFile(filepath.Join(root, "hello.txt"))
	if string(body) != "one\nthree\n" {
		t.Errorf("a refused patch changed the file: %q", body)
	}
}

func TestGitStatusAndDiffAreReadOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := useRoot(t)
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeTestFile(t, filepath.Join(root, "a.txt"), "alpha\n")

	st, err := gitStatus(nil, gitStatusArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Output, "a.txt") || st.Clean {
		t.Errorf("status = %+v", st)
	}
	if _, err := exec.Command("git", "-C", root, "add", "a.txt").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	diff, err := gitDiff(nil, gitDiffArgs{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Output, "alpha") {
		t.Errorf("staged diff = %q", diff.Output)
	}
}
