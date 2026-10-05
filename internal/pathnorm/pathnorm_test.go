package pathnorm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// symlinkable creates a symlink, reporting whether the filesystem allows one.
// Windows needs developer mode or administrator rights for symlinks, so a test
// that cannot create its link has nothing to check and skips rather than failing
// on the platform.
func symlinkable(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
}

// TestSameCollapsesTwoSpellingsOfOneFile is the whole reason this package
// exists: /var/... and /private/... are one directory, and code that bounds-checks
// one spelling against the other calls a file it just created a stranger.
func TestSameCollapsesTwoSpellingsOfOneFile(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	symlinkable(t, base, link)

	viaLink := filepath.Join(link, "a.txt")
	direct := filepath.Join(base, "a.txt")
	if !Same(viaLink, direct) {
		t.Errorf("Same(%q, %q) = false, want true: one file, two spellings", viaLink, direct)
	}
	if got, want := Canonical(viaLink), Canonical(direct); got != want {
		t.Errorf("Canonical(%q) = %q, want %q", viaLink, got, want)
	}
}

// TestCanonicalKeepsAMissingTail covers the case the tools hit on every write:
// the file does not exist yet, so there is no link to follow and only the
// directory above it can be resolved. Dropping the tail would hand back the
// directory and write the file inside the wrong name.
func TestCanonicalKeepsAMissingTail(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "not", "there", "new.txt")

	got := Canonical(missing)
	if filepath.Base(got) != "new.txt" || filepath.Base(filepath.Dir(got)) != "there" {
		t.Errorf("Canonical(%q) = %q, want the missing tail kept", missing, got)
	}
	if !Same(missing, missing) {
		t.Errorf("a path is not the same file as itself: %q", got)
	}
}

// TestSameTellsDifferentFilesApart is the counterweight. A helper that says yes
// to everything would make every boundary meaningless.
func TestSameTellsDifferentFilesApart(t *testing.T) {
	base := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(base, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := filepath.Join(base, "a.txt")
	b := filepath.Join(base, "b.txt")
	if Same(a, b) {
		t.Errorf("Same(%q, %q) = true, want false", a, b)
	}
	if Same(a, filepath.Join(base, "missing.txt")) {
		t.Errorf("a file and a path that does not exist are not the same file")
	}
	// The standing empty buffer is spelled "" and must match only itself.
	if !Same("", "") {
		t.Error(`Same("", "") = false, want true`)
	}
	if Same(a, "") {
		t.Errorf("Same(%q, \"\") = true, want false", a)
	}
}

// TestSameIgnoresLetterCaseOnWindows: the filesystem does, so a path spelled in
// a different case is the same file and must not read as a second one.
func TestSameIgnoresLetterCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("letter case only collapses into one name on Windows")
	}
	base := t.TempDir()
	f := filepath.Join(base, "CaseTest.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	shouted := filepath.Join(base, "casetest.TXT")
	if !Same(f, shouted) {
		t.Errorf("Same(%q, %q) = false, want true", f, shouted)
	}
}
