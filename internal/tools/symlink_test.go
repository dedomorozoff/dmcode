package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlinkable creates a symlink, reporting whether the filesystem allows one.
// Windows needs developer mode or administrator rights for symlinks, so a
// test that cannot create its link has nothing to check and skips instead of
// failing on the platform, not on the code.
func symlinkable(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
}

// TestReadFileRefusesSymlinkedFileOutsideWorkspace pins the gap the lexical
// check leaves open: a link inside the tree that points at a file outside it.
// read_file opens what the link names, so the resolved path has to clear the
// boundary too.
func TestReadFileRefusesSymlinkedFileOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "leak.txt")
	symlinkable(t, outside, link)
	withRoot(t, base)

	_, err := readFile(nil, readFileArgs{Path: "leak.txt"})
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("read_file through a symlink = %v, want ErrOutsideRoot", err)
	}
}

func TestEditFileRefusesSymlinkedFileOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "leak.txt")
	symlinkable(t, outside, link)
	withRoot(t, base)

	_, err := editFile(nil, editFileArgs{Path: "leak.txt", OldString: "s3cr3t", NewString: "owned"})
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("edit_file through a symlink = %v, want ErrOutsideRoot", err)
	}
	if data, rerr := os.ReadFile(outside); rerr != nil || string(data) != "s3cr3t" {
		t.Fatalf("the outside file changed: %q, %v", data, rerr)
	}
}

// TestWriteFileRefusesSymlinkedDirectoryOutsideWorkspace covers the write path:
// a directory link inside the tree moves every path beneath it outside the
// root, and the write has to be judged by where it really lands.
func TestWriteFileRefusesSymlinkedDirectoryOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	symlinkable(t, outside, link)
	withRoot(t, base)

	if _, err := writeFile(nil, writeFileArgs{Path: "link/escaped.txt", Content: "x"}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("write_file through a directory symlink = %v, want ErrOutsideRoot", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("the file landed outside the workspace, stat = %v", err)
	}
}

// TestWriteFileStillCreatesNestedDirectories is the counterweight: the symlink
// check must not cost write_file its ability to create a file in a directory
// that does not exist yet, which is its normal job.
func TestWriteFileStillCreatesNestedDirectories(t *testing.T) {
	base := t.TempDir()
	withRoot(t, base)

	res, err := writeFile(nil, writeFileArgs{Path: "a/b/c.txt", Content: "ok"})
	if err != nil {
		t.Fatalf("write_file = %v, want it allowed", err)
	}
	if res.BytesWritten != 2 {
		t.Fatalf("BytesWritten = %d, want 2", res.BytesWritten)
	}
	if data, rerr := os.ReadFile(filepath.Join(base, "a", "b", "c.txt")); rerr != nil || string(data) != "ok" {
		t.Fatalf("the file is wrong: %q, %v", data, rerr)
	}
}

// TestGrepSkipsSymlinkedFiles makes sure a walk cannot pull in outside content
// by following a link it meets inside the tree.
func TestGrepSkipsSymlinkedFiles(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "leak.txt")
	symlinkable(t, outside, link)
	withRoot(t, base)

	res, err := grep(nil, grepArgs{Pattern: "s3cr3t", Path: "."})
	if err != nil {
		t.Fatalf("grep = %v", err)
	}
	if strings.Contains(res.Matches, "s3cr3t") {
		t.Fatalf("grep followed the symlink: %q", res.Matches)
	}
}

// TestWithinRootAfterLinksAcceptsOrdinaryPaths makes sure the re-check does not
// reject what the lexical check rightly allows, including a missing path, which
// is write_file's normal case.
func TestWithinRootAfterLinksAcceptsOrdinaryPaths(t *testing.T) {
	base := t.TempDir()
	withRoot(t, base)

	for _, p := range []string{base, filepath.Join(base, "missing", "new.txt")} {
		if err := withinRootAfterLinks(p); err != nil {
			t.Errorf("withinRootAfterLinks(%q) = %v, want it accepted", p, err)
		}
	}
}

// TestExistingAncestorFindsTheDeepestPresentDirectory keeps the helper honest:
// it must stop at the first element that exists, not skip past it.
func TestExistingAncestorFindsTheDeepestPresentDirectory(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(filepath.Join(base, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := existingAncestor(deep), filepath.Join(base, "a"); got != want {
		t.Fatalf("existingAncestor = %q, want %q", got, want)
	}
}
