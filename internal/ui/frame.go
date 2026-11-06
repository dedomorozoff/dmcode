package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// sidebarVisible reports whether the sidebar is actually on screen. Having it
// switched on is not enough: below minSidebarTerm there is no room for it next
// to a usable chat panel, so it is dropped rather than squeezing the transcript
// down to a column of text.
func (m *uiModel) sidebarVisible() bool {
	return m.showSidebar && m.width >= minSidebarTerm && m.width-sidebarBoxWidth-sidebarGap >= minChatWidth
}

// headerVisible reports whether the top bar is on screen. The sidebar carries
// the same facts the header does — the model, the session, the folder — so the
// two are never up at once: with the panel open the header row goes back to the
// transcript, and with it hidden (a narrow terminal, or ctrl+b) the header is
// the only place those facts live.
func (m *uiModel) headerVisible() bool {
	return !m.sidebarVisible()
}

// chromeHeight is how many rows the frame spends on everything that is not
// transcript, header included when it is on screen. It is a method rather than
// the constant because the header is conditional: hiding it must give its rows
// to the transcript, not leave a gap.
func (m *uiModel) chromeHeight() int {
	// A question takes the bottom of the frame instead of floating over the
	// conversation: the rows it needs come out of the transcript, the same way
	// the pending-image strip's do, so the reply the question is about stays
	// readable above it. It replaces the status bar and the input box — keys go
	// to the question, and an input that cannot take them is a lie.
	if m.ask.open {
		h := m.askHeight() + panelBorder
		if m.headerVisible() {
			h += headerHeight
		}
		return h
	}
	h := chromeHeight
	if m.headerVisible() {
		h += headerHeight
	}
	h += m.pendingHeight()
	return h
}

// pendingHeight is how many rows the pending-image strip takes above the input,
// or 0 when nothing is attached.
//
// It is a method and not a constant for the same reason chromeHeight is: the rows
// belong to the transcript whenever they are not on screen. A strip that reserved
// its height permanently would take a preview's worth of conversation away from a
// user who has never attached anything.
func (m *uiModel) pendingHeight() int {
	if len(m.pending) == 0 {
		return 0
	}
	rows := 0
	for _, a := range m.pending {
		rows += a.Rows + 1 // the art, plus its caption
	}
	// The strip is framed like the input box it sits above, so it costs its two
	// borders as well.
	return rows + pendingPanelBorder
}

// chatBoxWidth is the outer width of the chat panel, borders included. It is
// never allowed to exceed the terminal: overrunning it is what pushed whole
// rows of the frame past the right edge.
func (m *uiModel) chatBoxWidth() int {
	w := m.width
	if m.sidebarVisible() {
		w -= sidebarBoxWidth + sidebarGap
	}
	return max(w, 8)
}

// contentWidth is how many cells a transcript row may occupy. The chat panel
// spends panelBorder columns on its vertical borders, so wrapping to anything
// wider is exactly what pushes text past the frame and into the sidebar.
func (m *uiModel) contentWidth() int {
	return max(m.chatBoxWidth()-panelBorder, 8)
}

// suggestHeight is how many rows the command list paints over the transcript, or
// 0 when it is closed.
//
// The list is an overlay, not a row of the frame: it is drawn on top of the
// chat panel instead of taking rows from it. Laying it out as real rows meant
// the transcript shrank every time a "/" was typed and jumped back when it was
// closed, and on a short terminal the list ran into the bottom of the screen
// with the input box still to fit. Painting over the transcript costs the
// newest reply for as long as the list is up and nothing else at all: the frame
// is identical with the list open and closed, so nothing shifts and nothing is
// ever pushed off screen.
func (m *uiModel) suggestHeight() int {
	if len(m.suggest) == 0 {
		return 0
	}
	// The list paints over the chat panel, so the rows available are everything
	// above the input box, less the chrome it must not cover.
	room := m.height - m.chromeHeight()
	if room < minSuggestRows {
		// Too little to draw a list over. The commands are still reachable by
		// typing them, which is the whole reason a picker is a convenience and
		// not the only path.
		return 0
	}
	return min(min(len(m.suggest)+2, suggestMaxRows), room)
}

// minSuggestRows is one command plus the header naming the keys, and the row the
// overflow marker takes when the list is longer than the space allows. A header
// with no command under it is worse than nothing: it advertises keys that lead
// nowhere and looks broken.
const minSuggestRows = 2

// suggestMaxRows caps the list. A user arrowing through commands wants to read
// the reply they are answering, not scroll a pane that swallowed it — but it has
// to be enough rows to be a list rather than a peek, because matching the letters
// in order (see rankCommands) keeps several commands plausible after one
// keystroke, and a window that showed two of them would be hiding the reason for
// the change. Everything above this is bounded by the room on the screen, which
// suggestHeight asks for.
const suggestMaxRows = 12

// transcriptRows is how many transcript rows the terminal affords. The same
// arithmetic layout and View both need, kept in one place so the viewport
// height and the frame drawn around it cannot disagree.
func (m *uiModel) transcriptRows() int {
	return max(m.height-m.chromeHeight(), minViewRows)
}

// layout sizes the viewport so that header + chat panel + status bar + input box
// add up to exactly the terminal height. The command list is not part of it: it
// is painted over the panel afterwards, so the frame does not change when it
// opens or closes. The viewport receives the panel's *inner* row count; View adds
// the borders back.
func (m *uiModel) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	rows := m.transcriptRows()
	m.vp.SetWidth(m.contentWidth())
	m.vp.SetHeight(rows)
	// The input scrolls horizontally within one row; without an explicit width
	// a long prompt grows the box and pushes the frame off the bottom.
	m.input.SetWidth(max(m.contentWidth()-ansi.StringWidth(m.input.Prompt), 8))
	m.historyDirty = true
}

// modalUp reports whether one of the stacked boxes owns the whole frame: with
// any of them open the frame is the box and not the chat, so every row the hit
// test could name is a row that is not on screen.
func (m *uiModel) modalUp() bool {
	return m.otherModalUp() || m.ask.open
}

// otherModalUp reports a box that owns the whole screen. The question is not
// one of them: it is docked under the transcript, so the chat stays on screen
// and keeps its rows. It still owns the keyboard (modalUp) — an answer is
// required before the turn can carry on — but it does not own the frame.
func (m *uiModel) otherModalUp() bool {
	return m.picker.open || m.palette.open || m.setup.open || m.lang.open ||
		m.proxy.open || m.sessionsList.open || m.dirPick.open ||
		m.help.open || m.ctxPick.open
}

// chatOverlayUp reports whether anything is drawn over the transcript, so that
// what is under the pointer is not a transcript row.
//
// The command list is one of them even though it is painted over the panel
// rather than replacing it: the transcript rows are still in the frame, so the
// hit test would answer with one of them, and it would be the row behind the
// list the user actually pointed at.
func (m *uiModel) chatOverlayUp() bool {
	return m.modalUp() || len(m.suggest) > 0
}

// buildFrame assembles the chat frame at the model's current width and height:
// the whole window when the editor has never been opened, the workspace main
// area in chat mode (chatFrame sets the sizes to the region's then).
func (m *uiModel) buildFrame() string {
	if m.otherModalUp() {
		box := m.paletteBox()
		switch {
		case m.picker.open:
			box = m.modelPickerBox()
		case m.setup.open:
			box = m.setupBox()
		case m.lang.open:
			box = m.langBox()
		case m.proxy.open:
			box = m.proxyBox()
		case m.sessionsList.open:
			box = m.sessionsBox()
		case m.dirPick.open:
			box = m.dirPickBox()
		case m.ctxPick.open:
			box = m.ctxPickBox()
		case m.help.open:
			box = m.helpBox()
		}
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}

	// Every row of the frame is exactly m.width wide and together they are
	// exactly m.height rows: the chat panel and the sidebar are framed to the
	// same outer height, and the viewport gets the panel's inner rows. The
	// progress note is not a row of the frame — syncVP appends it to the
	// content, right under the last message.
	rows := m.vp.Height()
	panel := stylePanel.Width(m.chatBoxWidth()).Height(rows + panelBorder).Render(m.vp.View())

	middle := panel
	if m.sidebarVisible() {
		middle = lipgloss.JoinHorizontal(lipgloss.Top, panel, m.sidebarView(rows+panelBorder))
	}

	// JoinVertical pads every block out to the widest one, so a single
	// overflowing row is enough to shift the whole frame sideways. The command
	// list is deliberately not a part: it is painted over the panel below, so
	// opening and closing it leaves the frame byte-identical.
	parts := make([]string, 0, 5)
	if m.headerVisible() {
		parts = append(parts, m.headerView())
	}
	parts = append(parts, middle)
	if m.ask.open {
		// The question is the bottom of the frame, not a box over it. It takes
		// the status bar's and the input's place: the transcript above keeps
		// every row it had apart from the ones the question itself needs.
		parts = append(parts, m.askBox())
	} else {
		parts = append(parts, m.statusBarView())
		// The strip goes between the status bar and the input box rather than
		// into either: it is about what the *next* message will carry, which is
		// the input's business, and putting it above the bar would read as part
		// of the transcript.
		if strip := m.pendingStrip(); strip != "" {
			parts = append(parts, strip)
		}
		parts = append(parts, stylePanel.Width(m.width).Render(m.input.View()))
	}

	frame := lipgloss.JoinVertical(lipgloss.Left, parts...)
	// The command list belongs to the prompt, and the prompt is not on screen
	// while a question is up: painting it there would draw a picker over the
	// question's own rows.
	if !m.ask.open {
		if rows := m.suggestHeight(); rows > 0 {
			frame = overlaySuggest(frame, m, rows)
		}
	}
	// The highlight is painted before the frame is cached, and the cache is what
	// the release handler reads: so what is copied is the text as drawn, styling
	// and all, not the same text before it was laid out.
	frame = m.paintSelection(frame)
	m.frame = frame
	return frame
}

// paintSelection draws the current selection in reverse video.
//
// It goes over the whole frame rather than only the transcript, because what the
// user can usefully select is everything they can see: a reply, an error, their
// own prompt, the status bar. Anchoring it to the chat panel would silently make
// the error line at the bottom unselectable, which is the one thing anybody
// highlighting an error actually wants.
func (m *uiModel) paintSelection(frame string) string {
	from, to, ok := m.selectionCells()
	if !ok {
		return frame
	}
	rows := strings.Split(frame, "\n")
	last := min(to.y, len(rows)-1)
	for y := max(from.y, 0); y <= last; y++ {
		start, end := 0, ansi.StringWidth(rows[y])
		if y == from.y {
			start = from.x
		}
		if y == to.y {
			end = to.x
		}
		if end <= start {
			continue
		}
		rows[y] = highlightCells(rows[y], start, end)
	}
	return strings.Join(rows, "\n")
}
