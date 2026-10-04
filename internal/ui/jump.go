package ui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// Going from a change in the transcript to that change in the file.
//
// The transcript draws what a write changed as a block with the file's name and
// real line numbers in the gutter, and until now those numbers were decoration:
// the only thing that could act on them was the eye. Clicking a row and having
// the editor open the file there is the obvious thing the block should do, and
// the numbers are already in the right place to support it.
//
// Two things make it more than an index lookup, and both are why this is its
// own file rather than three lines inside the mouse handler:
//
//   - The renderer throws the line number away. By the time a click arrives the
//     row is a styled, gutter-numbered, maybe-wrapped, maybe-truncated string,
//     so the number has to be kept as a value beside it (codeRef) rather than
//     read back out of the drawn row. See codeRef's comment for what re-deriving
//     it would mean.
//
//   - A screen row is not a transcript row. The frame puts the header, the chat
//     panel's own border, and whatever the viewport is scrolled to in between,
//     and any of the three changes while the user reads. transcriptTop and
//     vp.YOffset are that arithmetic, and TestClickOnADiffRowOpensTheFile measures
//     it against a frame that was actually built rather than trusting it.

// transcriptTop is the frame row the transcript's first row is drawn on: the
// header when it is on screen, and the chat panel's own top border always.
//
// A test builds a real frame, finds the change row in it, and clicks there; a
// number that was merely plausible would fail that test rather than pass it.
func (m *uiModel) transcriptTop() int {
	top := panelTopBorder
	if m.headerVisible() {
		top += headerHeight
	}
	return top
}

// codeRefAt resolves a frame row to the transcript row behind it and to the place
// in a file that row stands for. A row that is not a change row — a reply, the
// input box, the status bar, a diff block's header — resolves to nothing.
//
// The row index is returned alongside because the walk in nextChange needs to
// remember where it got to, and re-finding the same place by searching for the
// text of a ref would be the same mistake as reading the line number back out of
// a drawn row.
func (m *uiModel) codeRefAt(y int) (int, codeRef, bool) {
	i := y - m.transcriptTop() + m.vp.YOffset()
	if i < 0 || i >= len(m.rowRefs) {
		return 0, codeRef{}, false
	}
	ref := m.rowRefs[i]
	return i, ref, ref.ok()
}

// refAtPointer is codeRefAt for a mouse position, with the column checked too.
// A change row is inside the chat panel, and a click to the right of it belongs
// to the sidebar — which has its own targets, and none of them is a diff.
func (m *uiModel) refAtPointer(x, y int) (int, codeRef, bool) {
	if x < 0 || x >= m.chatBoxWidth() {
		return 0, codeRef{}, false
	}
	return m.codeRefAt(y)
}

// nextChange is the change row the walk should go to next: the one after the
// last one it went to, and — before it has gone anywhere — the first change in
// the transcript. It comes back round at the end rather than stopping dead there.
//
// It starts at the top rather than at the view for a reason that is not
// tidiness: the transcript is chronological, and a user who has just watched the
// newest change arrive is standing exactly where a walk seeded from the view
// would begin. Pressing the key would hand back the change already on screen and
// send the next press to the oldest one, which is the tour backwards.
//
// jumpFrom is the whole of the walk's memory. It has to be a remembered index
// rather than the scroll offset: two presses in a row do not move the view, so a
// walk that re-derived its position from where the user is looking would hand
// back the same change for ever.
func (m *uiModel) nextChange() (int, codeRef, bool) {
	n := len(m.rowRefs)
	start := min(max(m.jumpFrom, 0), n)
	for i := start; i < n; i++ {
		if ref := m.rowRefs[i]; ref.ok() {
			return i, ref, true
		}
	}
	for i := 0; i < start; i++ {
		if ref := m.rowRefs[i]; ref.ok() {
			return i, ref, true
		}
	}
	return 0, codeRef{}, false
}

// jumpToRef opens ref in the editor and switches to it.
//
// A zero ref is not an error: a plain click on a reply keeps meaning nothing,
// which is the rule that keeps a click from being a copy of one character. The
// place is handed over as pendingJump rather than applied here because openEditor
// may only be *asked* to flip the mode — a click that arrived through the editor's
// own mouse handling is inside the editor's Update, and a direct flip would be
// clobbered by the copy it returns. enterEditor applies it in both cases.
func (m *uiModel) jumpToRef(ref codeRef) tea.Cmd {
	if !ref.ok() {
		return nil
	}
	m.pendingJump = &ref
	return m.openEditor()
}

// applyJump puts the editor on the place the transcript named, and says where it
// went in the chat's own status line — which is the one that survives the switch,
// since the user arrives looking at the editor and the chat's row is what they
// come back to.
func (m *uiModel) applyJump(ref codeRef) {
	if m.ed == nil || !ref.ok() {
		return
	}
	if !m.ed.OpenAt(ref.path, ref.line) {
		m.statusText = fmt.Sprintf(i18n.T("cannot open %s"), ref.path)
		return
	}
	m.statusText = ref.String()
}

// jumpToNextChange is the key's half of the feature: the change the walk has not
// been to yet, for a user who wants to see what the agent did without aiming at
// a row.
func (m *uiModel) jumpToNextChange() tea.Cmd {
	// The index belongs to the rendered content, so it is refreshed before it is
	// read. A key can arrive before anything has drawn the transcript since the
	// last message landed, and an index one message stale would send the user to
	// a line the transcript no longer shows.
	m.syncVP()
	i, ref, ok := m.nextChange()
	if !ok {
		m.jumpFrom = -1
		m.statusText = i18n.T("nothing changed yet")
		return nil
	}
	m.jumpFrom = i + 1
	return m.jumpToRef(ref)
}

// jumpKey is the key that walks the changes, kept as a binding rather than a
// literal in the key switch so the handler and the line /help prints cannot
// drift apart. alt+g is where the mnemonic would put ctrl+g, which is unusable:
// the editor answers ctrl+g with its git panel before the chat ever sees the
// key, so it would work in a bare chat and do nothing at all once the editor
// existed — the same trap ctrl+v walked into, from the other direction.
var jumpKey = key.NewBinding(
	key.WithKeys("alt+g"),
	key.WithHelp("alt+g", i18n.T("next change")),
)

// jumpPressed reports whether msg is the jump key.
func jumpPressed(msg tea.KeyPressMsg) bool { return key.Matches(msg, jumpKey) }
