package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes a fixture file.
func writeFixture(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// TestUndoRestoresAWrittenFile is the heart of the feature: a write_file the
// turn made is taken back, the pre-turn content comes back, and the tally stops
// showing the undone edit.
func TestUndoRestoresAWrittenFile(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFixture(t, p, "old\ncontent\n")

	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("new content")); err != nil {
		t.Fatal(err)
	}

	rep := UndoTurn()

	if got := readFileT(t, p); got != "old\ncontent\n" {
		t.Errorf("after undo content = %q, want the pre-turn content", got)
	}
	if len(rep.Restored) != 1 || rep.Restored[0] != p {
		t.Errorf("Restored = %v, want [%s]", rep.Restored, p)
	}
	if len(rep.Unrestorable) != 0 || len(rep.Errors) != 0 {
		t.Errorf("unexpected report: %+v", rep)
	}
	for _, f := range ChangedFilesDetailed() {
		if f.Path == p {
			t.Errorf("the undone write is still in the tally: %+v", f)
		}
	}
}

// TestUndoRemovesANewlyCreatedFile: a write that created the file must undo to
// absence, not to an empty copy of something that was never there.
func TestUndoRemovesANewlyCreatedFile(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "new.txt")

	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("brand new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the file should exist before the undo: %v", err)
	}

	rep := UndoTurn()

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("after undo the created file is still there: %v", err)
	}
	if len(rep.Restored) != 1 {
		t.Errorf("Restored = %v, want the new file listed", rep.Restored)
	}
}

// TestUndoTwoWritesComeBackToTheFirst: two writes to one file in a turn undo
// in reverse order to where the first one found the file.
func TestUndoTwoWritesComeBackToTheFirst(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFixture(t, p, "start\n")

	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}

	UndoTurn()

	if got := readFileT(t, p); got != "start\n" {
		t.Errorf("after undo content = %q, want %q", got, "start\n")
	}
}

// TestUndoTurnPopsOneTurnAtATime: each rewind takes back exactly the turn it
// cuts from the transcript, so two prompts mean two undos.
func TestUndoTurnPopsOneTurnAtATime(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFixture(t, p, "start\n")

	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("turn one")); err != nil {
		t.Fatal(err)
	}
	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("turn two")); err != nil {
		t.Fatal(err)
	}

	UndoTurn()
	if got := readFileT(t, p); got != "turn one" {
		t.Errorf("after the first undo content = %q, want %q", got, "turn one")
	}
	UndoTurn()
	if got := readFileT(t, p); got != "start\n" {
		t.Errorf("after the second undo content = %q, want %q", got, "start\n")
	}
}

// TestUndoWriteThenMoveLandsAtTheStart is the compound case that reverse-order
// application exists for: a file written and then moved comes back to its
// original place *and* its original content, with the destination gone.
func TestUndoWriteThenMoveLandsAtTheStart(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	q := filepath.Join(dir, "sub", "b.txt")
	writeFixture(t, p, "original\n")

	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("rewritten")); err != nil {
		t.Fatal(err)
	}
	if _, err := moveFile(nil, moveFileArgs{Source: p, Destination: q}); err != nil {
		t.Fatal(err)
	}

	UndoTurn()

	if got := readFileT(t, p); got != "original\n" {
		t.Errorf("after undo %s = %q, want the pre-turn content", p, got)
	}
	if _, err := os.Stat(q); !os.IsNotExist(err) {
		t.Errorf("the move destination is still there after the undo: %v", err)
	}
}

// TestUndoMovePutsTheFileBack: a move by itself is inverted by a rename.
func TestUndoMovePutsTheFileBack(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	q := filepath.Join(dir, "b.txt")
	writeFixture(t, p, "hello\n")

	BeginTurn()
	if _, err := moveFile(nil, moveFileArgs{Source: p, Destination: q}); err != nil {
		t.Fatal(err)
	}
	UndoTurn()

	if got := readFileT(t, p); got != "hello\n" {
		t.Errorf("after undo %s = %q", p, got)
	}
	if _, err := os.Stat(q); !os.IsNotExist(err) {
		t.Errorf("destination %s still exists after the undo", q)
	}
}

// TestUndoDeleteRestoresTheFile: a delete is a restore of the content that was
// recorded at removal.
func TestUndoDeleteRestoresTheFile(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "gone.txt")
	writeFixture(t, p, "precious\n")

	BeginTurn()
	if _, err := deleteFile(nil, deleteFileArgs{Path: p}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("the delete did not remove the file: %v", err)
	}

	UndoTurn()

	if got := readFileT(t, p); got != "precious\n" {
		t.Errorf("after undo content = %q, want %q", got, "precious\n")
	}
}

// TestUndoShellChangesAreReportedNotRestored: a shell command's effect cannot
// be taken back — no content was saved — so the rewind names the files and
// leaves them alone rather than pretending otherwise.
func TestUndoShellChangesAreReportedNotRestored(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFixture(t, p, "as the command left it\n")

	BeginTurn()
	pushUndo(undoEntry{kind: undoShell, path: p})
	rep := UndoTurn()

	if got := readFileT(t, p); got != "as the command left it\n" {
		t.Errorf("the shell-touched file was modified by the undo: %q", got)
	}
	if len(rep.Unrestorable) != 1 || rep.Unrestorable[0] != p {
		t.Errorf("Unrestorable = %v, want [%s]", rep.Unrestorable, p)
	}
	if len(rep.Restored) != 0 {
		t.Errorf("Restored = %v, want nothing restored", rep.Restored)
	}
}

// TestUndoShellSuppressesARestoreOnTheSamePath: a write that followed a shell
// command on the same file cannot undo the command's part, so the rewind says
// so and leaves the file where it is instead of half-restoring it.
func TestUndoShellSuppressesARestoreOnTheSamePath(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFixture(t, p, "before the command\n")

	BeginTurn()
	pushUndo(undoEntry{kind: undoShell, path: p})
	if _, err := writeFileAtomic(p, []byte("after the write")); err != nil {
		t.Fatal(err)
	}

	rep := UndoTurn()

	if got := readFileT(t, p); got != "after the write" {
		t.Errorf("file content = %q, want it left untouched", got)
	}
	if len(rep.Unrestorable) != 1 || rep.Unrestorable[0] != p {
		t.Errorf("Unrestorable = %v, want the shell-touched path named", rep.Unrestorable)
	}
	if len(rep.Restored) != 0 {
		t.Errorf("Restored = %v, want no false restore on an unrestorable path", rep.Restored)
	}
}

// TestUndoTurnWithNothingRecordedIsEmpty: a turn that changed no files (a pure
// conversation) pops an empty group and reports nothing.
func TestUndoTurnWithNothingRecordedIsEmpty(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	BeginTurn()
	rep := UndoTurn()
	if len(rep.Restored) != 0 || len(rep.Unrestorable) != 0 || len(rep.Errors) != 0 {
		t.Errorf("an empty turn produced %+v", rep)
	}
}

// TestResetChangesClearsTheJournal: /new must not let a rewind reach back into
// the previous session's file changes.
func TestResetChangesClearsTheJournal(t *testing.T) {
	ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFixture(t, p, "old\n")

	BeginTurn()
	if _, err := writeFileAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	ResetChanges()

	rep := UndoTurn()
	if len(rep.Restored) != 0 {
		t.Errorf("after ResetChanges a rewind still restored %v", rep.Restored)
	}
	if got := readFileT(t, p); got != "new" {
		t.Errorf("the file changed: %q", got)
	}
}

// TestUndoReportErrorsWhenARestoreCannotLand: restoring a path whose parent
// cannot hold it (here, a file sitting where a directory must be) is an error
// the report carries, not a silent skip.
func TestUndoReportErrorsWhenARestoreCannotLand(t *testing.T) {
	ResetChanges()
	defer ResetChanges()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	q := filepath.Join(p, "child") // a path under a file: impossible to create
	writeFixture(t, p, "blocker\n")

	BeginTurn()
	pushUndo(undoEntry{kind: undoWrite, path: q, before: "x", existed: true})

	rep := UndoTurn()

	if len(rep.Errors) != 1 {
		t.Errorf("Errors = %v, want the failed restore reported", rep.Errors)
	}
	if !strings.Contains(rep.Errors[0], q) {
		t.Errorf("error does not name the path: %q", rep.Errors[0])
	}
}
