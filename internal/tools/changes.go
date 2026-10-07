package tools

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// The session's tally of what the agent actually changed, for the sidebar.
//
// It lives here rather than in the UI because this is the only place that knows
// the before and after of a file: the UI sees a tool call and its result as
// opaque text, and reconstructing a diff from those would be guesswork. Counting
// it as the write happens is also the only way to be right when the same file is
// edited several times in one session — the number then reflects the file as it
// now stands against how it stood at the start, not a sum of every intermediate
// state.
var changes = &changeSet{files: map[string]*fileChange{}}

// fileChange remembers the content a file had the first time this session
// touched it. Every later write is diffed against that baseline rather than
// against the previous write, so editing the same line three times reports the
// one line that actually differs at the end, not three separate edits.
type fileChange struct {
	baseline string
	added    int
	removed  int
	// touched is set when the session saw this file change without holding its
	// content from before the change — a shell command, which reports a path
	// and nothing else.
	//
	// It is the reason added and removed can both be zero while the file is
	// genuinely different: the two numbers are not known, not zero, and the
	// panel must not print a zero that reads as "nothing happened here". The
	// flag is sticky — once the counts are lost for a file, a later write cannot
	// recover them, because the baseline the write diffs against is the content
	// *after* whatever the command did.
	touched bool
}

type changeSet struct {
	mu    sync.Mutex
	files map[string]*fileChange
}

// ChangeStats is the session's change tally, as the sidebar shows it.
type ChangeStats struct {
	// Files is how many distinct files the agent has touched.
	Files int
	// Added and Removed are line counts, summed over every touched file.
	Added   int
	Removed int
	// Unknown is how many of those files have no line counts — they changed
	// somewhere the tools did not see the contents of. It is carried beside
	// Files rather than folded into it so "12 files, 3 unmeasured" is one honest
	// number wider than "12 files".
	Unknown int
}

// Changes returns the tally as it stands right now.
func Changes() ChangeStats {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	s := ChangeStats{Files: len(changes.files)}
	for _, f := range changes.files {
		if f.touched {
			s.Unknown++
			continue
		}
		s.Added += f.added
		s.Removed += f.removed
	}
	return s
}

// ChangedFiles names the files the agent has touched, for /debug.
func ChangedFiles() []string {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	out := make([]string, 0, len(changes.files))
	for p := range changes.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// FileChange is one file's line of the session tally: where it is, and what the
// session did to it.
//
// It is a separate type from ChangeStats rather than a map keyed by path
// because the sidebar draws one row per file and wants the two counts beside
// the name — a tally that answers "how much" but not "where" makes the caller
// re-join the two, and two joins of two answers is where they drift.
type FileChange struct {
	// Path is the file's absolute path, as the tools recorded it.
	Path string
	// Added and Removed are line counts against the file's pre-session baseline.
	// Both are zero and must not be read as zero when Touched is set.
	Added   int
	Removed int
	// Touched reports that the file changed where the tools did not see its
	// contents — a shell command, which names a path and nothing more.
	//
	// This is why the two counts above cannot be trusted on their own: a
	// "+0 -0" row for a file that a command rewrote is a lie told by an absence,
	// which is the one kind of lie a sidebar full of numbers cannot afford. A
	// caller must check Touched before printing the counts at all.
	Touched bool
}

// ChangedFilesDetailed returns one entry per file the agent has touched, sorted
// by path so the sidebar's list is stable between frames — a list that reshuffled
// on every turn would be unreadable and would make a click land somewhere else
// than the row it was taken from.
//
// A file whose net change came back to zero is left out: the session touched it
// and undid itself, and a row reading "+0 -0" is a statement about work that is
// not in the tree. It stays in ChangedFiles, which answers what was touched
// rather than what changed.
//
// A file changed only by a command *is* in the list, whatever its counts say —
// and its counts are the ones the tools never got to see. Dropping it on the
// zero test is exactly the failure this package exists to avoid: the file is
// different on disk, and a panel that cannot see that is the panel the sed -i
// case was reported about.
func ChangedFilesDetailed() []FileChange {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	out := make([]FileChange, 0, len(changes.files))
	for p, f := range changes.files {
		if !f.touched && f.added == 0 && f.removed == 0 {
			continue
		}
		out = append(out, FileChange{
			Path:    p,
			Added:   f.added,
			Removed: f.removed,
			Touched: f.touched,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// RecordTouched notes that a file changed without the tools seeing what it held
// before or after — the case run_command creates, since a shell reports a path
// and nothing about its contents.
//
// It takes the path as the tools would resolve it, so the same file touched by a
// command and later by write_file is one row and not two. An existing entry's
// counts are kept as they are and only the flag is set: a command cannot improve
// what is already known, and overwriting measured numbers with a flag would throw
// away the one part of the answer that was true.
func RecordTouched(path string) {
	recordTouched(path)
}

func recordTouched(path string) {
	changes.mu.Lock()
	defer changes.mu.Unlock()

	if f, ok := changes.files[path]; ok {
		f.touched = true
		return
	}
	changes.files[path] = &fileChange{touched: true}
}

// ResetChanges clears the tally and the undo journal. /new starts a new
// session, and carrying the previous session's numbers into it would report
// changes the new one never made — while carrying its undo groups would let a
// rewind reach back and take apart a different conversation's work.
func ResetChanges() {
	changes.mu.Lock()
	changes.files = map[string]*fileChange{}
	changes.mu.Unlock()
	undoJournal.mu.Lock()
	undoJournal.groups = nil
	undoJournal.mu.Unlock()
}

// recordChange adds one file's edit to the tally. before is the content as it
// was and after the content as it now stands; either may be empty, which is what
// a file that did not exist before looks like.
//
// The two are compared as whole files rather than accumulated per call, so
// editing the same line twice in a session nets out to the one line that actually
// differs at the end rather than counting both edits.
// RecordChange adds one file's edit to the session tally, comparing before and
// after as whole files. writeFileAtomic calls it for every write; it is exported
// so a caller that changed a file by some other route can still report it, and
// so the tally can be exercised from another package's tests without reaching
// into this one's internals.
func RecordChange(path, before, after string) {
	recordChange(path, before, after)
}

func recordChange(path, before, after string) {
	changes.mu.Lock()
	defer changes.mu.Unlock()

	f, ok := changes.files[path]
	if !ok {
		// First touch: this is what the file looked like before the session got
		// to it, and it is the baseline every later write is measured against.
		added, removed := lineDelta(before, after)
		if added == 0 && removed == 0 {
			return
		}
		changes.files[path] = &fileChange{baseline: before, added: added, removed: removed}
		return
	}
	// Diffed against the baseline, not against the previous write: that is what
	// makes three successive edits to one line report one line. Summing per-write
	// deltas would report three, and the sidebar would be counting the agent's
	// steps rather than its result.
	added, removed := lineDelta(f.baseline, after)
	f.added, f.removed = added, removed
	// The touched flag is deliberately not cleared here, and this is the whole
	// reason it exists. A command changed this file before the write, and the
	// content now called `baseline` is the content *after* that change — so a
	// delta measured from it describes this write alone and nothing before it.
	// Clearing the flag would turn "we do not know how much this file changed"
	// into a confident wrong number, which is strictly worse than an absence:
	// the reader has no way to tell which rows to distrust.
	//
	// The counts themselves are still updated, because they are true of the last
	// write. A row marked Touched says "changed, and at least this much since the
	// last write" rather than pretending to be the whole story.
}

// lineDelta counts how many lines differ between two versions of a file.
//
// It trims the common prefix and suffix and counts what is left, which is exact
// for the appends and single-block edits an agent makes and cheap for a file of
// any size. A full diff would be more precise on a heavily rearranged file, but
// this is a number in a sidebar, not a patch to apply, and a quadratic diff over
// a large file on every write is not worth the accuracy.
func lineDelta(before, after string) (added, removed int) {
	b := splitLines(before)
	a := splitLines(after)

	// A file that exists only on one side is wholly added or wholly removed, and
	// there is no shared prefix or suffix to trim off that would understate it.
	if before == "" {
		return len(a), 0
	}
	if after == "" {
		return 0, len(b)
	}

	pre := 0
	for pre < len(b) && pre < len(a) && b[pre] == a[pre] {
		pre++
	}
	suf := 0
	for suf < len(b)-pre && suf < len(a)-pre && b[len(b)-1-suf] == a[len(a)-1-suf] {
		suf++
	}
	return len(a) - pre - suf, len(b) - pre - suf
}

// splitLines splits content into lines, ignoring the trailing newline so that
// "a\n" and "a" are the same single line rather than one and none.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// diffMaxRows caps the per-write diff the transcript draws. A model replacing
// a whole file in one write_file can produce hundreds of lines, and a diff
// block that swallows the transcript defeats the point of showing it.
const diffMaxRows = 40

// DiffBlock renders what one write changed, the way the transcript shows it:
// the file name on a header row, then the removed and the added lines with
// their position in the file and a -/+ marker — line numbers from the old
// text for removals and from the new one for additions, which is what makes
// a replacement read as the same line twice. The block is a piece of the
// editor on the transcript: numbers in the gutter, code beside them. It is
// the view of this single call — before against after — not the session
// tally's net change against its baseline. The output is plain text; the
// transcript's renderer owns the colours. Empty when the two versions agree.
func DiffBlock(path, before, after string) string {
	b := splitLines(before)
	a := splitLines(after)
	pre := 0
	for pre < len(b) && pre < len(a) && b[pre] == a[pre] {
		pre++
	}
	suf := 0
	for suf < len(b)-pre && suf < len(a)-pre && b[len(b)-1-suf] == a[len(a)-1-suf] {
		suf++
	}
	del := b[pre : len(b)-suf]
	add := a[pre : len(a)-suf]
	if len(del) == 0 && len(add) == 0 {
		return ""
	}
	rows := make([]string, 0, len(del)+len(add)+2)
	rows = append(rows, "── "+path)
	oldN, newN := pre+1, pre+1
	for _, l := range del {
		rows = append(rows, fmt.Sprintf("-%d: %s", oldN, l))
		oldN++
	}
	for _, l := range add {
		rows = append(rows, fmt.Sprintf("+%d: %s", newN, l))
		newN++
	}
	if len(rows)-1 > diffMaxRows {
		rows = rows[:diffMaxRows+1]
		rows = append(rows, fmt.Sprintf("… (+%d more lines)", len(del)+len(add)-diffMaxRows))
	}
	return strings.Join(rows, "\n")
}

// readIfExists returns a file's content, or "" when it does not exist. A read
// error other than "not there" is reported, because silently treating an
// unreadable file as empty would count a whole file as newly added.
func readIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}
