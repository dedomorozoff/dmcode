package editor

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/editor/i18n"
	"github.com/dedomorozoff/dmcode/internal/editor/lsp"
	"github.com/dedomorozoff/dmcode/internal/editor/syntax"
	"github.com/dedomorozoff/dmcode/internal/editor/vcs"
)

var (
	gutterStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	curGutterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	cursorStyle     = lipgloss.NewStyle().Reverse(true)
	statusStyle     = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250"))
	frameStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusHiStyle   = lipgloss.NewStyle().Background(lipgloss.Color("61")).Foreground(lipgloss.Color("255")).Bold(true)
	langStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	hintStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activePaneStyle = lipgloss.NewStyle().Background(lipgloss.Color("235"))
	matchStyle      = lipgloss.NewStyle().Background(lipgloss.Color("214")).Foreground(lipgloss.Color("0"))
	curMatchStyle   = lipgloss.NewStyle().Background(lipgloss.Color("226")).Foreground(lipgloss.Color("0")).Bold(true)
	gitAddStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	gitModStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	gitDelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	diagErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	diagWarnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	diagInfoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	bmStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	okTestStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	errTestStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	diffAddBg       = lipgloss.NewStyle().Background(lipgloss.Color("22"))
	diffDelBg       = lipgloss.NewStyle().Background(lipgloss.Color("52"))
	diffModBg       = lipgloss.NewStyle().Background(lipgloss.Color("58"))
	selectionStyle  = lipgloss.NewStyle().Background(lipgloss.Color("60")).Foreground(lipgloss.Color("255"))
	blameStyle      = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
)

type helpEntry struct {
	keys string
	desc string // i18n key
}

var helpEntries = []helpEntry{
	{"Ctrl+S", "help.save"},
	{"", ""},
	// The first entry: the status-bar icon at the head of the strip is how a
	// user leaves the editor for the chat, and there is no key for it inside the
	// editor — the chat is the host's mode. Help is where a user looks for the
	// way out, so the way out is listed first.
	{"Ctrl+Q or the ▣ icon", "help.to_chat"},
	{"", ""},
	{"Ctrl+P / F2 / Shift+Shift", "help.palette"},
	{"Shift+Arrows", "help.select"},
	{"Ctrl+C / Ctrl+X / Ctrl+V", "help.clipboard"},
	{"", ""},
	{"Ctrl+F / F3 / Shift+F3", "help.search"},
	{"Ctrl+H", "help.replace"},
	{"Ctrl+L", "help.goto_line"},
	{"Alt+Z", "help.word_wrap"},
	{"Ctrl+G", "help.git_panel"},
	{"D (in Git panel)", "help.git_diff"},
	{"Alt+[ / Alt+]", "help.hunk"},
	{"Alt+M / Alt+N / S+Alt+N", "help.bookmark"},
	{"F12", "help.goto_def"},
	{"Ctrl+O", "help.finder"},
	{"Ctrl+T", "help.open"},
	{"Alt+T", "help.terminal"},
	{"Ctrl+B / F9", "help.tree"},
	{"↑↓/Enter/←→ in tree", "help.tree_nav"},
	{"Alt+←/→", "help.tab_switch"},
	{"Alt+1..9", "help.tab_jump"},
	{"Ctrl+\\ / F6", "help.split_vert"},
	{"Ctrl+Alt+H / F7", "help.split_horiz"},
	{"Ctrl+Alt+P / F8", "help.split_focus"},
	{"Ctrl+Alt+W", "help.split_close"},
	{"Ctrl+W / Ctrl+X", "help.tab_close"},
	{"", ""},
	{"Arrows/Home/End/PgUp/PgDn", "help.move"},
	// A picture tab answers only these: the wheel and the paging keys scroll a
	// tall image, and nothing on this list edits it.
	{"↑↓/PgUp/PgDn/Wheel (picture)", "help.image_scroll"},
	{"Enter/Backspace/Delete/Tab", "help.edit"},
	{"Ctrl+Space", "help.complete"},
	{"Ctrl+/", "help.comment"},
	{"Ctrl+Z / Ctrl+R", "help.undo"},
	{"Ctrl+Y / Ctrl+D", "help.lines"},
	{"Ctrl+U", "help.uppercase"},
	{"Alt+D", "help.multicursor_word"},
	{"Alt+Click", "help.multicursor_click"},
	{"Esc", "help.multicursor_esc"},
	{"Alt+↑ / Alt+↓", "help.move_line"},
	{"", ""},
	{"F1 or Ctrl+E", "help.toggle_help"},
	{"Ctrl+Q / Ctrl+C", "help.quit"},
}

// helpRows adapts the static table to the embedding: in dmcode's workspace
// ctrl+e is the chat toggle, so the way out is named by the same key that
// opened the editor and the help binding stays F1 alone.
func (m Model) helpRows() []helpEntry {
	if !m.Embed {
		return helpEntries
	}
	rows := make([]helpEntry, len(helpEntries))
	copy(rows, helpEntries)
	for i := range rows {
		switch rows[i].desc {
		case "help.to_chat":
			rows[i].keys = "Ctrl+E / Ctrl+Q / ▣ icon"
		case "help.toggle_help":
			rows[i].keys = "F1"
		}
	}
	return rows
}

func (m Model) finderExtraRows() int {
	if !m.finderOpen {
		return 0
	}
	return len(m.finderHits) + 2
}

// dividerRow is the boundary between the editor/content stack and a docked
// panel. It is prepended to the panel, so it is not a trailing footer: the
// next panel or the final status bar remains the actual bottom boundary.
func (m Model) dividerRow() string {
	return statusStyle.Render(strings.Repeat(m.g.hline, m.width))
}

func (m Model) withDivider(rows []string) []string {
	return append([]string{m.dividerRow()}, rows...)
}

func (m Model) paletteExtraRows() int {
	if !m.paletteOpen {
		return 0
	}
	hits := m.filterPalette()
	if len(hits) > 8 {
		return 10
	}
	return len(hits) + 2
}

// folderExtraRows is how many rows the built-in folder picker occupies above
// the status bar, including its leading divider.
func (m Model) folderExtraRows() int {
	if !m.folderOpen {
		return 0
	}
	n := 4 // leading divider + header + parent row + hint
	if len(m.folderEntries) == 0 {
		n++ // — empty directory —
		return n
	}
	if len(m.folderEntries) > folderVisible {
		n += folderVisible
	} else {
		n += len(m.folderEntries)
	}
	return n
}

func (m Model) termPanelHeight() int {
	h := m.height / 3
	if h < 6 {
		h = 6
	}
	if h > 16 {
		h = 16
	}
	return h
}

func (m Model) terminalGeometry() (int, int) {
	w := m.width
	if w < 1 {
		w = 80
	}
	h := m.termPanelHeight()
	if m.termOpen {
		h = m.termPanelHeight()
	}
	return w, h
}

func (m Model) termExtraRows() int {
	if !m.termOpen {
		return 0
	}
	return m.termPanelHeight() + 1 // leading divider
}

func (m Model) langChooserExtraRows() int {
	if !m.langChooserOpen {
		return 0
	}
	return len(i18n.Supported()) + 2 // leading divider + header/items
}

func (m Model) pluginStoreExtraRows() int {
	if !m.pluginStoreOpen {
		return 0
	}
	n := len(m.storeItems)
	if m.storeLoading || m.storeErr != "" {
		n++
	}
	return n + 2 // leading divider + title
}

func (m Model) contextBottomExtraRows() int {
	switch {
	case m.diffViewOpen,
		m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0,
		m.conflictOpen, m.treeConfirm != "", m.quitConfirm,
		// The git hint lines are dropped in chat mode — the reserved row
		// goes back to the main area with them.
		m.gitOpen && !m.Chat, m.promptSave, m.promptOpen,
		m.searchOpen, m.gotoOpen:
		return 1
	default:
		return 0
	}
}

func (m Model) viewHeight() int {
	h := m.height - 2 - m.contextBottomExtraRows() - m.finderExtraRows() - m.folderExtraRows() - m.paletteExtraRows() - m.langChooserExtraRows() - m.termExtraRows() - m.pluginStoreExtraRows() - m.gitCommitExtraRows()
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) contextBottomRow() string {
	if m.diffViewOpen {
		return m.diffBottom()
	} else if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		return m.diffBottom()
	} else if m.conflictOpen {
		return m.conflictLine()
	} else if m.treeConfirm != "" {
		return m.treeConfirmLine()
	} else if m.quitConfirm {
		return m.quitLine()
	} else if m.gitOpen && !m.Chat {
		// The chat workspace drops the git command/hint lines for now — what
		// to do with them there is still an open question.
		switch m.gitMode {
		case gitModeCommit:
			return m.gitLine()
		case gitModeLog:
			return m.gitLogStatusLine()
		case gitModeBranch:
			return m.gitBranchLine()
		default:
			return m.gitStatusLine()
		}
	} else if m.promptSave {
		return m.saveLine()
	} else if m.promptOpen {
		return m.promptLine()
	} else if m.searchOpen {
		if m.replaceOpen {
			return m.replaceLine()
		}
		return m.searchLine()
	} else if m.gotoOpen {
		return m.gotoLine()
	}
	return ""
}

func (m Model) gutterWidthForTab(t *tab) int {
	// A picture has no lines. The gutter would number an empty buffer's single
	// line "1" down the side of a rendered image, which is a document that does
	// not exist — and paneContentWidth subtracts this, so returning a real width
	// here would also make the art narrower than the pane for no reason.
	if t.img != nil {
		return 0
	}
	// Line number + one shared marker column (breakpoint ● / bookmark ◆).
	w := len(strconv.Itoa(t.buf.LineCount())) + 3
	if w < 6 {
		w = 6
	}
	return w
}

func (m Model) paneContentWidth(paneIdx int) int {
	t := &m.tabs[m.panes[paneIdx].tabIdx]
	return m.paneTotalWidth(paneIdx) - m.gutterWidthForTab(t)
}

func (m Model) View() tea.View {
	h := m.viewHeight()
	rows := make([]string, 0, h+2)
	if m.Chat {
		// Chat mode keeps the geometry — every panel row, the status bar and
		// the mouse math assume the tab bar row exists — but hides its
		// content: the main area is the chat, tabs mean nothing there.
		rows = append(rows, strings.Repeat(" ", maxInt(m.width, 0)))
	} else {
		rows = append(rows, m.tabBar())
	}
	if m.Chat && m.Host != nil && m.Host.View != nil {
		// Chat mode: the main area is the host's transcript and input at the
		// same geometry the editor panes would use, so the tree sidebar, the
		// git panel, the terminal and the status bar compose around it the
		// same way they compose around the buffers.
		rows = append(rows, m.composeSidebar(m.Host.View(m.editorAreaWidth(), h))...)
	} else if m.diffViewOpen {
		rows = append(rows, m.diffViewRows(h)...)
	} else if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		// Inline diff preview: show side-by-side diff of selected file/commit
		// in the editor area while the git panel is open.
		diffRows := m.renderSideBySide(m.diffHeadLines, m.diffRightLines, m.diffRows, m.diffOffsetY, m.diffOffsetX, m.editorAreaWidth(), h, m.diffHeadSyntax, m.diffRightSyntax)
		rows = append(rows, m.composeSidebar(diffRows)...)
	} else if m.conflictOpen && len(m.conflictRows) > 0 {
		rows = append(rows, m.renderSideBySide(m.conflictLeftLines, m.conflictRightLines, m.conflictRows, m.conflictOffY, m.conflictOffX, m.width, h, nil, nil)...)
	} else if m.helpOpen {
		rows = append(rows, m.helpPanel(h)...)
	} else {
		rows = append(rows, m.editorRows(h)...)
	}
	// The bottom overlays keep the existing focused prompt/status line and
	// input rows together. The application-wide status bar is appended last,
	// after the terminal, so no bottom panel can hide it.
	if context := m.contextBottomRow(); context != "" {
		rows = append(rows, context)
	}
	if m.gitOpen && m.gitMode == gitModeCommit {
		rows = append(rows, m.gitCommitInputRender()...)
	}
	if m.finderOpen {
		rows = append(rows, m.withDivider(m.finderPanel())...)
	}
	if m.folderOpen {
		rows = append(rows, m.withDivider(m.folderPanel())...)
	}
	if m.paletteOpen {
		rows = append(rows, m.withDivider(m.palettePanel())...)
	}
	if m.langChooserOpen {
		rows = append(rows, m.withDivider(m.langChooserPanel())...)
	}
	if m.pluginStoreOpen {
		rows = append(rows, m.withDivider(m.pluginStorePanel())...)
	}
	if m.termOpen {
		// The chat workspace frames the terminal like its own boxes; the
		// editor keeps the bare divider. The divider row is framed rather than
		// dropped: termExtraRows reserves it and every mouse coordinate counts
		// from it, so leaving it out makes the frame a row short of the
		// terminal and lifts the status bar off the bottom edge.
		if m.Chat {
			rows = append(rows, m.frameRows(m.withDivider(m.terminalPanel()), m.width)...)
		} else {
			rows = append(rows, m.withDivider(m.terminalPanel())...)
		}
	}
	rows = append(rows, m.statusBar())
	// The completion popup floats under the edit line instead of being pinned
	// to the bottom of the screen.
	rows = m.overlayCompletion(rows)
	// The status-icon hover callout floats just above the status bar.
	rows = m.overlayStatusTooltip(rows)
	// The split-icon hover callout floats just under the tab bar.
	rows = m.overlaySplitTooltip(rows)
	var v tea.View
	v.SetContent(lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(rows, "\n")))
	v.AltScreen = true
	v.WindowTitle = "dmcode — " + m.activeTab().name(m.baseDir())
	// All-motion mode is required so hover (no button held) reaches the app;
	// this powers the status-bar icon callout. Terminals without any-event
	// tracking simply never deliver hover.
	v.MouseMode = tea.MouseModeAllMotion

	// Request the Kitty keyboard protocol so the terminal reports bare
	// modifier presses and every physical key as an escape code. This is what
	// makes double-Shift (JetBrains-style palette) possible. Terminals without
	// support ignore the request, so this degrades gracefully.
	v.KeyboardEnhancements.ReportAllKeysAsEscapeCodes = true

	// The editor draws its own static reverse-video cursor at every caret
	// (including the main one), so the terminal cursor must be hidden. Otherwise
	// the blinking terminal block overlaps the static reverse cell and looks
	// like two cursors stacked on the same position.
	v.Cursor = nil

	return v
}

func (m Model) editorRows(h int) []string {
	var rows []string
	if m.layout == splitNone {
		rows = m.composeSidebar(m.renderPaneRows(0, h, m.paneTotalWidth(0)))
		return rows
	} else if m.layout == splitVert {
		w0 := m.paneTotalWidth(0)
		w1 := m.paneTotalWidth(1)
		ch := m.paneContentHeight(0)
		left := m.renderPaneRows(0, ch, w0)
		right := m.renderPaneRows(1, ch, w1)
		combined := make([]string, h)
		sepColor := "238"
		if m.activePane == 0 {
			sepColor = "61" // highlight left pane separator
		}
		sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(sepColor))
		sep := sepStyle.Render(m.g.vline)
		for row := 0; row < ch; row++ {
			combined[row] = padTo(left[row], w0) + sep + padTo(right[row], w1)
		}
		// The shared bottom row is the status bar of both panes, one per column.
		combined[ch] = padTo(m.paneStatusBar(0), w0) + sep + padTo(m.paneStatusBar(1), w1)
		rows = m.composeSidebar(combined)
		return rows
	}
	// splitHoriz
	h0 := m.paneViewHeight(0)
	h1 := m.paneViewHeight(1)
	w0 := m.paneTotalWidth(0)
	w1 := m.paneTotalWidth(1)
	top := m.renderPaneRows(0, m.paneContentHeight(0), w0)
	bottom := m.renderPaneRows(1, m.paneContentHeight(1), w1)
	for row := range top {
		top[row] = padTo(top[row], w0)
	}
	for row := range bottom {
		bottom[row] = padTo(bottom[row], w1)
	}
	sepColor := "238"
	if m.activePane == 0 {
		sepColor = "61" // highlight top pane separator
	}
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(sepColor))
	sep := sepStyle.Render(strings.Repeat(m.g.hline, m.editorAreaWidth()))
	combined := make([]string, 0, h0+h1+1)
	combined = append(combined, top...)
	// Each pane docks its own status line at the bottom of its cell.
	combined = append(combined, padTo(m.paneStatusBar(0), w0))
	combined = append(combined, sep)
	combined = append(combined, bottom...)
	combined = append(combined, padTo(m.paneStatusBar(1), w1))
	rows = m.composeSidebar(combined)
	return rows
}

// nothingOpenRows is the buffer area when no file is open: a dim line saying so
// and naming the keys that fill it, over blank rows.
//
// It is drawn rather than left empty on purpose. A blank editor with nothing in
// the tab bar reads as a broken program; a line that says which key opens a file
// reads as an editor waiting for one. The rows are padded to the pane's width
// because composeSidebar concatenates them with the rail rather than laying them
// out — a short row would shear the sidebar down the page.
func (m Model) nothingOpenRows(h, totalW int) []string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = strings.Repeat(" ", max(totalW, 0))
	}
	if h < 1 || totalW < 8 {
		return rows
	}
	hint := "  " + m.t("editor.nothing_open")
	rows[0] = padTo(ansi.Truncate(hintStyle.Render(hint), totalW, "…"), totalW)
	return rows
}

func (m Model) composeSidebar(editor []string) []string {
	var rail []string
	switch {
	case m.gitOpen:
		rail = m.gitPanel(len(editor))
	case m.sidebarOn():
		rail = m.treePanel(len(editor))
	default:
		return editor
	}
	if m.Chat {
		// The chat workspace frames its panels like its own chat box.
		rail = m.frameRows(rail, m.leftRailWidth())
	}
	out := make([]string, len(editor))
	for row := range editor {
		out[row] = rail[row] + editor[row]
	}
	return out
}

// frameRows wraps panel rows in a box border without changing the row count:
// the first and last rows give up their content to the horizontal borders.
// This is what the chat workspace draws instead of the editor's bare
// divider, so its panels match the framed look of the chat box.
func (m Model) frameRows(rows []string, w int) []string {
	if len(rows) < 2 || w < 4 {
		return rows
	}
	inner := w - 2
	// The ASCII glyph set has no box corners; its hline is "-" and the
	// corners become "+".
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	if m.g.hline != "─" {
		tl, tr, bl, br = "+", "+", "+", "+"
	}
	out := make([]string, len(rows))
	out[0] = frameStyle.Render(tl + strings.Repeat(m.g.hline, w-2) + tr)
	out[len(rows)-1] = frameStyle.Render(bl + strings.Repeat(m.g.hline, w-2) + br)
	for i := 1; i < len(rows)-1; i++ {
		cell := ansi.Truncate(rows[i], inner, "")
		if pad := inner - lipgloss.Width(cell); pad > 0 {
			cell += strings.Repeat(" ", pad)
		}
		out[i] = frameStyle.Render(m.g.vline) + cell + frameStyle.Render(m.g.vline)
	}
	return out
}

func (m Model) renderPaneRows(paneIdx, h, totalW int) []string {
	p := &m.panes[paneIdx]
	t := &m.tabs[p.tabIdx]
	// The standing empty buffer gets no gutter and no line numbers: a column of
	// "1" beside an empty screen is exactly the artefact the scratch tab is
	// hidden to avoid. What is there instead says so, and names the two keys
	// that fill it.
	if t.scratch() {
		return m.nothingOpenRows(h, totalW)
	}
	// A picture is drawn where the lines would be. It comes before the gutter
	// because a gutter over a picture is a column of line numbers for a document
	// that has none — and the buffer behind it is empty, so the numbers would
	// all be "1" anyway.
	if t.img != nil {
		return m.imageRows(t, h, totalW)
	}
	gw := m.gutterWidthForTab(t)
	contentW := totalW - gw
	if contentW < 0 {
		contentW = 0
	}
	cur := t.buf.CurLine()
	active := paneIdx == m.activePane
	rows := make([]string, h)

	syntaxLines := t.getSyntaxLines(m.syn)
	diff := t.getDiff(m.repo)
	diagPath, _ := filepath.Abs(t.path)
	tabDiags := m.diags[diagPath]
	bmSet := m.bookmarks[diagPath]

	wrap := p.wordWrap && contentW > 0
	var segs []wrapSeg
	if wrap {
		segs = t.tabWrap(contentW, m.cfg.Editor.TabWidth)
	}

	for row := 0; row < h; row++ {
		ln := p.offsetY + row
		segStart, segEnd := 0, 0
		continuation := false
		if wrap {
			si := p.offsetY + row
			if si >= len(segs) {
				if gw > 0 {
					rows[row] = strings.Repeat(" ", gw)
				}
				continue
			}
			ln = segs[si].line
			segStart = segs[si].expStart
			segEnd = segs[si].expEnd
			continuation = segStart > 0
		}

		if continuation {
			// Wrapped continuation row: blank gutter, content segment only.
			rows[row] = strings.Repeat(" ", gw) + m.renderLineWrap(p, t, ln, segStart, segEnd, active, syntaxLines)
			continue
		}

		num := strconv.Itoa(ln + 1)
		gitMark := " "
		gitMarkStyle := gutterStyle
		if ln < len(diff.Lines) {
			switch diff.Lines[ln] {
			case vcs.DiffAdded:
				gitMark = "+"
				gitMarkStyle = gitAddStyle
			case vcs.DiffModified:
				gitMark = "~"
				gitMarkStyle = gitModStyle
			case vcs.DiffDeleted:
				gitMark = "_"
				gitMarkStyle = gitDelStyle
			}
		}

		diagMark, diagSev := m.diagMarkFor(tabDiags, ln)
		diagMarkStyle := gutterStyle
		switch diagSev {
		case 1:
			diagMarkStyle = diagErrStyle
		case 2:
			diagMarkStyle = diagWarnStyle
		default:
			if diagMark != "" {
				diagMarkStyle = diagInfoStyle
			}
		}
		if diagMark == "" {
			diagMark = " "
		}

		// One shared marker column: the bookmark ◆ lives in the gutter's
		// marker slot beside the diagnostics and git marks.
		mark := " "
		if bmSet[ln+1] {
			mark = m.g.bookmark
		}
		markStyle := gutterStyle
		switch mark {
		case m.g.bookmark:
			markStyle = bmStyle
		}

		numPad := gw - 3 - len(num)
		if numPad < 0 {
			numPad = 0
		}
		numStr := strings.Repeat(" ", numPad) + num
		var gutStr string
		if active && ln == cur && ln < t.buf.LineCount() {
			gutStr = curGutterStyle.Render(numStr)
		} else {
			gutStr = gutterStyle.Render(numStr)
		}
		gutStr += diagMarkStyle.Render(diagMark)
		gutStr += gitMarkStyle.Render(gitMark)
		gutStr += markStyle.Render(mark)

		if ln >= t.buf.LineCount() {
			rows[row] = gutStr
			continue
		}
		if wrap {
			rows[row] = gutStr + m.renderLineWrap(p, t, ln, segStart, segEnd, active, syntaxLines)
		} else {
			rows[row] = gutStr + m.renderLine(p, t, ln, contentW, active, syntaxLines)
			rows[row] = m.appendBlame(rows[row], t, diff, ln, contentW)
		}
	}
	return rows
}

// appendBlame right-aligns a "author · when" git blame annotation on a row.
// It only annotates unchanged lines (buffer line == HEAD line) so the label
// stays truthful, and only when there is room on the row.
func (m Model) appendBlame(row string, t *tab, diff vcs.FileDiff, ln, contentW int) string {
	if !m.blameOn || t.blame == nil {
		return row
	}
	if ln >= len(diff.Lines) || diff.Lines[ln] != vcs.DiffNone || ln >= len(t.blame) {
		return row
	}
	lab := m.blameLabel(t.blame[ln])
	labW := lipgloss.Width(lab)
	if labW < 1 {
		return row
	}
	rowW := lipgloss.Width(row)
	pad := contentW - rowW - labW
	if pad < 2 {
		return row
	}
	return row + strings.Repeat(" ", pad) + blameStyle.Render(lab)
}

// blameLabel formats one blame line as "author · when".
func (m Model) blameLabel(b vcs.BlameLine) string {
	author := b.Author
	if i := strings.IndexAny(author, "<"); i >= 0 {
		author = strings.TrimSpace(author[:i])
	}
	if author == "" {
		author = "?"
	}
	if len(author) > 12 {
		author = author[:12]
	}
	return author + " " + m.g.dotSep + " " + relWhen(b.Date)
}

// relWhen renders a time as a compact relative human string.
func relWhen(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// renderLineWrap renders one wrap segment (expanded rune range [segStart,
// segEnd)) of a buffer line as its own screen row.
func (m Model) renderLineWrap(p *pane, t *tab, ln, segStart, segEnd int, activePane bool, syntaxLines []syntax.HighlightedLine) string {
	if ln < 0 || ln >= t.buf.LineCount() || segEnd <= segStart {
		return ""
	}
	pp := *p
	pp.offsetX = segStart
	return m.renderLine(&pp, t, ln, segEnd-segStart, activePane, syntaxLines)
}

// diagMarkFor returns the gutter marker for a line given the file's
// diagnostics, and its severity (1: Error, 2: Warning, else Info/Hint). Empty
// marker means the line has no diagnostics. Lower severity wins on overlap.
func (m Model) diagMarkFor(diags []lsp.Diagnostic, line int) (string, int) {
	mark, sev := "", 0
	for _, d := range diags {
		if d.Line != line {
			continue
		}
		if mark == "" || d.Severity < sev {
			if d.Severity == 1 || d.Severity == 2 {
				mark = "!"
			} else {
				mark = m.g.diagInfo
			}
			sev = d.Severity
		}
	}
	return mark, sev
}

// gitPanelWidth is the width of the left Git rail (like the project tree).
const gitPanelWidth = 30

func (m Model) sidebarWidth() int {
	if m.sidebarOn() {
		return m.cfg.UI.TreeWidth
	}
	return 0
}

// leftRailWidth is the width of the whole left column: the Git panel takes
// precedence over the project tree while it is open.
func (m Model) leftRailWidth() int {
	if m.gitOpen {
		return gitPanelWidth
	}
	return m.sidebarWidth()
}

// tabLabel is the text of one tab in the tab bar, shared by the renderer and
// the mouse hit-test so that clicks line up with painted tabs.
func (m Model) tabLabel(i int) string {
	t := &m.tabs[i]
	name := fmt.Sprintf(" %d:%s ", i+1, t.name(m.baseDir()))
	if t.buf.Dirty() {
		name += "* "
	}
	return name
}

func (m Model) tabBar() string {
	var parts []string
	activeTab := m.activeTabIndex()
	for i := range m.tabs {
		// The standing empty buffer is not a tab anybody can look at. It is still
		// in the slice, so the numbering below is the real index and ctrl+2 still
		// means the second tab: skipping it here must not renumber the rest.
		if m.tabs[i].scratch() {
			continue
		}
		name := m.tabLabel(i)
		if i == activeTab {
			parts = append(parts, statusHiStyle.Render(name))
		} else {
			parts = append(parts, statusStyle.Render(name))
		}
	}
	line := strings.Join(parts, "")
	icons := m.splitIconsString()
	fill := m.width - lipgloss.Width(line) - lipgloss.Width(icons)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + icons
}

func (m Model) treePanel(h int) []string {
	inner := m.cfg.UI.TreeWidth - 1
	entryRows := h
	if entryRows < 0 {
		entryRows = 0
	}
	off := m.clampedTreeOffset(entryRows)
	rows := make([]string, 0, h)
	for row := 0; row < entryRows; row++ {
		i := off + row
		var cell string
		if i < len(m.treeRows) {
			e := m.treeRows[i]
			var plain strings.Builder
			var styled strings.Builder
			if e.depth > 1 {
				for _, isLast := range e.anc {
					seg := "    "
					if !isLast {
						seg = m.g.vline + "   "
					}
					plain.WriteString(seg)
					styled.WriteString(treeConnStyle.Render(seg))
				}
				seg := m.g.tee + m.g.hline + m.g.hline + " "
				if e.last {
					seg = m.g.corner + m.g.hline + m.g.hline + " "
				}
				plain.WriteString(seg)
				styled.WriteString(treeConnStyle.Render(seg))
			}
			if e.isDir {
				icon := m.g.expand + " "
				if m.expanded[e.rel] {
					icon = m.g.collapse + " "
				}
				plain.WriteString(icon)
				styled.WriteString(treeIconStyle.Render(icon))
				plain.WriteString(e.name)
				styled.WriteString(treeDirStyle.Render(e.name))
			} else {
				plain.WriteString(e.name)
				styled.WriteString(treeFileStyle.Render(e.name))
			}
			line := styled.String()
			plainS := plain.String()
			pad := inner - lipgloss.Width(line)
			if pad < 0 {
				// Truncate to the available width.
				runes := []rune(plainS)
				plainS = string(runes[:maxInt(0, len(runes)+pad)])
				line = plainS
				pad = 0
			}
			fill := strings.Repeat(" ", pad)
			if i == m.treeSel && m.treeFocus {
				cell = statusHiStyle.Render(plainS + fill)
			} else if i == m.treeSel {
				cell = statusStyle.Render(plainS + fill)
			} else {
				cell = line + fill
			}
		} else {
			cell = strings.Repeat(" ", inner)
		}
		rows = append(rows, cell+" ")
	}
	return rows
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// minInt is maxInt's counterpart, for the few places that need a lower bound.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func (m Model) helpPanel(h int) []string {
	entries := m.helpRows()
	all := make([]string, 0, len(entries)+1)
	title := statusHiStyle.Render(m.t("help.title")) + " " + hintStyle.Render(m.t("help.close_hint"))
	if m.helpMaxScroll() > 0 {
		title += hintStyle.Render(m.t("help.scroll_hint"))
	}
	all = append(all, title)
	for _, e := range entries {
		if e.keys == "" {
			all = append(all, "")
			continue
		}
		key := e.keys
		if len(key) < 26 {
			key += strings.Repeat(" ", 26-len(key))
		}
		all = append(all, " "+statusStyle.Render(key)+m.t(e.desc))
	}
	// Window the list so it never overflows the terminal; j/k/PgUp/PgDn and
	// the mouse wheel scroll it.
	off := m.helpScroll
	if max := len(all) - h; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	end := off + h
	if end > len(all) {
		end = len(all)
	}
	rows := all[off:end]
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

func (m Model) promptLine() string {
	label := m.t("prompt.open_file")
	if m.promptRename {
		label = m.t("prompt.rename")
	} else if m.promptNewFile {
		label = m.t("prompt.new_file")
	} else if m.promptNewFolder {
		label = m.t("prompt.new_folder")
	}
	line := statusHiStyle.Render(label) + statusStyle.Render(string(m.promptIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) saveLine() string {
	line := statusHiStyle.Render(m.t("prompt.save_as")) + statusStyle.Render(string(m.promptSaveIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) quitLine() string {
	line := statusHiStyle.Render(m.t("prompt.save_changes")) + statusStyle.Render(m.t("prompt.yes_no"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// treeConfirmLine renders the pending delete/trash confirmation at the bottom
// of the screen while the tree panel awaits Y/N/Esc.
func (m Model) treeConfirmLine() string {
	label := m.t("prompt.delete_q", m.treeConfirmRel)
	if m.treeConfirm == "trash" {
		label = m.t("prompt.trash_q", m.treeConfirmRel)
	}
	line := statusHiStyle.Render(label)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) gitLine() string {
	line := statusHiStyle.Render(m.t("git.commit_line")) + cursorStyle.Render(" ")
	if m.repo != nil {
		branch := m.repo.Branch()
		if branch != "" {
			line += hintStyle.Render(fmt.Sprintf("(%s: %s)", branch, m.repo.StatusSummary()))
		}
	}
	line += hintStyle.Render("  " + m.t("git.commit_hint"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// gitCommitInputRows returns the word-wrapped lines of the commit input.
func (m Model) gitCommitInputRows() []string {
	w := m.width - 4 // "Commit: " (8 but styled) + cursor (1) — approximate
	if w < 1 {
		w = 1
	}
	lines := wrapRunes(string(m.gitCommitIn), w)
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// gitCommitExtraRows returns the number of rows the commit input occupies
// (the status-bar line remains; the input renders below it).
func (m Model) gitCommitExtraRows() int {
	if !m.gitOpen || m.gitMode != gitModeCommit {
		return 0
	}
	n := len(m.gitCommitInputRows())
	if n < 1 {
		n = 1
	}
	return n
}

// gitCommitInputRows renders returns the multi-line commit input rows.
func (m Model) gitCommitInputRender() []string {
	w := m.width
	inputTextW := w - 8 // "Commit: " is 8 chars
	if inputTextW < 1 {
		inputTextW = 1
	}
	lines := wrapRunes(string(m.gitCommitIn), inputTextW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	var out []string
	for i, line := range lines {
		row := statusHiStyle.Render("Commit: ") + statusStyle.Render(line)
		if i == len(lines)-1 {
			row += cursorStyle.Render(" ")
		}
		if fill := w - lipgloss.Width(row); fill > 0 {
			row += statusStyle.Render(strings.Repeat(" ", fill))
		}
		out = append(out, row)
	}
	return out
}

func (m Model) gitStatusLine() string {
	r := m.repoForCur()
	var hint string
	if r == nil {
		hint = m.t("git.init_hint")
	} else {
		hint = m.t("git.hints")
	}
	line := m.statusIconsString()
	if r == nil {
		line += statusHiStyle.Render(" git: ") + statusStyle.Render(m.t("git.no_repo"))
	} else {
		summary := r.StatusSummary()
		staged := 0
		for _, fs := range m.gitFiles {
			if fs.IsStaged() {
				staged++
			}
		}
		line += statusHiStyle.Render(m.t("git.prefix_status")) +
			hintStyle.Render("("+r.Branch()+" "+summary+")") +
			statusStyle.Render(fmt.Sprintf(" %s", m.t("git.status_count", len(m.gitFiles), staged)))
	}
	// The hint (keybindings) must stay visible even on narrow terminals: trim the
	// summary to fit alongside it. If the hint itself is wider than the terminal,
	// trim the hint too so the summary is not lost entirely.
	fill := m.width - lipgloss.Width(hint) - lipgloss.Width(line)
	if fill < 0 {
		if avail := m.width - lipgloss.Width(hint); avail < 4 {
			hint = m.fitStatusTail(hint, m.width-2)
		}
		avail := m.width - lipgloss.Width(hint)
		if avail < 0 {
			avail = 0
		}
		line = statusStyle.Render(m.fitStatusTail(line, avail))
		fill = m.width - lipgloss.Width(hint) - lipgloss.Width(line)
	}
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + hintStyle.Render(hint)
}

// fitStatusTail strips styling and keeps the tail of s within w visible columns.
func (m Model) fitStatusTail(s string, w int) string {
	r := []rune(stripANSI(s))
	if len(r) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	return m.g.ellipsis + string(r[len(r)-(w-1):])
}

func (m Model) gitLogStatusLine() string {
	line := m.statusIconsString()
	if len(m.gitLogEntries) == 0 {
		line += statusHiStyle.Render(m.t("git.prefix_log")) + statusStyle.Render(m.t("git.no_commits"))
	} else {
		line += statusHiStyle.Render(m.t("git.prefix_log")) + hintStyle.Render(m.t("git.commit_count", len(m.gitLogEntries)))
	}
	hint := m.t("git.log_hint")
	fill := m.width - lipgloss.Width(line) - lipgloss.Width(hint)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + hintStyle.Render(hint)
}

func (m Model) gitBranchLine() string {
	if m.gitBranchNew {
		line := statusHiStyle.Render(m.t("git.new_branch_label")) + statusStyle.Render(string(m.gitBranchIn)) + cursorStyle.Render(" ")
		line += hintStyle.Render("  " + m.t("git.branch_new_hint"))
		fill := m.width - lipgloss.Width(line)
		if fill > 0 {
			line += statusStyle.Render(strings.Repeat(" ", fill))
		}
		return line
	}
	var line string
	if r := m.repoForCur(); r != nil {
		line = statusHiStyle.Render(m.t("git.prefix_branch")) + statusStyle.Render(r.Branch())
	}
	hint := m.t("git.branch_hint")
	fill := m.width - lipgloss.Width(line) - lipgloss.Width(hint)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + hintStyle.Render(hint)
}

// fitPath keeps the tail of long paths (the file name matters most).
func (m Model) fitPath(p string, w int) string {
	r := []rune(p)
	if len(r) <= w {
		return p
	}
	if w < 1 {
		return ""
	}
	return m.g.ellipsis + string(r[len(r)-(w-1):])
}

func (m Model) gitPanel(h int) []string {
	if m.gitMode == gitModeLog {
		return m.gitLogPanel(h)
	}
	if m.gitMode == gitModeBranch {
		return m.branchPanel(h)
	}
	rows := make([]string, 0, h)
	start := m.gitOffset
	end := start + m.gitListHeight()
	for i := start; i < end && len(rows) < h; i++ {
		fs := m.gitFiles[i]
		marker := fmt.Sprintf("%c%c", fs.Staging, fs.Worktree)
		path := m.fitPath(fs.Path, gitPanelWidth-5)
		plain := " " + marker + " " + path
		pad := gitPanelWidth - 1 - lipgloss.Width(plain)
		if pad < 0 {
			pad = 0
		}
		line := plain + strings.Repeat(" ", pad)
		var cell string
		switch {
		case i == m.gitSel:
			cell = statusHiStyle.Render(line)
		case fs.IsStaged():
			styled := " " + gitAddStyle.Render(marker) + " " + path
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		case fs.Worktree == vcs.StatusUntracked:
			styled := " " + hintStyle.Render(marker) + " " + path
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		default:
			styled := " " + gitModStyle.Render(marker) + " " + path
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		}
		rows = append(rows, cell+" ")
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", gitPanelWidth))
	}
	return rows
}

func (m Model) gitLogPanel(h int) []string {
	rows := make([]string, 0, h)
	start := m.gitLogOffset
	end := start + m.gitLogListHeight()
	for i := start; i < end && len(rows) < h; i++ {
		entry := m.gitLogEntries[i]
		// Two-line entry: " hash  subject" on first line, "         author time" on second
		hashStr := entry.Hash
		subject := m.fitPath(entry.Subject, gitPanelWidth-lipgloss.Width(hashStr)-3)
		first := " " + gitAddStyle.Render(hashStr) + " " + subject
		// Pad first line
		visW := lipgloss.Width(first)
		if pad := gitPanelWidth - 1 - visW; pad > 0 {
			first += strings.Repeat(" ", pad)
		}
		// Second line: relative time + author
		relTime := entry.When.Format("Jan 02 15:04")
		if len(entry.Author) > 10 {
			entry.Author = entry.Author[:10]
		}
		second := " " + hintStyle.Render(entry.Author+" "+relTime)
		visW2 := lipgloss.Width(second)
		if pad := gitPanelWidth - 1 - visW2; pad > 0 {
			second += strings.Repeat(" ", pad)
		}

		if i == m.gitLogSel {
			rows = append(rows, statusHiStyle.Render(first))
			rows = append(rows, statusHiStyle.Render(second))
		} else {
			rows = append(rows, first)
			rows = append(rows, second)
		}
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", gitPanelWidth))
	}
	return rows
}

// diffViewRows renders the side-by-side HEAD vs buffer view: left column is
// the old text, right column the current text. The diff rail is drawn over
// the full editor width (no sidebar while the diff is open).
func (m Model) diffViewRows(h int) []string {
	return m.renderSideBySide(m.diffHeadLines, m.diffRightLines, m.diffRows, m.diffOffsetY, m.diffOffsetX, m.width, h, m.diffHeadSyntax, m.diffRightSyntax)
}

// renderSideBySide renders a two-column diff view. Used by git diff, AI inline
// review, and conflict preview. Pass nil for leftSyntax/rightSyntax to disable
// syntax highlighting.
func (m Model) renderSideBySide(leftLines, rightLines []string, diffRows []vcs.DiffRow, offsetY, offsetX, w, h int, leftSyntax, rightSyntax []syntax.HighlightedLine) []string {
	half := (w - 1) / 2

	numW := len(strconv.Itoa(maxInt(len(leftLines), len(rightLines)))) + 1
	if numW < 3 {
		numW = 3
	}
	contentW := half - numW - 2 // number, space, marker, space
	if contentW < 1 {
		contentW = 1
	}

	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	sep := sepStyle.Render(m.g.vline)

	rows := make([]string, h)
	for row := 0; row < h; row++ {
		idx := offsetY + row
		var left, right string
		if idx < len(diffRows) {
			dr := diffRows[idx]
			var ls, rs syntax.HighlightedLine
			if leftSyntax != nil && idx < len(leftSyntax) {
				ls = leftSyntax[idx]
			}
			if rightSyntax != nil && idx < len(rightSyntax) {
				rs = rightSyntax[idx]
			}
			left = renderDiffCell(leftLines, dr.Left, dr.Type, false, numW, contentW, offsetX, ls)
			right = renderDiffCell(rightLines, dr.Right, dr.Type, true, numW, contentW, offsetX, rs)
		} else {
			left = strings.Repeat(" ", half)
			right = strings.Repeat(" ", half)
		}
		rows[row] = padTo(left+sep+right, w)
	}
	return rows
}

func renderDiffCell(lines []string, lineIdx int, dt vcs.DiffType, rightSide bool, numW, contentW, offsetX int, syntaxLine syntax.HighlightedLine) string {
	half := numW + 2 + contentW
	if lineIdx < 0 {
		return strings.Repeat(" ", half)
	}
	text := ""
	var runes []rune
	if lineIdx < len(lines) {
		runes = []rune(lines[lineIdx])
		lo := offsetX
		if lo > len(runes) {
			lo = len(runes)
		}
		hi := lo + contentW
		if hi > len(runes) {
			hi = len(runes)
		}
		runes = runes[lo:hi]
		text = string(runes)
	}
	marker := ' '
	var bg *lipgloss.Style
	switch dt {
	case vcs.DiffAdded:
		if rightSide {
			marker = '+'
			bg = &diffAddBg
		}
	case vcs.DiffDeleted:
		if !rightSide {
			marker = '-'
			bg = &diffDelBg
		}
	case vcs.DiffModified:
		marker = '~'
		bg = &diffModBg
	}

	// Build the prefix: line number + marker + space
	prefix := fmt.Sprintf("%*d %c ", numW-1, lineIdx+1, marker)
	needed := half - lipgloss.Width(prefix)
	if needed < 0 {
		needed = 0
	}

	if syntaxLine != nil || bg != nil {
		var out strings.Builder
		out.WriteString(prefix)
		for i := 0; i < len(runes) && i < needed; i++ {
			var st lipgloss.Style
			if syntaxLine != nil && i+offsetX < len(syntaxLine) {
				st = syntaxLine[i+offsetX]
			}
			if bg != nil {
				st = st.Background(bg.GetBackground())
			}
			out.WriteString(st.Render(string(runes[i])))
		}
		// Pad remaining space
		visW := lipgloss.Width(out.String())
		if pad := half - visW; pad > 0 {
			if bg != nil {
				out.WriteString(bg.Render(strings.Repeat(" ", pad)))
			} else {
				out.WriteString(strings.Repeat(" ", pad))
			}
		}
		return out.String()
	}

	cell := prefix + text
	if pad := half - len([]rune(cell)); pad > 0 {
		cell += strings.Repeat(" ", pad)
	}
	return cell
}

func (m Model) diffBottom() string {
	added, modified, deleted := 0, 0, 0
	for _, dr := range m.diffRows {
		switch dr.Type {
		case vcs.DiffAdded:
			added++
		case vcs.DiffModified:
			modified++
		case vcs.DiffDeleted:
			deleted++
		}
	}
	hint := m.t("git.diff_hint")
	if m.gitDiffFocused {
		hint = m.t("git.diff_focus")
	} else if m.gitMode == gitModeLog {
		hint = m.t("git.log_hint2")
	}
	modeTag := ""
	if m.gitMode == gitModeLog {
		modeTag = statusHiStyle.Render(m.t("git.log_tag")) + " "
	}
	line := modeTag + statusHiStyle.Render(m.t("git.diff_label")) + statusStyle.Render(m.diffPath) +
		hintStyle.Render(m.t("git.diff_stats", added, modified, deleted)) +
		hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) terminalPanel() []string {
	h, w := m.termPanelHeight(), m.width
	rows := make([]string, 0, h)
	for y := 0; y < h; y++ {
		var row terminalRow
		if y < len(m.termRows) {
			row = m.termRows[y]
		}
		cursorX := -1
		if m.termCursorOK && m.termCursorY == y {
			cursorX = m.termCursorX
		}
		selStart, selEnd := -1, -1
		if m.termSelActive {
			if s, e, ok := m.termSelRange(y, len(row.cells)); ok {
				selStart, selEnd = s, e
			}
		}
		rows = append(rows, renderTerminalRow(row, w, cursorX, selStart, selEnd))
	}
	return rows
}

// conflictLine renders the merge-conflict banner row.
func (m Model) conflictLine() string {
	fname := filepath.Base(m.conflictPath)
	line := statusHiStyle.Render(m.t("conflict.label")) + statusStyle.Render(fmt.Sprintf(m.t("conflict.msg"), fname))
	if len(m.conflictRows) > 0 {
		added, modified, deleted := 0, 0, 0
		for _, dr := range m.conflictRows {
			switch dr.Type {
			case vcs.DiffAdded:
				added++
			case vcs.DiffModified:
				modified++
			case vcs.DiffDeleted:
				deleted++
			}
		}
		line += hintStyle.Render(fmt.Sprintf("  +%d ~%d -%d", added, modified, deleted))
		line += hintStyle.Render(m.t("conflict.scroll"))
	}
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) searchLine() string {
	line := statusHiStyle.Render(m.t("search.label")) + statusStyle.Render(string(m.searchQuery)) + cursorStyle.Render(" ")
	if len(m.searchQuery) > 0 {
		if m.searchTotalMatches > 0 {
			line += hintStyle.Render(fmt.Sprintf(" [%d/%d]", m.searchMatchIdx+1, m.searchTotalMatches))
		} else {
			line += hintStyle.Render(m.t("search.none"))
		}
	}
	line += hintStyle.Render(m.t("search.hint"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) gotoLine() string {
	line := statusHiStyle.Render(m.t("goto.label")) + statusStyle.Render(string(m.gotoIn)) + cursorStyle.Render(" ")
	line += hintStyle.Render(m.t("goto.hint"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) replaceLine() string {
	findPart := statusHiStyle.Render(m.t("replace.find")) + statusStyle.Render(string(m.searchQuery))
	if m.replaceFocusFind {
		findPart += cursorStyle.Render(" ")
	} else {
		findPart += " "
	}

	repPart := statusHiStyle.Render(m.t("replace.with")) + statusStyle.Render(string(m.replaceWith))
	if !m.replaceFocusFind {
		repPart += cursorStyle.Render(" ")
	} else {
		repPart += " "
	}

	line := findPart + repPart
	if len(m.searchQuery) > 0 {
		if m.searchTotalMatches > 0 {
			line += hintStyle.Render(fmt.Sprintf(" [%d/%d]", m.searchMatchIdx+1, m.searchTotalMatches))
		} else {
			line += hintStyle.Render(m.t("search.none"))
		}
	}
	line += hintStyle.Render(m.t("replace.hint"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) finderPanel() []string {
	rows := make([]string, 0, len(m.finderHits)+1)
	for i, hit := range m.finderHits {
		label := " " + hit + " "
		if i == m.finderSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	line := statusHiStyle.Render(m.t("finder.prompt")) + statusStyle.Render(string(m.finderQ)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, line)
	return rows
}

// folderPanel renders the built-in folder picker: a path header, the
// directory listing (row 0 is always the parent), and a hint line.
func (m Model) folderPanel() []string {
	rows := make([]string, 0, folderVisible+2)
	head := statusHiStyle.Render(" " + m.t("folder.title") + m.folderPath + " ")
	fill := m.width - lipgloss.Width(head)
	if fill > 0 {
		head += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, head)

	// Row 0: parent directory.
	upLabel := m.t("folder.up")
	if m.folderSel == 0 {
		rows = append(rows, statusHiStyle.Render(padTo(" "+upLabel+" ", m.width)))
	} else {
		rows = append(rows, statusStyle.Render(padTo(" "+upLabel+" ", m.width)))
	}

	// Rows 1+: entries from the current directory.
	if len(m.folderEntries) == 0 {
		rows = append(rows, statusStyle.Render(padTo(" "+m.t("folder.empty")+" ", m.width)))
	} else {
		end := m.folderOffset + folderVisible
		if start := m.folderOffset; start < len(m.folderEntries) {
			if end > len(m.folderEntries) {
				end = len(m.folderEntries)
			}
			for i := start; i < end; i++ {
				e := m.folderEntries[i]
				label := " " + m.g.expand + " " + e.name + "/"
				if !e.dir {
					label = "   " + e.name
				}
				label = padTo(label+" ", m.width)
				if m.folderSel == i+1 {
					rows = append(rows, statusHiStyle.Render(label))
				} else if e.dir {
					rows = append(rows, statusStyle.Render(label))
				} else {
					rows = append(rows, hintStyle.Render(label))
				}
			}
		}
	}

	// Hint line.
	hint := statusStyle.Render(m.t("folder.hint"))
	fill = m.width - lipgloss.Width(hint)
	if fill > 0 {
		hint += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, hint)
	return rows
}

func (m Model) renderLine(p *pane, t *tab, ln, w int, activePane bool, syntaxLines []syntax.HighlightedLine) string {
	if w <= 0 || ln >= t.buf.LineCount() {
		return ""
	}
	raw := t.buf.LineAt(ln)
	var rawStyles syntax.HighlightedLine
	if ln < len(syntaxLines) {
		rawStyles = syntaxLines[ln]
	}

	// Expand tabs
	exp := make([]rune, 0, len(raw))
	expStyles := make([]lipgloss.Style, 0, len(raw))
	rawToExp := make([]int, len(raw)+1)

	for i, r := range raw {
		rawToExp[i] = len(exp)
		var st lipgloss.Style
		if i < len(rawStyles) {
			st = rawStyles[i]
		}
		if r == '\t' {
			for k := 0; k < m.cfg.Editor.TabWidth; k++ {
				exp = append(exp, ' ')
				expStyles = append(expStyles, st)
			}
		} else {
			exp = append(exp, r)
			expStyles = append(expStyles, st)
		}
	}
	rawToExp[len(raw)] = len(exp)

	// Search match highlighting
	type matchInfo struct {
		start int
		end   int
		isCur bool
	}
	var matches []matchInfo
	if len(m.searchQuery) > 0 {
		qLen := len(m.searchQuery)
		matchCols := findMatchesInRunes(raw, m.searchQuery)
		for _, col := range matchCols {
			expStart := rawToExp[col]
			expEnd := rawToExp[col+qLen]
			isCur := (ln == t.buf.CurLine() && col == t.buf.Col())
			matches = append(matches, matchInfo{start: expStart, end: expEnd, isCur: isCur})
		}
	}

	// All cursor positions and per-cursor selections on this line (active pane
	// only). Secondary cursors render as reverse cells; their selections use
	// the normal selection style.
	var carets []int
	var selRanges [][2]int
	if activePane {
		for _, c := range t.buf.Cursors() {
			if c.Line != ln {
				continue
			}
			if c.Col <= len(raw) {
				carets = append(carets, rawToExp[c.Col])
			} else if len(raw) >= 0 {
				carets = append(carets, rawToExp[len(raw)])
			}
			if c.From != c.To {
				cf, ct := c.From, c.To
				if cf > len(raw) {
					cf = len(raw)
				}
				if ct > len(raw) {
					ct = len(raw)
				}
				selRanges = append(selRanges, [2]int{rawToExp[cf], rawToExp[ct]})
			}
		}
	}

	// Visible window
	start := p.offsetX
	if start > len(exp) {
		start = len(exp)
	}
	end := start + w
	if end > len(exp) {
		end = len(exp)
	}

	// Selection range
	selStart := -1
	selEnd := -1
	if t.buf.HasSelection() {
		sl, sc, el, ec := t.buf.SelectionRange()
		if ln >= sl && ln <= el {
			if ln == sl {
				if sc <= len(raw) {
					selStart = rawToExp[sc]
				}
			} else {
				selStart = 0
			}

			if ln == el {
				if ec <= len(raw) {
					selEnd = rawToExp[ec]
				}
			} else {
				selEnd = len(exp)
			}
		}
	}

	var out strings.Builder
	for i := start; i < end; i++ {
		r := exp[i]
		st := expStyles[i]

		for _, mi := range matches {
			if i >= mi.start && i < mi.end {
				if mi.isCur {
					st = curMatchStyle
				} else {
					st = matchStyle
				}
				break
			}
		}

		if selStart >= 0 && i >= selStart && i < selEnd {
			st = selectionStyle
		} else {
			for _, rng := range selRanges {
				if i >= rng[0] && i < rng[1] {
					st = selectionStyle
					break
				}
			}
		}

		for _, cc := range carets {
			if i == cc {
				st = cursorStyle
				break
			}
		}

		out.WriteString(st.Render(string(r)))
	}

	// Cursor(s) at end of line
	drawn := false
	for _, cc := range carets {
		if cc == len(exp) && cc >= start && cc < start+w {
			if !drawn {
				out.WriteString(cursorStyle.Render(" "))
			}
			drawn = true
		}
	}

	// (search/selection/cursor overrides already won for their cells above.)

	return out.String()
}

func (m Model) langChooserPanel() []string {
	langs := i18n.Supported()
	rows := make([]string, 0, len(langs)+1)
	rows = append(rows, statusHiStyle.Render(m.t("lang.choose")))
	for i, l := range langs {
		label := " " + l.Native
		if l.Code == m.cfg.UI.Lang {
			label += m.t("lang.current")
		}
		label += "  "
		pad := m.width - lipgloss.Width(label)
		if pad < 0 {
			pad = 0
		}
		label += strings.Repeat(" ", pad)
		if i == m.langChooserSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	return rows
}

func (m Model) pluginStorePanel() []string {
	rows := make([]string, 0, len(m.storeItems)+2)
	rows = append(rows, statusHiStyle.Render(m.t("plugin.store_title")))
	for i, p := range m.storeItems {
		status := m.t("plugin.not_installed")
		if m.pluginInstalled(p.File) {
			status = m.t("plugin.installed")
		}
		src := ""
		if p.Remote {
			src = " [github]"
		}
		label := fmt.Sprintf(" %s — %s%s [%s] ", p.Name, p.Desc, src, status)
		pad := m.width - lipgloss.Width(label)
		if pad < 0 {
			pad = 0
		}
		label += strings.Repeat(" ", pad)
		if i == m.pluginStoreSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	if m.storeLoading {
		rows = append(rows, statusStyle.Render(m.t("plugin.loading")))
	} else if m.storeErr != "" {
		rows = append(rows, statusStyle.Render("plugin store: "+m.storeErr))
	}
	return rows
}

func (m Model) palettePanel() []string {
	const paletteVisible = 8
	hits := m.filterPalette()
	displayHits := hits
	total := len(displayHits)
	if total > paletteVisible {
		if m.paletteOffset > total-paletteVisible {
			m.paletteOffset = total - paletteVisible
		}
		if m.paletteOffset < 0 {
			m.paletteOffset = 0
		}
		displayHits = displayHits[m.paletteOffset : m.paletteOffset+paletteVisible]
	}
	rows := make([]string, 0, len(displayHits)+1)
	for i, hit := range displayHits {
		label := fmt.Sprintf(" %s — %s ", m.cmdTitle(hit), m.cmdDesc(hit))
		if m.paletteOffset+i == m.paletteSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	line := statusHiStyle.Render(" > ") + statusStyle.Render(string(m.paletteQ)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, line)
	return rows
}

func (m Model) statusBar() string {
	t := m.activeTab()
	// In a split each pane draws its own status line (name, Ln/Col, file
	// format) at the bottom of its cell, so the app-wide line must not repeat
	// that per-file state.
	perPane := m.layout != splitNone
	branchSuffix := ""
	if m.repo != nil {
		if b := m.repo.Branch(); b != "" {
			branchSuffix = " (" + b + ")"
		}
	}

	mid := ""
	// Chat mode shares this row with the editor but none of its business: a
	// "config reloaded" or "terminal: process exited" means nothing on a
	// screen that is the transcript, so the note waits for the editor. The
	// open git panel is the exception — its context line lives in the same
	// slot and the panel works in both modes.
	if m.msg != "" && (!m.Chat || m.gitOpen) {
		mid = statusStyle.Render("  " + m.msg)
	}
	right := ""
	// The scratch buffer has no file to report on, so it reports no line either:
	// "Ln 1, Col 1" on an empty screen is a cursor that is nowhere.
	//
	// A picture has no cursor either, and its line endings are not a question
	// anyone asks about a PNG, so it reports its size instead — which is the one
	// fact about the file the status bar is in a position to state.
	if !perPane && !m.Chat && !t.scratch() {
		if t.img != nil {
			right = " " + t.img.caption("") + " "
		} else {
			right = m.t("status.lncol", t.buf.CurLine()+1, t.buf.Col()+1)
		}
	}
	fileInfo := ""
	langTag := ""
	if !perPane && !m.Chat && t.path != "" && t.img == nil {
		endings := map[string]string{"lf": "LF", "crlf": "CRLF"}
		enc := strings.ToUpper(t.encoding)
		fileInfo = endings[t.lineEnding] + " " + enc + " "
		if lang := syntax.Lang(t.path); lang != "" {
			langTag = lang + " "
		}
	}
	hint := ""
	// Chat mode has no buffer of its own on screen: the transcript belongs to the
	// host and the cursor is nowhere near it, so Ln/Col, the encoding and the
	// language tag describe a file the user cannot see and did not ask about.
	// They also compete for the one row with the icon strip, which is the only
	// part of that row the user can act on. The F1 hint goes for the same reason
	// — it advertises editor keys on a screen that is not the editor.
	if !m.Chat && !m.promptOpen && !m.promptSave && !m.quitConfirm && !m.finderOpen && !m.searchOpen && !m.gotoOpen && !m.gitOpen && !m.conflictOpen && !m.diffViewOpen && !m.termOpen && !m.helpOpen {
		hint = m.t("status.f1_help")
		if m.layout != splitNone {
			hint += m.t("status.f8_pane")
		}
		if m.Embed {
			hint += m.t("status.embed_chat")
		}
	}
	rightBar := hintStyle.Render(hint) + langStyle.Render(langTag) + statusStyle.Render(fileInfo) + statusStyle.Render(right)

	// The active file name already lives in the tab bar, so the status line
	// only carries the icon strip, the split marker and the git branch.
	left := m.statusIconsString()
	if m.layout != splitNone {
		left += statusHiStyle.Render(fmt.Sprintf(" [%d] ", m.activePane+1))
	}
	if !perPane && branchSuffix != "" {
		left += hintStyle.Render(branchSuffix)
	}

	fill := m.width - lipgloss.Width(left) - lipgloss.Width(mid) - lipgloss.Width(rightBar)
	if fill > 0 {
		return left + mid + statusStyle.Render(strings.Repeat(" ", fill)) + rightBar
	}
	return left + mid + rightBar
}

func expandTabs(l []rune, tw int) []rune {
	out := make([]rune, 0, len(l))
	for _, r := range l {
		if r == '\t' {
			for i := 0; i < tw; i++ {
				out = append(out, ' ')
			}
		} else {
			out = append(out, r)
		}
	}
	return out
}

func visCol(l []rune, col int, tw int) int {
	x := 0
	for _, r := range l[:col] {
		if r == '\t' {
			x += tw
		} else {
			x++
		}
	}
	return x
}
