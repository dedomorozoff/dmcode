package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Watching what a shell command did to the tree.
//
// The property under test is not "the watcher finds changes" — that is easy, and
// a test that only checks it would pass on an implementation that invents diffs.
// It is that the watcher reports *only what it can know*, and that a file it
// cannot measure is never presented as one it measured.

// watchDir is a workspace with the tools pointed at it and the tally empty.
func watchDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	t.Cleanup(ResetChanges)
	return dir
}

// watchFile writes a file inside the workspace and returns its path.
func watchFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runWatched runs fn through commandEffect, which is what run_command does, so
// the test drives the seam rather than a hand-rolled call that skips it.
func runWatched(t *testing.T, dir string, fn func() ([]byte, error)) []byte {
	t.Helper()
	out, err := commandEffect(dir, fn)
	if err != nil {
		t.Fatalf("the watched command failed: %v", err)
	}
	return out
}

// The case this whole file exists for: a shell rewrites files it never mentions,
// and the panel must show them. Before the watcher, sed -i over a tree changed two
// hundred files and the tally reported none of them.
func TestACommandThatRewritesFilesIsRecorded(t *testing.T) {
	dir := watchDir(t)
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		watchFile(t, dir, n, "hello\n")
	}

	runWatched(t, dir, func() ([]byte, error) {
		for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
			p := filepath.Join(dir, n)
			if err := os.WriteFile(p, []byte("hello there\n"), 0o644); err != nil {
				return nil, err
			}
		}
		return []byte("done"), nil
	})

	got := ChangedFilesDetailed()
	if len(got) != 3 {
		t.Fatalf("the tally holds %d files, want all three the command rewrote: %+v", len(got), got)
	}
	for _, f := range got {
		if !f.Touched {
			t.Errorf("%s is listed without the touched flag, so its zero counts read as real", f.Path)
		}
	}
}

// A file the tools never saw the contents of has no line counts. They must come
// back as unknown rather than as zero, because "+0 -0" on a rewritten file says
// the opposite of the truth and looks like a measurement besides.
func TestACommandChangeCarriesNoLineCounts(t *testing.T) {
	dir := watchDir(t)
	watchFile(t, dir, "a.txt", "one\ntwo\n")

	runWatched(t, dir, func() ([]byte, error) {
		return nil, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\nfour\n"), 0o644)
	})

	files := ChangedFilesDetailed()
	if len(files) != 1 {
		t.Fatalf("files = %+v, want one", files)
	}
	if !files[0].Touched {
		t.Error("a command's edit is reported as a measured one")
	}
	// The aggregate must not add these into its totals either: two rows reading
	// +0 -0 would otherwise sum to a confident -0.
	if s := Changes(); s.Unknown != 1 {
		t.Errorf("Unknown = %d, want 1", s.Unknown)
	}
}

// A measured change and an unmeasured one sit in the same list, so the two have to
// be tellable apart from the type alone. If they were not, every caller would
// have to guess, and one of the two guesses is a lie.
func TestMeasuredAndTouchedAreDistinguishable(t *testing.T) {
	dir := watchDir(t)
	p := watchFile(t, dir, "measured.txt", "a\n")

	// A write the tools made: both halves in hand.
	recordChange(p, "a\n", "a\nb\n")

	// A change a command made: neither half.
	other := watchFile(t, dir, "command.txt", "x\n")
	recordTouched(other)

	byName := map[string]FileChange{}
	for _, f := range ChangedFilesDetailed() {
		byName[filepath.Base(f.Path)] = f
	}
	if got := byName["measured.txt"]; got.Touched || got.Added != 1 {
		t.Errorf("measured.txt = %+v, want a measured +1", got)
	}
	if got := byName["command.txt"]; !got.Touched {
		t.Errorf("command.txt = %+v, want it marked touched", got)
	}
}

// The flag is sticky. A command changed the file; a write after it is measured
// against content that already includes whatever the command did, so the delta is
// true of the write and silent about everything before it. Clearing the flag would
// turn "we do not know" into a confident number nobody can distrust correctly.
func TestALaterWriteDoesNotEraseTheLostBaseline(t *testing.T) {
	dir := watchDir(t)
	p := watchFile(t, dir, "a.txt", "one\ntwo\n")

	runWatched(t, dir, func() ([]byte, error) {
		return nil, os.WriteFile(p, []byte("one\nTWO\nthree\n"), 0o644)
	})
	// Now a real write, which does know both of its halves.
	recordChange(p, "one\nTWO\nthree\n", "one\nTWO\n")

	files := ChangedFilesDetailed()
	if len(files) != 1 {
		t.Fatalf("files = %+v, want one", files)
	}
	if !files[0].Touched {
		t.Error("the flag was cleared by a later write, so the row now reads as a whole-session diff")
	}
}

// A file the command deleted is still a change the session made. It cannot be
// opened, so the panel marks it — but dropping it from the tally would understate
// the session in the one direction the user cannot undo by typing something.
func TestAFileTheCommandDeletedIsStillRecorded(t *testing.T) {
	dir := watchDir(t)
	p := watchFile(t, dir, "gone.txt", "x\n")

	runWatched(t, dir, func() ([]byte, error) {
		return nil, os.Remove(p)
	})

	files := ChangedFilesDetailed()
	if len(files) != 1 || filepath.Base(files[0].Path) != "gone.txt" {
		t.Fatalf("a deleted file left the tally: %+v", files)
	}
	if !files[0].Touched {
		t.Error("a deletion is reported as a measured edit")
	}
}

// An untouched tree must produce an empty list. A watcher that reports every file
// as changed is worse than none: the panel would fill with ± on a command that
// only read something.
func TestACommandThatChangesNothingRecordsNothing(t *testing.T) {
	dir := watchDir(t)
	watchFile(t, dir, "a.txt", "untouched\n")

	runWatched(t, dir, func() ([]byte, error) {
		return []byte("just looked"), nil
	})

	if files := ChangedFilesDetailed(); len(files) != 0 {
		t.Errorf("a read-only command recorded changes: %+v", files)
	}
	if s := Changes(); s.Files != 0 {
		t.Errorf("Files = %d, want 0", s.Files)
	}
}

// The snapshot must see the same tree the listings do. A watcher that descended
// into node_modules would be enormous; one that saw less than grep would report
// files the user cannot find anywhere else. Reusing isIgnoredDir is what keeps
// those two answers equal.
func TestTheWatcherSkipsWhatTheListingsSkip(t *testing.T) {
	dir := watchDir(t)
	watchFile(t, dir, "seen.txt", "x\n")
	watchFile(t, dir, filepath.Join("node_modules", "pkg", "index.js"), "module.exports = 1\n")
	watchFile(t, dir, filepath.Join(".git", "config"), "[core]\n")

	before := takeSnapshot(dir)
	if _, ok := before.files[filepath.Join(dir, "seen.txt")]; !ok {
		t.Fatal("the snapshot missed an ordinary file")
	}
	for _, hidden := range []string{
		filepath.Join(dir, "node_modules", "pkg", "index.js"),
		filepath.Join(dir, ".git", "config"),
	} {
		if _, ok := before.files[hidden]; ok {
			t.Errorf("the snapshot recorded %s, which no listing would show", hidden)
		}
	}
	if !before.wholeTree() {
		t.Error("a small tree reported an incomplete pass")
	}
}

// A command that fails still changed things. `sed -i ... && false` exits non-zero
// after rewriting everything, and a tally that only recorded successful commands
// would be wrong exactly when something went wrong.
func TestAFailedCommandStillRecordsWhatItDid(t *testing.T) {
	dir := watchDir(t)
	p := watchFile(t, dir, "a.txt", "one\n")

	out, err := commandEffect(dir, func() ([]byte, error) {
		if werr := os.WriteFile(p, []byte("two words\n"), 0o644); werr != nil {
			return nil, werr
		}
		return []byte("partial"), errorsNew("exit status 1")
	})
	if err == nil {
		t.Fatal("the test's command was supposed to fail")
	}
	if !strings.Contains(string(out), "partial") {
		t.Errorf("the command's own output was lost: %q", out)
	}
	if files := ChangedFilesDetailed(); len(files) != 1 {
		t.Errorf("a failing command's edit was not recorded: %+v", files)
	}
}

// An incomplete pass must contribute nothing. Two snapshots of two different
// prefixes of a big tree would report every file the prefix did not reach as
// deleted, which is not a small lie: it would fill the panel with files that were
// never touched.
func TestAnIncompleteSnapshotRecordsNothing(t *testing.T) {
	dir := watchDir(t)
	watchFile(t, dir, "a.txt", "x\n")

	before := takeSnapshot(dir)
	// Pretend the walk stopped early, which is what hitting the bound does.
	before.whole = false
	recordCommandChanges(before, takeSnapshot(dir))

	if files := ChangedFilesDetailed(); len(files) != 0 {
		t.Errorf("an incomplete pair still produced claims: %+v", files)
	}
}

// And the command is told, because the model is about to go on reasoning about
// what it just did with no way to find out.
func TestAnIncompleteWatchIsReportedToTheCommand(t *testing.T) {
	dir := watchDir(t)
	// A root that cannot be read at all is the cheapest way to get an
	// incomplete pass without writing a hundred thousand files.
	out, err := commandEffect(filepath.Join(dir, "nope"), func() ([]byte, error) {
		return []byte("ran anyway"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "could not watch") {
		t.Errorf("an unwatched command says nothing about being unwatched: %q", out)
	}
}

// The boundary is lexical at resolve time and a shell ignores it entirely, so the
// watcher has to decide for itself what part of the tree it is describing. It
// describes the root: a path outside it is not this session's workspace, and
// recording it would put a row on the panel for a file the user cannot open from
// the panel at all.
func TestTheWatcherOnlyDescribesTheWorkspace(t *testing.T) {
	dir := watchDir(t)
	outside := t.TempDir()
	watchFile(t, dir, "inside.txt", "x\n")
	watchFile(t, outside, "elsewhere.txt", "y\n")

	runWatched(t, dir, func() ([]byte, error) {
		if err := os.WriteFile(filepath.Join(outside, "elsewhere.txt"), []byte("changed\n"), 0o644); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("changed\n"), 0o644)
	})

	for _, f := range ChangedFilesDetailed() {
		if filepath.Base(f.Path) == "elsewhere.txt" {
			t.Errorf("a file outside the workspace reached the panel: %s", f.Path)
		}
	}
}

// A symlink is recorded but not followed. Following one is how a snapshot reads a
// file the boundary refuses to open, and the tally is a statement about this
// workspace.
func TestTheWatcherDoesNotFollowSymlinksOut(t *testing.T) {
	if os.Getenv("GOOS") == "windows" && !hasSymlinkPrivilege(t) {
		t.Skip("creating a symlink on Windows needs a privilege this test does not assume")
	}
	dir := watchDir(t)
	outside := t.TempDir()
	secret := watchFile(t, outside, "secret.txt", "original\n")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	before := takeSnapshot(dir)
	if _, ok := before.files[link]; !ok {
		t.Error("the symlink itself was not recorded")
	}
	runWatched(t, dir, func() ([]byte, error) {
		return nil, os.WriteFile(secret, []byte("changed outside\n"), 0o644)
	})
	after := takeSnapshot(dir)
	for _, p := range snapshotEdited(before, after) {
		if p == link {
			t.Error("a write through a symlink was reported as a change to a workspace file")
		}
	}
}

// errorsNew keeps the failing-command test readable without importing errors for
// one call.
func errorsNew(s string) error { return &simpleError{s} }

type simpleError struct{ s string }

func (e *simpleError) Error() string { return e.s }

// hasSymlinkPrivilege reports whether this machine can make a symlink, which on
// Windows depends on a token setting rather than on anything the test controls.
func hasSymlinkPrivilege(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "link")); err != nil {
		return false
	}
	return true
}
