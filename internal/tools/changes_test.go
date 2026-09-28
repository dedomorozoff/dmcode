package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLineDeltaCountsOnlyTheDifference(t *testing.T) {
	for _, tc := range []struct {
		name            string
		before, after   string
		wantAdd, wantRm int
	}{
		{"new file", "", "a\nb\nc\n", 3, 0},
		{"emptied", "a\nb\n", "", 0, 2},
		{"appended", "a\n", "a\nb\n", 1, 0},
		{"one line changed", "a\nb\nc\n", "a\nB\nc\n", 1, 1},
		{"block replaced", "a\nb\nc\nd\n", "a\nx\ny\nd\n", 2, 2},
		{"identical", "a\nb\n", "a\nb\n", 0, 0},
		{"no trailing newline", "a", "a\nb", 1, 0},
		{"crlf counts as one line", "a\r\nb\r\n", "a\r\nb\r\nc\r\n", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			add, rm := lineDelta(tc.before, tc.after)
			if add != tc.wantAdd || rm != tc.wantRm {
				t.Errorf("lineDelta = +%d -%d, want +%d -%d", add, rm, tc.wantAdd, tc.wantRm)
			}
		})
	}
}

// The tally has to be the diff of the session, not a sum of the write calls: a
// file written three times reports what it now differs by from how it started,
// not three independent edits.
func TestChangesNetOutAcrossWrites(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	t.Cleanup(ResetChanges)

	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetChanges()

	// Two edits to the same line: the file ends up differing from the original by
	// exactly one line, whatever the agent went through on the way.
	if err := writeFileAtomic(p, []byte("one\nTWO\nthree\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("one\nTWO!\nthree\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("one\ntwo\nthree\nfour\n")); err != nil {
		t.Fatal(err)
	}

	got := Changes()
	if got.Files != 1 {
		t.Errorf("Files = %d, want 1", got.Files)
	}
	if got.Added != 1 || got.Removed != 0 {
		t.Errorf("changes = +%d -%d, want +1 -0 for the appended line", got.Added, got.Removed)
	}
}

func TestChangesCountNewFiles(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	t.Cleanup(ResetChanges)

	if err := writeFileAtomic(filepath.Join(dir, "n.txt"), []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "m.txt"), []byte("c\n")); err != nil {
		t.Fatal(err)
	}
	got := Changes()
	if got.Files != 2 || got.Added != 3 || got.Removed != 0 {
		t.Errorf("changes = %d files +%d -%d, want 2 +3 -0", got.Files, got.Added, got.Removed)
	}
}

// A write that changes nothing is not a change. Reporting it would make an
// idempotent write look like an edit in the sidebar.
func TestUnchangedFileIsNotCounted(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	t.Cleanup(ResetChanges)

	p := filepath.Join(dir, "same.txt")
	if err := writeFileAtomic(p, []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	if err := writeFileAtomic(p, []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	if got := Changes(); got.Files != 0 {
		t.Errorf("an identical rewrite counted as %d changed files", got.Files)
	}
}
