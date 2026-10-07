package ui

import (
	"fmt"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/i18n"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// The sidebar's three working sections.
//
// They replaced a list of the agent's instrument names, which was the one part
// of the panel that answered no question: the same on every frame of every
// session, unchanged while a turn ran, and nothing the user could do with it.
// The three sections here each answer a question a user asks mid-session — what
// did it change, which branch am I on, is a server down — and each is hidden
// when it has nothing to say, which is the rule PROXY and LAST TOOL already
// follow.
//
// row, value and valueRef are the sidebar's own writers, passed in so a section
// is measured and wrapped exactly like the ones built inline: a section that
// wrapped differently would be one column off the panel's edge, and the framed
// row the trim below measures against would be the wrong height.
//
// valueRef is value with the file a row stands for, and it is what makes a
// changed-file row clickable. It is a separate function rather than a parameter
// on value because every other section's rows name nothing, and a value that
// took a ref would invite a label to be passed one and become a jump target.

// maxChangeRows caps the changed-file list's share of the panel. A session that
// touches fifty files would take the panel over and push the plan and the
// hotkeys off the bottom — and the plan is state the turn is producing, where a
// file list is only a record of what already happened.
const maxChangeRows = 10

// changesSidebar lists the files this session changed, one row per file with that
// file's line counts.
//
// The counts come from the tools, which are the only place that knows a file's
// before and after, so this is the session's real diff rather than a sum of the
// write calls that produced it: a file edited three times shows the one line
// that differs at the end.
//
// A file a *command* changed is listed too, and without counts — see the Touched
// branch below. It is the same section answering a smaller question, not a second
// one: the panel does not know how a file was changed, only that it was, and a
// row that admitted it would rather than invent a number.
//
// The section is hidden until something has changed. A list of nothing is not a
// list of no news — it is a heading and rows spent proving the agent has not
// done anything yet, and its absence is the information.
func (m *uiModel) changesSidebar(w int, row func(lipgloss.Style, string), valueRef func(lipgloss.Style, string, string, codeRef)) {
	files := dmtools.ChangedFilesDetailed()
	if len(files) == 0 {
		return
	}
	row(styleSidebarLabel, i18n.T("CHANGES"))
	shown := files
	if len(shown) > maxChangeRows {
		shown = shown[:maxChangeRows]
	}
	for _, f := range shown {
		// The base name rather than the path: the panel is 27 columns wide, and a
		// path would be trimmed to a separator and a fragment, which names no
		// file. Two files sharing a base name in different directories are the
		// case this loses, and the counts beside them are what make the two
		// tellable apart well enough to pick one.
		text := filepath.Base(f.Path)
		marker := " "
		if f.Touched {
			// The marker leads rather than trails, and that is the whole point of
			// this branch. A trailing "~" would be the first thing truncate cut
			// off the end of a long name — leaving a row that looks exactly like a
			// measured one, on a file whose counts are not known. The marker is the
			// claim, so it goes where a narrow panel cannot drop it.
			//
			// No counts beside it either: "+0 -0" is not a smaller truth than a
			// marker, it is a false one. The tools never saw this file's contents,
			// so they have nothing to count, and printing zeroes would tell the
			// reader the command changed nothing — which is the mistake this whole
			// section exists to have stopped.
			marker = " ± "
		} else {
			text += fmt.Sprintf(" +%d -%d", f.Added, f.Removed)
		}
		// Line 1 rather than a diff position, because the sidebar lists files and
		// not hunks: the honest place to open one is its top. The transcript's
		// diff rows are what carry a line number.
		valueRef(styleHint, marker, truncate(text, w-ansi.StringWidth(marker)),
			codeRef{path: f.Path, line: 1})
	}
	if more := len(files) - len(shown); more > 0 {
		row(styleHint, " "+truncate(fmt.Sprintf(i18n.T("and %d more"), more), w-1))
	}
	// The legend is printed only when a row actually needs it. A marker with
	// nothing to mark is a row spent explaining a symbol that is not on screen,
	// which is the same failure as a heading over nothing.
	if anyTouched(shown) {
		row(styleHint, " "+truncate(i18n.T("± changed by a command"), w-1))
	}
}

// anyTouched reports whether any of these rows has no counts, which is the one
// condition under which the legend has anything to refer to.
func anyTouched(files []dmtools.FileChange) bool {
	for _, f := range files {
		if f.Touched {
			return true
		}
	}
	return false
}

// gitSidebar names the branch the session is working on, and how much of the
// tree is uncommitted.
//
// Both halves are one question — is this work safe to hand over — and the branch
// alone does not answer it: work on the right branch that has never been
// committed is exactly the case that loses a morning.
//
// The section is hidden outside a repository, following PROXY: a GIT heading
// over "not a repository" spends a heading and a row announcing an absence.
//
// The counts are as of the last refresh rather than of this frame. readGitState
// walks the worktree, which is disk I/O, and doing it here would stutter the
// interface on every keystroke in any repository of size — see git_sidebar.go.
func (m *uiModel) gitSidebar(w int, row func(lipgloss.Style, string), value func(lipgloss.Style, string, string)) {
	g := m.gitState
	if !g.ok {
		return
	}
	row(styleSidebarLabel, i18n.T("GIT"))
	value(styleSidebarValue, " ", truncate(g.branch, w-1))
	if !g.walked {
		// The branch is true and the tree is unknown. Saying so beats printing
		// counts that would read as zero, which is a claim about a clean tree
		// the panel never checked.
		row(styleHint, " "+truncate(i18n.T("status unavailable"), w-1))
		return
	}
	// One row per non-zero count, so a clean repository is a branch and nothing
	// else: three rows of zero are noise, and the branch has already said
	// everything there was to say.
	for _, c := range []struct {
		n    int
		text string
	}{
		{g.staged, i18n.T("staged")},
		{g.changed, i18n.T("changed")},
		{g.untracked, i18n.T("new")},
	} {
		if c.n == 0 {
			continue
		}
		row(styleHint, fmt.Sprintf(" %d %s", c.n, truncate(c.text, w-3)))
	}
}

// mcpSidebar lists the configured MCP servers and what came of asking each one.
//
// It exists because a server that could not be asked is otherwise invisible
// after start-up: its tools never appear in a turn, and the note about it lives
// in a status line the next message replaces. A gap in the agent's reach that
// nothing on screen accounts for reads as the agent declining to use a tool it
// has.
//
// Hidden when nothing is configured, which is most sessions — the rule PROXY
// already follows for a session with no proxy.
func (m *uiModel) mcpSidebar(w int, row func(lipgloss.Style, string)) {
	if len(m.mcpStates) == 0 {
		return
	}
	row(styleSidebarLabel, i18n.T("MCP"))
	for _, st := range m.mcpStates {
		switch {
		case st.Err != nil:
			// The error itself is not printed: the MCP SDK's and go-git's are a
			// line or two of transport vocabulary that does not fit 27 columns
			// and would push everything below it off the panel. The cross says
			// the server did not answer; the reason is still in the status line
			// from start-up and in m.StatusMsg, so nothing is lost — it is only
			// no longer the only place it was.
			row(styleTool, " ✗ "+truncate(st.Name, w-3))
		case len(st.Tools) == 0:
			// Reachable and offering nothing is not the same as unreachable, and
			// until ServerState carried both they were one row between them. They
			// are not the same: a server answering with an empty list is
			// misconfigured, and a cross would send the user hunting for a
			// process that is running perfectly well.
			row(styleHint, " "+truncate(fmt.Sprintf("%s %s", st.Name, i18n.T("(no tools)")), w-1))
		default:
			row(styleTool, " ⏺ "+truncate(fmt.Sprintf("%s %d", st.Name, len(st.Tools)), w-3))
		}
	}
}
