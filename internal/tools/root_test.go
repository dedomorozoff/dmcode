package tools

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withRoot scopes the tools to dir for the duration of the test and clears the
// boundary afterwards, so one test's root cannot leak into the next.
func withRoot(t *testing.T, dir string) {
	t.Helper()
	if err := SetRoot(dir); err != nil {
		t.Fatalf("SetRoot(%q): %v", dir, err)
	}
	t.Cleanup(func() {
		rootMu.Lock()
		root = ""
		rootMu.Unlock()
	})
}

func TestSetRootRejectsMissingDirectory(t *testing.T) {
	if err := SetRoot(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("SetRoot on a missing directory returned no error")
	}
}

func TestSetRootRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetRoot(f); err == nil {
		t.Fatal("SetRoot on a regular file returned no error")
	}
}

// TestResolveAcceptsPathsInsideWorkspace is the core guarantee: everything under
// the root passes, and a relative path is judged by the absolute path it denotes
// rather than by its spelling.
func TestResolveAcceptsPathsInsideWorkspace(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	withRoot(t, base)

	for _, p := range []string{".", "sub", "sub/deep", filepath.Join(base, "sub", "f.go")} {
		if _, err := resolve(p); err != nil {
			t.Errorf("resolve(%q) = %v, want it accepted", p, err)
		}
	}
}

// TestResolveRejectsPathsOutsideWorkspace covers the traversal forms: a relative
// escape, an absolute path elsewhere, and a sibling whose name merely starts the
// same way as the root.
func TestResolveRejectsPathsOutsideWorkspace(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "project")
	sibling := filepath.Join(parent, "secrets")
	for _, d := range []string{base, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withRoot(t, base)

	for _, p := range []string{
		"..",
		"../secrets",
		"../../etc/passwd",
		sibling,
		filepath.Join(base, "..", "secrets", "id_rsa"),
		// A sibling directory whose name has the root as a prefix: lexically it
		// must be treated as outside, not as a child.
		base + "-backup",
	} {
		_, err := resolve(p)
		if err == nil {
			t.Errorf("resolve(%q) was allowed, want it rejected", p)
			continue
		}
		if !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("resolve(%q) = %v, want ErrOutsideRoot", p, err)
		}
		if !strings.Contains(err.Error(), "project") {
			t.Errorf("resolve(%q) error %q does not name the working directory", p, err)
		}
	}
}

func TestReadFileRefusesOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	withRoot(t, base)

	_, err := readFile(nil, readFileArgs{Path: secret})
	if err == nil {
		t.Fatal("read_file read a file outside the workspace")
	}
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("read_file error = %v, want ErrOutsideRoot", err)
	}
}

func TestWriteFileRefusesOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escaped.txt")
	withRoot(t, base)

	if _, err := writeFile(nil, writeFileArgs{Path: outside, Content: "x"}); err == nil {
		t.Fatal("write_file wrote outside the workspace")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("the file exists at %s, want it never created", outside)
	}
}

func TestRunCommandRefusesOutsideWorkDir(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	withRoot(t, base)

	_, err := runCommand(nil, runCommandArgs{Command: "echo hi", WorkDir: outside})
	if err == nil {
		t.Fatal("run_command accepted a work_dir outside the workspace")
	}
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("run_command error = %v, want ErrOutsideRoot", err)
	}
}

func TestGlobRefusesEscapingPattern(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "project")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	withRoot(t, base)

	if _, err := glob(nil, globArgs{Pattern: "../*.txt"}); err == nil {
		t.Fatal("glob matched outside the workspace")
	}
	if _, err := glob(nil, globArgs{Pattern: "*.go"}); err != nil {
		t.Fatalf("glob inside the workspace = %v, want it allowed", err)
	}
}

func TestCheckPatternRootFindsTheRealDirectory(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "project")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	withRoot(t, base)

	for _, p := range []string{"*.go", "**/*.go", "src/**/*.go", "sub/deep/file.go"} {
		if err := checkPatternRoot(p); err != nil {
			t.Errorf("checkPatternRoot(%q) = %v, want it accepted", p, err)
		}
	}
	// The absolute case has to be spelled with a path the running OS considers
	// absolute. "C:/Windows/system32/*.dll" is only such a path on Windows; on
	// Unix it is a relative name, so resolve joins it onto the root and the
	// pattern is workspace-local by every rule the code follows.
	anchored := []string{"/etc/passwd"}
	if runtime.GOOS == "windows" {
		anchored = []string{`C:\Windows\system32\*.dll`, "C:/Windows/system32/*.dll"}
	}
	for _, p := range append([]string{"../*.go"}, anchored...) {
		if err := checkPatternRoot(p); err == nil {
			t.Errorf("checkPatternRoot(%q) was allowed, want it rejected", p)
		}
	}
}

// TestMakeReadOnlyToolsExcludesEveryMutator is the guarantee plan mode rests on:
// the read-only set must contain nothing that can change the workspace.
func TestMakeReadOnlyToolsExcludesEveryMutator(t *testing.T) {
	ro, err := MakeReadOnlyTools()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range ro {
		got[tl.Name()] = true
	}
	for _, want := range []string{"read_file", "list_dir", "grep", "glob"} {
		if !got[want] {
			t.Errorf("the read-only set is missing %q", want)
		}
	}
	// run_command is the subtle one: a shell redirects to a file, so it cannot
	// be treated as read-only.
	for _, banned := range []string{"write_file", "edit_file", "run_command"} {
		if got[banned] {
			t.Errorf("the read-only set contains %q, which can modify the workspace", banned)
		}
	}
}

func TestToolNamesFollowsTheSet(t *testing.T) {
	all, err := MakeTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(ToolNames(all)) != len(all) {
		t.Errorf("ToolNames returned %d names for %d tools", len(ToolNames(all)), len(all))
	}
	ro, err := MakeReadOnlyTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(ro) >= len(all) {
		t.Errorf("the read-only set (%d) is not smaller than the full one (%d)", len(ro), len(all))
	}
}

// TestResolveWithoutRootAllowsAnything documents the unconfigured case: the tools
// stay usable, they are simply not fenced in.
func TestResolveWithoutRootAllowsAnything(t *testing.T) {
	rootMu.Lock()
	root = ""
	rootMu.Unlock()

	if _, err := resolve(filepath.Join(t.TempDir(), "..", "elsewhere")); err != nil {
		t.Fatalf("resolve without a root = %v, want it allowed", err)
	}
}
