package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/editor/vcs"
)

// The repository facts the sidebar's GIT section shows.
//
// They come from go-git, which reads .git and walks the worktree to work out
// what is dirty. That is disk I/O on a path that runs on every frame, so it is
// never done there: gitStatusCmd answers on a Bubble Tea goroutine and sends the
// result back as a message, and the panel draws the snapshot it was handed. A
// sidebar that asked StatusFiles() inside View would stutter for as long as the
// walk took, on every keystroke, in a repository of any size.
//
// The consequence worth stating: the counts are as of the last refresh and can
// be behind the tree. gitState.gitDirty is derived from them rather than stored,
// because a fourth field that could disagree with the three it summarises is a
// fourth thing to keep right.
type gitState struct {
	// ok is false when the folder is not in a repository at all, or the
	// repository could not be opened. The section is then hidden entirely: a
	// GIT heading over "not a repository" is a heading and a row spent
	// announcing an absence, which is the rule PROXY already follows.
	ok bool
	// branch is the current branch name, or a short SHA on a detached HEAD.
	branch string
	// staged, changed and untracked count the dirty tree. They are three
	// numbers rather than one because "3 changed" is ambiguous between work
	// that is ready to commit and work that is not, and telling those apart is
	// the question a user opens the git panel to ask.
	staged    int
	changed   int
	untracked int
	// walked is false when the branch came back but the worktree walk did not,
	// so the section shows the branch alone rather than nothing at all.
	walked bool
}

// dirty reports whether anything in the tree is uncommitted, which is the one
// bit the heading is marked with.
func (g gitState) dirty() bool { return g.staged+g.changed+g.untracked > 0 }

// gitStatusMsg carries a repository snapshot from the command that took it.
type gitStatusMsg struct{ state gitState }

// gitStatusCmd is the model's own refresh: the folder it is currently scoped to.
// It is a method so the three callers — Init, the end of a turn and /cd — cannot
// disagree about which directory is being asked about, which is the failure a
// caller passing a stale path would produce rather than an error.
func (m *uiModel) gitStatusCmd() tea.Cmd { return gitStatusCmd(m.workDir) }

// gitStatusCmd opens the repository at dir and reads its branch and dirty counts.
//
// A directory that is not in a repository is not an error: most sessions are, and
// one that finds nothing should show no GIT section rather than a message about
// it. A repository that exists and cannot be walked is not an error either — the
// branch is still true, and a section showing the branch alone is more use than
// no section.
func gitStatusCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		_, st := readGitState(dir)
		return gitStatusMsg{state: st}
	}
}

// readGitState is the one place the snapshot is measured. It opens the
// repository and walks the worktree, and it is shared by the command above and
// the startup path below because two copies of a three-count tally would be two
// answers to "how dirty is it" — and the panel cannot tell which one it is
// looking at.
func readGitState(dir string) (*vcs.Repo, gitState) {
	repo, err := vcs.Open(dir)
	if err != nil {
		return nil, gitState{}
	}
	st := gitState{ok: true, branch: repo.Branch()}
	files, err := repo.StatusFiles()
	if err != nil {
		return repo, st
	}
	st.walked = true
	for _, f := range files {
		// An untracked file is not "changed": nothing about it was changed, it
		// simply arrived. Counting it as a modification would make a folder of
		// new files read as a folder of edits.
		if f.Worktree == vcs.StatusUntracked || f.Staging == vcs.StatusUntracked {
			st.untracked++
			continue
		}
		if f.IsStaged() {
			st.staged++
		}
		if f.Worktree != vcs.StatusUnmodified {
			st.changed++
		}
	}
	return repo, st
}
