package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Watching what a shell command did to the tree.
//
// run_command is the one instrument that can change the workspace without saying
// what it changed: `sed -i`, `>` redirection, `rm`, `git checkout`, a build step
// that rewrites generated files. The agent's own tools report a path and a diff
// because that is all they do, so the tally in changes.go can be exact. A shell
// reports nothing, and before this file the answer was that the panel did not
// know — which is the failure the whole `CHANGES` section exists to prevent: an
// agent rewrites two hundred files and the sidebar says it changed two.
//
// What this can honestly know
//
// A snapshot before and after gives, for each file, its size and its mtime. That
// is enough to answer "did this file change" and it is not enough to answer "how
// many lines". Those are different questions, and the tally has room for only
// one honest answer to each: a file a command touched is recorded as touched, with
// its counts marked unknown, rather than counted as "+0 -0".
//
// The alternative — reading every file's content on both sides to compute a real
// diff — is what makes this tempting and wrong. It turns a keystroke's worth of
// tool into a full copy of the workspace twice over, on a path that runs on every
// `run_command` in the session, and the expensive version is the one that would
// be wrong anyway about the files it cannot read.
//
// Why mtime and size rather than either alone
//
// Size alone misses an edit that replaces a line with one of the same length.
// Mtime alone misses a file whose content changed while its timestamp did not,
// which is rare but real: `touch -t`, a checkout that preserves times, and any
// filesystem with coarse timestamp granularity. Comparing both means a change is
// reported unless *both* facts stayed identical, and the two facts fail
// differently, so their overlap is small.
//
// Coarse timestamps are the one case this cannot fix, and it is worth naming: on a
// filesystem with one-second mtime granularity, a file rewritten within the same
// second as the command started can be missed if its size also matched. Every
// mainstream local filesystem has fine-grained timestamps; the report is honest
// about the limit rather than pretending it does not exist.

// snapshot is what one pass over the tree remembers about a file: the two facts
// that answer "did this change" without its contents.
type snapshot struct {
	// files maps each path to the two facts about it. The value is a fileStamp
	// rather than a pointer: there is one per file in the tree, and a pointer
	// each would be an allocation per file on a path that runs on every command.
	files map[string]fileStamp
	// whole is false when the pass hit its bound or could not read the root,
	// which is the one thing that makes a snapshot unfit to diff against another.
	whole bool
}

// fileStamp is the pair that answers "did this change" without the contents.
type fileStamp struct {
	size    int64
	modTime time.Time
}

// watchMaxFiles bounds one snapshot pass.
//
// It exists because a snapshot is taken on a path the user is waiting on, and a
// walk of a hundred thousand files on a slow disk is long enough to be noticed.
// The bound is the same one list_dir and grep use, so the workspace's answer to
// "how much of this tree do we look at" is one number rather than three.
//
// A pass that hits the bound is marked incomplete, and an incomplete snapshot is
// never diffed into claims of precision: see snapshot.complete.
const watchMaxFiles = maxScannedItems

// takeSnapshot walks the tree under root and records the size and mtime of every
// file in it.
//
// The ignored-directory list is the same one the other walks use, and reusing it
// rather than keeping a second is the point: a snapshot that descended into
// node_modules would be enormous, and one that skipped less than grep does would
// report files the user cannot see in any other listing. One list means the
// workspace, the listings and the tally all describe the same tree.
//
// symlinks are recorded but never followed. A link's own size and mtime are
// facts about the link, and following one is how a snapshot would report the
// contents of a file outside the workspace — which the boundary exists to stop,
// and which is exactly what walkDir's entries are for instead.
func takeSnapshot(root string) snapshot {
	s := snapshot{files: make(map[string]fileStamp), whole: true}
	if root == "" {
		return s
	}
	// A root that is not a readable directory is not a complete picture of
	// anything, and saying otherwise is what makes two empty snapshots diff to
	// "nothing changed" — the same confident wrong answer the flag exists to
	// prevent, reached from the other end.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return snapshot{files: s.files, whole: false}
	}
	walkDir(root, func(path string, info fs.FileInfo) {
		if len(s.files) >= watchMaxFiles {
			// The bound is reached rather than exceeded, so the last file that
			// fitted is the last one recorded. What the flag refuses is the
			// comparison itself, not the partial walk.
			s.whole = false
			return
		}
		s.files[path] = fileStamp{size: info.Size(), modTime: info.ModTime()}
	})
	return s
}

// wholeTree reports whether the pass saw the whole tree.
//
// It is false only when the walk hit its bound or could not read the root at all,
// and it gates the diff: two incomplete snapshots of two different prefixes of a
// tree would report every file the prefix did not reach as deleted, which is a
// spectacular lie rather than a small one. So an incomplete pair contributes
// nothing at all, and the command's result says its effect went unrecorded.
func (s snapshot) wholeTree() bool { return s.whole }

// snapshotEdited lists the files that differ between two snapshots.
//
// A file that appeared is reported alongside one that was edited, because in both
// cases the tools have no idea what its contents were and know only that it is not
// what it was. That is the whole of what a shell can tell us, and it is what the
// tally records for a command: the fact, marked as unmeasured.
func snapshotEdited(before, after snapshot) (edited []string) {
	for path, now := range after.files {
		if was, ok := before.files[path]; !ok {
			edited = append(edited, path)
		} else if was.size != now.size || !was.modTime.Equal(now.modTime) {
			edited = append(edited, path)
		}
	}
	return edited
}

// vanished lists the files that were there before and are not there now.
//
// They are reported separately from changed because a deleted file cannot be
// opened: the panel offers a click that opens the file, and offering one on a
// path that no longer exists is a dead control. The tally records it, the panel
// marks it, and the click says the file is gone.
func snapshotVanished(before, after snapshot) (vanished []string) {
	for path := range before.files {
		if _, ok := after.files[path]; !ok {
			vanished = append(vanished, path)
		}
	}
	return vanished
}

// recordCommandChanges folds a command's effect on the tree into the tally.
//
// It is the whole seam between the watcher and changes.go, and it is deliberately
// the only writer that can mark a file touched without knowing its diff: every
// other route into the tally has both halves of the file in hand.
//
// before and after are the snapshots taken either side of the command. Either may
// be incomplete, in which case nothing is recorded at all: the honest answer to
// "what changed" when the only available answer is a guess is "I did not look",
// and a partial diff is the guess.
func recordCommandChanges(before, after snapshot) {
	if !before.whole || !after.whole {
		return
	}
	for _, p := range snapshotEdited(before, after) {
		recordTouched(p)
		RecordShellChange(p)
	}
	// A deleted file is recorded too. It is not in the tree any more, so the
	// tally's "what changed" answer has to include it — a session that removed a
	// file did change the workspace, and a panel that only counts what is still
	// there understates it in the one direction that cannot be undone by the
	// user typing something.
	for _, p := range snapshotVanished(before, after) {
		recordTouched(p)
		RecordShellChange(p)
	}
}

// noteUnwatched is what a command's result says when its effect on the tree could
// not be recorded. It is a sentence rather than a bool because the reader needs to
// know that the panel is not covering this command, and "true" tells them nothing
// they can act on.
const noteUnwatched = "\n[dmcode could not watch this command's effect on the workspace — the tree is too large to compare, so any files it changed are not in the panel]"

// commandEffect runs fn and records whatever it did to the tree under root.
//
// The recording happens before the result is returned rather than in a defer
// around fn, because fn's own error is the caller's to interpret and a defer
// here would fire on the panic path too — recording a tree nobody finished
// touching. Every *successful return* of fn is recorded, which is the case that
// matters: `sed -i 's/x/y/' *.go && false` changes two hundred files and then
// exits non-zero, and a tally that only recorded commands that reported success
// would be wrong precisely when something went wrong, which is the moment a user
// most wants to know what was touched.
//
// The recording is skipped when the workspace root is unset, which is what a
// caller that never called SetRoot looks like: there is no tree to compare, and
// guessing at the process directory would put the tally somewhere other than the
// files the other tools are confined to.
func commandEffect(root string, fn func() ([]byte, error)) ([]byte, error) {
	if root == "" {
		return fn()
	}
	before := takeSnapshot(root)
	out, err := fn()
	after := takeSnapshot(root)
	recordCommandChanges(before, after)
	if !before.wholeTree() || !after.wholeTree() {
		// Said to the model as well as kept off the panel, because the model is
		// the one that will go on to claim it changed nothing: it has just been
		// told a shell command did something and been given no way to find out
		// what, and this is the sentence that closes the gap.
		out = append(out, []byte(noteUnwatched)...)
	}
	return out, err
}

// walkDir calls fn for every regular file under root, without following symlinks
// and without descending into the ignored directories.
//
// It is written out rather than shared with list_dir's walk because that one
// returns entries to a paginated caller and this one wants a file's own metadata;
// the one thing they must agree on is isIgnoredDir, and they call the same one.
func walkDir(root string, fn func(path string, info fs.FileInfo)) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped rather than fatal. The tools'
			// own walks do the same, and for the same reason: one permission
			// denied in a tree is not a reason to answer nothing about the rest
			// of it.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// A symlink is recorded, not followed: the link's own facts are facts,
		// and following it would read a file the boundary does not allow.
		if info.Mode()&os.ModeSymlink != 0 {
			fn(path, info)
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		fn(path, info)
		return nil
	})
}
