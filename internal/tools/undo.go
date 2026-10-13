package tools

// undo.go — the turn journal behind ctrl+z's second half.
//
// The session rewind already cuts the conversation; this is the other half of
// the same key, the files on disk. Every write the tools make passes through
// writeFileAtomic, and every one of them knows what it replaced, so the tools
// can remember per turn what each file held before the turn touched it, and a
// rewind can put the tree back. Before this file, ctrl+z took a session back
// and left the workspace exactly where the agent had put it — the CHANGES
// panel's lie told from the other side, where the transcript says one thing
// and the disk says another.
//
// What can be undone, and what cannot:
//
//   - write_file and edit_file land in writeFileAtomic, which records the
//     content each write replaced. Undoing one restores that content, or
//     removes the file when it did not exist before the write.
//   - move_file and delete_file record enough to invert themselves: a move is
//     a rename back, a delete is a restore.
//   - run_command is the one instrument the journal cannot back out. The shell
//     watcher (shellwatch.go) compares the tree around a command and names what
//     changed, but it deliberately does not copy contents — copying is the
//     whole-tree snapshot it exists to avoid. A command's effects are therefore
//     recorded as *unrestorable*: a rewind reports the paths and leaves them
//     alone, rather than guessing at a pre-turn content nobody saved.
//
// The unit is the turn because the UI rewinds turns. BeginTurn marks the start
// of one, called at the same moment a prompt is sent, and UndoTurn rolls back
// the most recent group — in the opposite order to how its entries were
// recorded, so that two writes to one file come back to where the first one
// found it, a file that was written and then moved lands where the turn began,
// and a file created and then deleted disappears again.

import (
	"os"
	"path/filepath"
	"sync"
)

// undoKind says what an entry knows how to undo.
type undoKind int

const (
	// undoWrite restores path to before (or removes it when it did not exist).
	undoWrite undoKind = iota
	// undoMove renames dest back to path.
	undoMove
	// undoDelete restores path to before.
	undoDelete
	// undoShell marks path as changed by a shell command; nothing was captured,
	// so there is nothing to restore it to.
	undoShell
)

// undoEntry is one reversible fact about the workspace.
type undoEntry struct {
	kind   undoKind
	path   string // a write/delete/shell target, or a move's source
	before string // content before the operation
	// existed is false when the write created the file, so an undo must remove
	// it again rather than restore an empty version of something that was not
	// there. readIfExists cannot tell an empty file from a missing one, which
	// is why this is a separate fact.
	existed bool
	dest    string // undoMove only: where the file was moved to
}

// undoGroup is everything one turn did, popped whole by a rewind.
type undoGroup struct {
	entries []undoEntry
}

// undoJournal is the package-level stack of turn groups. It is a global for the
// same reason changes.go's tally is: the tools are wired straight into the
// agent's function calls, and the only way a write deep inside a turn reaches
// the UI that will undo it is through one shared seam.
var undoJournal = struct {
	mu     sync.Mutex
	groups []undoGroup
}{}

// BeginTurn opens a new journal group. The UI calls it at the moment a prompt
// is sent, so everything the resulting turn writes lands in one group and the
// next rewind rolls exactly that turn back.
func BeginTurn() {
	undoJournal.mu.Lock()
	defer undoJournal.mu.Unlock()
	undoJournal.groups = append(undoJournal.groups, undoGroup{})
}

// RecordShellChange notes in the undo journal that a shell command changed
// path, whose pre-turn content was never captured. It is the journal's
// counterpart to RecordTouched: the tally and the journal are told the same
// fact, by the same caller, because a caller that reports a change to one and
// not the other gets a panel that lies or a rewind that half-answers.
//
// It is exported for the same reason RecordTouched is — a caller that changed a
// file by some route the tools did not see can still record it — and so the
// UI's rewind wiring can be exercised from outside the package.
func RecordShellChange(path string) {
	pushUndo(undoEntry{kind: undoShell, path: path})
}

// pushUndo records one reversible operation in the current group.
//
// A caller that never called BeginTurn — a unit test, or a write outside a
// turn — still gets a group, so its work is not silently forgotten: an empty
// stack means "no group exists yet", not "do not journal".
func pushUndo(e undoEntry) {
	undoJournal.mu.Lock()
	defer undoJournal.mu.Unlock()
	if len(undoJournal.groups) == 0 {
		undoJournal.groups = append(undoJournal.groups, undoGroup{})
	}
	g := &undoJournal.groups[len(undoJournal.groups)-1]
	g.entries = append(g.entries, e)
}

// UndoReport is what a rewind did to the workspace, for the transcript to say.
type UndoReport struct {
	// Restored lists the paths written back to their pre-turn content.
	Restored []string
	// Unrestorable lists the paths a shell command changed during the turn.
	// Their pre-turn content was never captured, so they are left as they are —
	// reported rather than silently skipped, because a rewind that claims to
	// have undone the turn while a sed -i result stays in the tree is the
	// exact lie this journal exists to prevent.
	Unrestorable []string
	// Errors lists the paths that could not be restored, each carrying why.
	Errors []string
}

// UndoTurn rolls back the most recent journal group and reports what it did.
//
// The entries are applied in reverse order of recording. That is what makes the
// compound cases come out right: two writes to one file undo in two steps to
// the content the first write found; a write followed by a move undoes to the
// moved file coming back and then the write restoring the original; a file
// written, moved and edited lands exactly where the turn started. Restoring
// forward would leave every such path on its last-but-one state instead.
//
// The tally is updated alongside the tree, so the CHANGES panel stops showing
// an edit the rewind just removed. A path any shell command touched during the
// turn is left untouched: its pre-turn content is unknown, and writing a guess
// over a working tree is worse than saying so.
func UndoTurn() UndoReport {
	undoJournal.mu.Lock()
	if len(undoJournal.groups) == 0 {
		undoJournal.mu.Unlock()
		return UndoReport{}
	}
	g := undoJournal.groups[len(undoJournal.groups)-1]
	undoJournal.groups = undoJournal.groups[:len(undoJournal.groups)-1]
	undoJournal.mu.Unlock()

	// First pass: the shell entries name the paths whose pre-turn state is
	// unknowable, and that decision has to be made before any restore — a
	// write that came after a command still cannot undo the command's part.
	unrestorable := map[string]bool{}
	for _, e := range g.entries {
		if e.kind == undoShell {
			unrestorable[e.path] = true
		}
	}

	var rep UndoReport
	reported := map[string]bool{}
	noteUnrestorable := func(p string) {
		if !reported[p] {
			reported[p] = true
			rep.Unrestorable = append(rep.Unrestorable, p)
		}
	}

	for i := len(g.entries) - 1; i >= 0; i-- {
		e := g.entries[i]
		switch e.kind {
		case undoWrite:
			if unrestorable[e.path] {
				noteUnrestorable(e.path)
				continue
			}
			if err := applyContentRestore(e.path, e.before, e.existed); err != nil {
				rep.Errors = append(rep.Errors, e.path+": "+err.Error())
				continue
			}
			recordChange(e.path, e.before, e.before)
			reportRestored(&rep, reported, e.path)
		case undoDelete:
			if unrestorable[e.path] {
				noteUnrestorable(e.path)
				continue
			}
			if err := restoreFile(e.path, e.before); err != nil {
				rep.Errors = append(rep.Errors, e.path+": "+err.Error())
				continue
			}
			recordChange(e.path, e.before, e.before)
			reportRestored(&rep, reported, e.path)
		case undoMove:
			if unrestorable[e.path] || unrestorable[e.dest] {
				noteUnrestorable(e.path)
				continue
			}
			if err := os.Rename(e.dest, e.path); err != nil {
				rep.Errors = append(rep.Errors, e.path+": "+err.Error())
				continue
			}
			recordChange(e.path, e.before, e.before)
			recordChange(e.dest, "", "")
			reportRestored(&rep, reported, e.path)
		case undoShell:
			noteUnrestorable(e.path)
		}
	}
	return rep
}

func reportRestored(rep *UndoReport, seen map[string]bool, p string) {
	if seen[p] {
		return
	}
	seen[p] = true
	rep.Restored = append(rep.Restored, p)
}

// applyContentRestore puts path back to the state a write found it in: content
// when the file existed, absence when it did not.
func applyContentRestore(path, before string, existed bool) error {
	if !existed {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return restoreFile(path, before)
}

// restoreFile writes content to path without counting it: a rewind's own write
// must not re-enter the tally or the journal, or undoing would become an edit
// that can itself be undone and counted.
func restoreFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return writeFileBytes(path, []byte(content))
}
