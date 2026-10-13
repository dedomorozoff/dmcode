package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/dedomorozoff/dmcode/internal/mcp"
)

// The sidebar's GIT section: a branch, and how much of the tree is uncommitted.
//
// The counting is readGitState's and it is a translation from go-git's status
// codes into three numbers, which is exactly the kind of thing that reads
// correctly and is wrong — an untracked file counted as a modification makes a
// folder of new files read as a folder of edits. So the tests below drive real
// repositories through real states rather than handing readGitState a struct.

// gitRepo makes a repository with one committed file and returns its directory.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A clean repository is a branch and nothing else. Three rows reading zero would
// be noise, and the absence of the counts is what says the tree is clean.
func TestAGitCleanTreeShowsOnlyTheBranch(t *testing.T) {
	dir := gitRepo(t)
	m := framedModel(120, 60, "m")
	m.workDir = dir
	_, st := readGitState(dir)
	m.gitState = st

	s := ansi.Strip(m.sidebarView(60))
	if !strings.Contains(s, "GIT") {
		t.Fatalf("the section is missing:\n%s", s)
	}
	if !strings.Contains(s, st.branch) {
		t.Errorf("the branch %q is not on the panel:\n%s", st.branch, s)
	}
	for _, c := range []string{"staged", "changed", "new"} {
		if strings.Contains(s, c) {
			t.Errorf("a clean tree reports %q:\n%s", c, s)
		}
	}
	if st.dirty() {
		t.Error("a clean repository is reported as dirty")
	}
}

// Outside a repository the section is hidden entirely: a GIT heading over "not a
// repository" spends a heading and a row announcing an absence, which is the
// rule PROXY already follows.
func TestNoGitSectionOutsideARepository(t *testing.T) {
	m := framedModel(120, 60, "m")
	m.workDir = t.TempDir()
	_, st := readGitState(m.workDir)
	m.gitState = st

	if s := ansi.Strip(m.sidebarView(60)); strings.Contains(s, "GIT") {
		t.Errorf("a folder that is not a repository still shows the section:\n%s", s)
	}
}

// The three counts are separate because "3 changed" is ambiguous between work
// ready to commit and work that is not, and telling them apart is the question a
// user opens the git panel to ask.
func TestTheGitCountsSeparateStagedFromChanged(t *testing.T) {
	dir := gitRepo(t)
	m := framedModel(120, 60, "m")
	m.workDir = dir

	// A modified tracked file, plus a new one nobody has added.
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("a\nB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, st := readGitState(dir)

	if st.changed != 1 {
		t.Errorf("changed = %d, want 1 for the modified tracked file", st.changed)
	}
	if st.untracked != 1 {
		t.Errorf("untracked = %d, want 1 for the new file", st.untracked)
	}
	if st.staged != 0 {
		t.Errorf("staged = %d, want 0 — nothing was added to the index", st.staged)
	}
	// The untracked file is not an edit. Nothing about it was changed; it simply
	// arrived, and counting it as a modification would make a folder of new files
	// read as a folder of edits.
	if st.changed+st.untracked != 2 {
		t.Errorf("changed+untracked = %d, want the two files counted once each", st.changed+st.untracked)
	}

	m.gitState = st
	s := ansi.Strip(m.sidebarView(60))
	if !strings.Contains(s, "1 changed") {
		t.Errorf("the modified file is not reported as changed:\n%s", s)
	}
	if !strings.Contains(s, "1 new") {
		t.Errorf("the new file is not reported as new:\n%s", s)
	}
	if !st.dirty() {
		t.Error("a tree with changes is reported as clean")
	}
}

// A branch that could not be walked is a different fact from a clean tree, and
// printing zeros would be a claim the panel never checked. So the section says
// so rather than saying nothing is wrong.
func TestAGitWalkFailureIsNotACleanTree(t *testing.T) {
	dir := gitRepo(t)
	// A repository whose index cannot be read: .git/index replaced by a directory.
	// Opening the repository and reading the branch both still work; the worktree
	// walk is what fails.
	if err := os.RemoveAll(filepath.Join(dir, ".git", "index")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git", "index"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, st := readGitState(dir)
	if !st.ok || st.branch == "" {
		t.Skipf("this go-git still reads a branch from a broken index: %+v", st)
	}
	if st.walked {
		t.Skip("this go-git read the index through a directory; nothing to assert")
	}

	m := framedModel(120, 60, "m")
	m.workDir = dir
	m.gitState = st
	s := ansi.Strip(m.sidebarView(60))
	if !strings.Contains(s, "status unavailable") {
		t.Errorf("an unreadable tree is not reported as such:\n%s", s)
	}
	if st.dirty() {
		t.Error("an unknown tree must not claim to be dirty")
	}
}

// The refresh is asked for rather than performed inline, because reading a
// repository is disk I/O and the sidebar is drawn on the frame path. The message
// it sends is what lands the snapshot on the model, so a test drives that
// message rather than a command, and the command is checked for being a command.
func TestTheGitRefreshArrivesAsAMessage(t *testing.T) {
	dir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := framedModel(120, 60, "m")
	m.workDir = dir
	m.gitState = gitState{}

	cmd := m.gitStatusCmd()
	if cmd == nil {
		t.Fatal("the refresh produced no command")
	}
	msg, ok := cmd().(gitStatusMsg)
	if !ok {
		t.Fatalf("the refresh sent %T, want gitStatusMsg", msg)
	}
	if !msg.state.ok || msg.state.branch == "" {
		t.Fatalf("the snapshot has no repository in it: %+v", msg.state)
	}

	m.Update(msg)
	if !strings.Contains(ansi.Strip(m.sidebarView(60)), "GIT") {
		t.Error("the section is still missing after the refresh landed")
	}
}

// A /cd out of a repository must not leave the old folder's branch on screen: it
// would be a claim about where the agent is working, and the agent is not there.
func TestChangingFolderDropsTheOldBranch(t *testing.T) {
	base := gitRepo(t)
	outside := t.TempDir()
	// The test starts in base because changeDir moves the process there, and the
	// helper puts it back at the end — which the temporary directories need, since
	// Windows refuses to remove the directory a process is standing in.
	chdir(t, base)
	m := framedModel(120, 60, "m")
	m.workDir = base
	m.gitState = gitState{ok: true, branch: "main", walked: true}

	m.changeDir(outside)
	chdir(t, base)

	if m.gitState.ok {
		t.Error("the state from the folder just left is still on the model")
	}
	if s := ansi.Strip(m.sidebarView(60)); strings.Contains(s, "GIT") {
		t.Errorf("the old repository's branch is still drawn:\n%s", s)
	}
}

// The MCP section exists because a server that could not be asked is otherwise
// invisible: its tools never appear in a turn, and the note about it lives in a
// status line the next message replaces.
func TestTheMcpSectionListsEveryServer(t *testing.T) {
	m := framedModel(120, 60, "m")
	m.mcpStates = []mcp.ServerState{
		{Name: "files", Tools: []string{"read", "write", "list"}},
		{Name: "broken", Err: errors.New("connection refused")},
		{Name: "empty"},
	}

	s := ansi.Strip(m.sidebarView(60))
	for _, want := range []string{"MCP", "files", "broken", "empty", "3", "(no tools)"} {
		if !strings.Contains(s, want) {
			t.Errorf("the section does not report %q:\n%s", want, s)
		}
	}
	// Reachable and offering nothing is not unreachable. A cross on a server that
	// answered with an empty list would send the user hunting for a process that
	// is running perfectly well.
	if strings.Contains(s, "✗ empty") {
		t.Errorf("a server that answered is marked as one that did not:\n%s", s)
	}
	if !strings.Contains(s, "✗ broken") {
		t.Errorf("the server that did not answer is not marked:\n%s", s)
	}
}

// Most sessions configure no MCP servers at all, and a section heading over
// nothing is furniture.
func TestNoMcpSectionWithoutServers(t *testing.T) {
	m := framedModel(120, 60, "m")
	m.mcpStates = nil
	if s := ansi.Strip(m.sidebarView(60)); strings.Contains(s, "MCP") {
		t.Errorf("a session with no servers still shows the section:\n%s", s)
	}
}

// Every section on the panel is hidden when it has nothing to say, and each of
// them was asked for by name. This is the test that would fail if a new section
// were added that printed zeros on a fresh session — the failure PROXY, LAST
// TOOL, GIT, CHANGES and MCP all had to be designed around.
func TestAFreshPanelIsNotAFurniture(t *testing.T) {
	m := framedModel(120, 60, "m")
	// framedModel is built adversarially — a long last tool, a busy status — so
	// the two fields that make a section appear have to be cleared for this to
	// be a panel about a session that has done nothing.
	m.lastTool = ""
	m.mcpStates = nil
	m.gitState = gitState{}
	s := ansi.Strip(m.sidebarView(60))

	for _, absent := range []string{"PROXY", "GIT", "MCP", "CHANGES", "LAST TOOL", "PLAN", "TOOLS", "HOTKEYS"} {
		if strings.Contains(s, absent) {
			t.Errorf("%s is on a panel with nothing to report:\n%s", absent, s)
		}
	}
	// CONTEXT is deliberately in neither list: it appears only once an endpoint
	// has reported a prompt size, which is the same rule, and a model with no
	// window has nothing to draw.
	for _, present := range []string{"MODEL", "SESSION", "FOLDER"} {
		if !strings.Contains(s, present) {
			t.Errorf("%s went missing from the panel:\n%s", present, s)
		}
	}
}

// A section that printed nothing must not leave a blank line behind it.
//
// This was invisible while the hotkeys filled the bottom of the panel, and it is
// the same failure the sections themselves were: a separator standing in for
// something that is not there. A panel where three hidden sections leave three
// gaps reads as three sections that failed to draw, which is a claim the panel
// then makes with every keystroke.
func TestAHiddenSectionLeavesNoGapBehindIt(t *testing.T) {
	m := framedModel(120, 60, "m")
	m.lastTool = ""
	m.gitState = gitState{}
	m.mcpStates = nil
	cleanTally(t)

	// The three hidden sections are LAST TOOL, GIT and MCP, and the plan is empty
	// too — four of them, in the order the panel draws them, between FOLDER and
	// the bottom. So FOLDER's value row is the one row that must be followed
	// directly by the end of the body.
	rows := sidebarRows(t, m)
	folder := -1
	for i, r := range rows {
		if strings.Contains(r, "FOLDER") {
			folder = i
			break
		}
	}
	if folder < 0 {
		t.Fatal("FOLDER is not on the panel")
	}
	if got := strings.TrimSpace(rows[folder+1]); got == "" {
		t.Errorf("the FOLDER section has no value under its heading:\n%s", strings.Join(rows, "\n"))
	}
	if got := strings.TrimSpace(rows[folder+2]); got != "" {
		t.Errorf("a row follows FOLDER's value where four hidden sections should have left nothing: %q", got)
	}
}
