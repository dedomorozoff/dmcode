package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if _, err := writeFileAtomic(p, []byte("one\nTWO\nthree\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(p, []byte("one\nTWO!\nthree\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(p, []byte("one\ntwo\nthree\nfour\n")); err != nil {
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

	if _, err := writeFileAtomic(filepath.Join(dir, "n.txt"), []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(filepath.Join(dir, "m.txt"), []byte("c\n")); err != nil {
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
	if _, err := writeFileAtomic(p, []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	if _, err := writeFileAtomic(p, []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	if got := Changes(); got.Files != 0 {
		t.Errorf("an identical rewrite counted as %d changed files", got.Files)
	}
}

// The sidebar's per-file list has to be the same tally the aggregate answers
// for, split by file — one walk over one map, not a second bookkeeping.
func TestChangedFilesDetailedAgreesWithTheAggregate(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	t.Cleanup(ResetChanges)

	// a.txt is put in place outside the tally, so its baseline is its original
	// content and the edit below reads as a replacement: one line added and one
	// removed. b.txt does not exist yet, so the session creates it and every one
	// of its lines is an addition — two files in the list with two different
	// kinds of row, which is the case a single aggregate number could not show.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	if _, err := writeFileAtomic(filepath.Join(dir, "b.txt"), []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(filepath.Join(dir, "a.txt"), []byte("one\nTWO\n")); err != nil {
		t.Fatal(err)
	}

	files := ChangedFilesDetailed()
	if len(files) != 2 {
		t.Fatalf("files = %d, want one row per touched file: %+v", len(files), files)
	}
	// Sorted by path, because a list that reshuffled between frames could not be
	// read and a click would land on a different file than the row named.
	if filepath.Base(files[0].Path) != "a.txt" || filepath.Base(files[1].Path) != "b.txt" {
		t.Errorf("files are not in path order: %+v", files)
	}
	if files[0].Added != 1 || files[0].Removed != 1 {
		t.Errorf("a.txt = +%d -%d, want +1 -1 for the one line that differs", files[0].Added, files[0].Removed)
	}
	if files[1].Added != 2 || files[1].Removed != 0 {
		t.Errorf("b.txt = +%d -%d, want +2 -0 for a new file", files[1].Added, files[1].Removed)
	}

	// The aggregate is the sum of the rows: two answers to one question have to
	// be the same answer.
	agg := Changes()
	var add, rm int
	for _, f := range files {
		add += f.Added
		rm += f.Removed
	}
	if agg.Files != len(files) || agg.Added != add || agg.Removed != rm {
		t.Errorf("aggregate %+v does not match the rows %+v", agg, files)
	}
}

// A file the session edited and then put back is not in the list: "+0 -0" is a
// statement about work that is not in the tree. It stays in ChangedFiles, which
// answers what was touched.
func TestAFileChangedBackIsLeftOutOfTheList(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	t.Cleanup(ResetChanges)

	p := filepath.Join(dir, "same.txt")
	if _, err := writeFileAtomic(p, []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}
	ResetChanges()
	if _, err := writeFileAtomic(p, []byte("a\nB\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(p, []byte("a\nb\n")); err != nil {
		t.Fatal(err)
	}

	if files := ChangedFilesDetailed(); len(files) != 0 {
		t.Errorf("a file whose net change is zero is listed: %+v", files)
	}
	if len(ChangedFiles()) != 1 {
		t.Error("the file was dropped from ChangedFiles too, which answers a different question")
	}
}

func TestDiffBlockShowsTheChangedLines(t *testing.T) {
	got := DiffBlock("src/app.go", "a\nb\nc\n", "a\nB\nc\n")
	want := "── src/app.go\n-2: b\n+2: B"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestDiffBlockEmptyWhenUnchanged(t *testing.T) {
	if d := DiffBlock("f", "same\n", "same\n"); d != "" {
		t.Fatalf("unchanged files must not produce a diff, got %q", d)
	}
}

func TestDiffBlockNewFileIsAllAdditions(t *testing.T) {
	got := DiffBlock("n.txt", "", "a\nb\n")
	want := "── n.txt\n+1: a\n+2: b"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestDiffBlockCapsTheRows(t *testing.T) {
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, strconv.Itoa(i))
	}
	got := DiffBlock("big.txt", "", strings.Join(lines, "\n")+"\n")
	rows := strings.Split(got, "\n")
	if len(rows) != diffMaxRows+2 {
		t.Fatalf("rows = %d, want header + %d + the tail", len(rows), diffMaxRows)
	}
	if !strings.Contains(rows[len(rows)-1], "+10 more") {
		t.Fatalf("tail %q does not name the remaining lines", rows[len(rows)-1])
	}
}
