package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// suggestBox renders the command list as a bordered panel, in the same chrome as
// the ctrl+p palette, and anchored directly above the input box.
//
// It is drawn over the chat panel rather than added to the frame. As rows of the
// frame the list shrank the transcript every time a "/" was typed, jumped back
// when it closed, and on a short terminal ran into the bottom of the screen with
// the input still to fit. As a panel it costs the same on every terminal and
// nothing in the layout moves.
func (m *uiModel) suggestBox() string {
	// As wide as the chat panel, so it lines up with the text it sits above
	// instead of stretching under the sidebar or floating free of both.
	width := max(min(m.chatBoxWidth(), floatingBoxMaxWidth), 24)
	inner := width - panelBorder
	styleSel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))

	// The title names the keys, and the commands follow directly: a blank spacer
	// row here is a row the list cannot use, and on a short terminal it is the
	// difference between showing another command and showing the overflow marker.
	head := []string{
		styleHeader.Render(" "+i18n.T("⌘ commands")+" ") + styleHint.Render(i18n.T("↑↓ · enter · esc")),
	}

	rows := append([]string{}, head...)

	// How many rows the commands may occupy, and how many of them are actually
	// given to commands once the overflow marker has taken its row. Reserving it up
	// front rather than trimming afterwards is what keeps the highlight on screen:
	// a window trimmed from the bottom drops the selected row first, and the user
	// is left arrowing at a list with nothing marked in it.
	room := max(m.suggestHeight()-panelBorder-len(head), 1)
	show := room
	if len(m.suggest) > room && room > 1 {
		show = room - 1
	}

	// The window is anchored on the selection: it starts at the top and follows
	// the highlight down, so the selected command is always one of the drawn rows.
	start := 0
	if m.suggestSel >= start+show {
		start = m.suggestSel - show + 1
	}
	if start > len(m.suggest)-show {
		start = max(len(m.suggest)-show, 0)
	}
	limit := min(start+show, len(m.suggest))

	for i := start; i < limit; i++ {
		// The marker is the row's indent, so it is passed separately rather than
		// glued onto the text: wrapIndent then hangs the wrapped lines under the
		// text, and no marker is doubled up.
		marker := "   "
		if i == m.suggestSel {
			marker = " ▸ "
		}
		s := m.suggest[i]
		text := s.text
		if s.desc != "" {
			if room := inner - ansi.StringWidth(text) - 4; room >= 8 {
				text += "  — " + truncate(s.desc, room)
			}
		}
		line := wrapIndent(text, inner, marker, "     ")
		if i == m.suggestSel {
			for j := range line {
				line[j] = styleSel.Render(line[j])
			}
		}
		rows = append(rows, line...)
	}
	if more := len(m.suggest) - limit; more > 0 {
		rows = append(rows, styleHint.Render("   "+i18n.T("↓ more ")+fmt.Sprint(more)))
	}
	return stylePanel.Width(width).Render(strings.Join(rows, "\n"))
}

// floatingBoxMaxWidth keeps a dialog from stretching the full width of a wide
// terminal, where a list of short command names would sit in a vast empty box.
const floatingBoxMaxWidth = 68

// overlaySuggest paints the command dialog over the bottom of the chat panel,
// immediately above the input box.
//
// It rewrites rows of the frame that are already there rather than appending any,
// so the frame keeps exactly m.height rows and nothing in the interface moves:
// the input box, the status bar and the panel's top border are all still where
// they were. The dialog is anchored to the bottom because that is where the eye
// already is, next to the cursor.
//
// Only the chat panel's columns are rewritten. The sidebar shares those rows, and
// blanking it would take the model, the folder and the tool list away for as long
// as a "/" is on screen — the very information a user reaches for to decide what
// to type next. What is left of the row after the panel is kept verbatim.
func overlaySuggest(frame string, m *uiModel, rows int) string {
	lines := strings.Split(frame, "\n")
	// The dialog ends where the status bar starts: header + panel + status + input
	// is the whole frame, and the last row of the panel is the one above the badge.
	end := len(lines) - inputHeight - statusHeight
	start := max(end-rows, headerHeight)
	if end <= headerHeight || start >= end {
		return frame
	}

	box := strings.Split(m.suggestBox(), "\n")
	if len(box) > end-start {
		box = box[:end-start]
	}
	panel := m.chatBoxWidth()
	keep := max(m.width-panel, 0) // the sidebar's columns, or nothing when hidden
	for i, l := range box {
		row := lines[start+i]
		// The dialog is narrower than the panel on a wide terminal, so the gap
		// between it and the panel's right border is filled rather than left as a
		// ragged hole. TruncateLeft removes a count from the head rather than
		// keeping a tail, so the number is what has to come off.
		fill := panel - ansi.StringWidth(l)
		if fill < 0 {
			l = ansi.Truncate(l, panel, "")
			fill = 0
		}
		drop := ansi.StringWidth(row) - keep
		lines[start+i] = l + strings.Repeat(" ", fill) + ansi.TruncateLeft(row, max(drop, 0), "")
	}
	return strings.Join(lines, "\n")
}

// fitCells right-fills s with spaces to exactly w cells, and cuts it if it is
// floatingWidth is the outer width of the palette and the model picker. Both
// list model identifiers, which are unbounded, so their rows are wrapped inside
// a frame that fits the terminal instead of growing past it.
func (m *uiModel) floatingWidth() int {
	return max(min(m.width, 68), 24)
}

// floatPage is how far a page key moves inside a floating list: one screenful
// of entries. It is derived from the terminal because a fixed page is a step
// that jumps past the end of a short box and crawls through a tall one, and it
// is capped so one press cannot skip past a model the user was reaching for.
func (m *uiModel) floatPage() int {
	return max(min(m.height-panelBorder-6, 12), 1)
}

// listView is where a list is: the highlighted entry and the first entry on
// screen. top is state rather than something each frame re-derives, because a
// box exists only between two redraws — a window computed from the selection
// alone puts the cursor at the top of the body for every list longer than the
// terminal, and a wheel or a page key then has nothing to accumulate into.
type listView struct {
	sel int
	top int
}

// Every list drawn through here marks the row under the cursor with ▸ and the
// item currently in use with *, the same two glyphs in every overlay: one
// selection reading as two depending on which box is open is something a user
// has to learn twice.
//
// floatingPanel frames a palette-style box with the header every overlay
// shares: the title, the filter line and a blank spacer.
func (m *uiModel) floatingPanel(title, query string, entries [][]string, lv *listView) string {
	return m.floatingPanelAt(m.floatingWidth(), m.height, title, query, entries, lv)
}

// floatingPanelAt is floatingPanel at a width and a height the caller chooses.
// The question uses it to sit under the transcript at the terminal's full width:
// a box floating over the conversation hid the reply the question was about,
// which is the one thing a user needs to read before answering.
func (m *uiModel) floatingPanelAt(width, maxHeight int, title, query string, entries [][]string, lv *listView) string {
	inner := max(width-panelBorder, 8)
	head := []string{
		styleHeader.Render(" "+title+" ") + styleHint.Render(i18n.T("esc — close")),
		styleHint.Render(truncate(" › "+query, inner)),
		"",
	}
	return m.panelBox(width, maxHeight, head, entries, lv)
}

// floatingList is floatingPanel with a header the caller wrote. A list with more
// to say than a title and a filter line — which keys move it, how many of the
// endpoint's models survived the filter — says it in rows above the body rather
// than by growing the title into a paragraph that wraps off the narrow box.
//
// The body is windowed by rendered row rather than by entry, because one long
// model id can take two rows and a count taken before wrapping is not the count
// that reaches the screen.
func (m *uiModel) floatingList(head []string, entries [][]string, lv *listView) string {
	return m.panelBox(m.floatingWidth(), m.height, head, entries, lv)
}

// panelBox frames a list at a chosen width and height. floatingList keeps the
// full-screen case; the question uses it to fill the terminal's width while
// taking only the rows the transcript can spare.
func (m *uiModel) panelBox(width, maxHeight int, head []string, entries [][]string, lv *listView) string {
	styleSel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))

	// The head is measured rather than counted. The title shares its row with
	// the "esc — close" hint, so a narrow box wraps it onto two rows; a head
	// counted as three then leaves the panel one row taller than the screen it
	// has to fit inside, and the bottom border falls off the bottom.
	headRows := lipgloss.Height(stylePanel.Width(width).Render(strings.Join(head, "\n"))) - panelBorder

	// Two rows are held back, one for each way a list can still be scrolled, so
	// revealing a marker can never push the bottom border off the screen. A row
	// the finished window does not need is given back below, because a list of
	// six rows should not lose one of them to a marker it never draws.
	avail := max(maxHeight-panelBorder-headRows, 1)
	room := max(avail-2, 1)
	start, end := panelWindow(entries, lv.sel, lv.top, room)
	if (start == 0 || end >= len(entries)) && room < avail {
		room = avail - 1
		start, end = panelWindow(entries, lv.sel, lv.top, room)
	}
	lv.top = start

	rows := make([]string, 0, len(head)+room+2)
	rows = append(rows, head...)
	if start > 0 {
		rows = append(rows, styleHint.Render(i18n.T("   ↑ more ")+fmt.Sprint(start)))
	}
	left := room
	for i := start; i < end && left > 0; i++ {
		for _, r := range entries[i] {
			if left == 0 {
				break
			}
			if i == lv.sel {
				r = styleSel.Render(r)
			}
			rows = append(rows, r)
			left--
		}
	}
	if len(entries) == 0 {
		rows = append(rows, styleHint.Render(i18n.T("   nothing found")))
	}
	if end < len(entries) {
		rows = append(rows, styleHint.Render(i18n.T("   ↓ more ")+fmt.Sprint(len(entries)-end)))
	}

	return stylePanel.Width(width).Render(strings.Join(rows, "\n"))
}

// panelWindow picks the entries a body of `room` rows can show.
//
// Two rules, and the second is the one that was broken. The highlighted entry is
// always among them; and the list never grows to reach it. Trimming from the
// tail and stopping once the cursor was included did satisfy the first rule —
// by making the panel as tall as the distance to the cursor, at which point
// lipgloss.Place centres the overflow and the row under the cursor and the
// title of the box both end up off the screen. A list that showed nothing
// however far the user scrolled is what that looks like from the keyboard.
//
// An entry taller than the whole room is still shown, from its first row: it is
// the one carrying the cursor, and a row the user cannot scroll to is a dead
// end. The caller clips what does not fit.
func panelWindow(entries [][]string, sel, top, room int) (start, end int) {
	if len(entries) == 0 {
		return 0, 0
	}
	if room < 1 {
		room = 1
	}
	sel = min(max(sel, 0), len(entries)-1)
	start = min(max(top, 0), sel)

	// fill is the window that begins at `from`: as many whole entries as the
	// room holds, or that one entry alone when even it is taller than the room.
	fill := func(from int) (end, rows int) {
		end, rows = from, 0
		for end < len(entries) && rows+len(entries[end]) <= room {
			rows += len(entries[end])
			end++
		}
		if end == from {
			end, rows = from+1, len(entries[from])
		}
		return end, rows
	}
	end, rows := fill(start)

	// The cursor is below the window, so slide down one entry at a time until
	// it is on screen: the smallest move that reveals it, which is what a
	// single Down should read as. Jumping straight to the cursor instead would
	// make every keypress past the fold move the whole body.
	for sel >= end && start < sel {
		start++
		rows -= len(entries[start-1])
		for end < len(entries) && rows+len(entries[end]) <= room {
			rows += len(entries[end])
			end++
		}
		if end == start {
			end, rows = start+1, len(entries[start])
		}
	}
	return start, end
}

func (m *uiModel) paletteBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, c := range m.filteredCommands() {
		marker := "   "
		if i == m.palette.list.sel {
			marker = " ▸ "
		}
		entries = append(entries, wrapIndent(c.name+" — "+c.desc, inner, marker, "     "))
	}
	return m.floatingPanel(i18n.T("commands"), m.palette.query, entries, &m.palette.list)
}

// modelPickerBox renders /models: the endpoint's catalogue, narrowed.
//
// The header is built here rather than handed to floatingPanel because this list
// has three things to say that the shared one-row title cannot: which keys move
// it, how much of the catalogue the filter kept, and whether the free filter is
// on. On a list of three hundred models the count is not a nicety — it is the
// difference between "the list is short" and "you are looking at the first
// screen of something enormous".
func (m *uiModel) modelPickerBox() string {
	inner := m.floatingWidth() - panelBorder
	shown := m.filteredModels()

	// The hint is not truncated. The head is measured row by row, so letting it
	// wrap costs one row on a narrow box and keeps every key on it; cutting it to
	// fit hid the tail, which is where the newest and least-guessable key —
	// ctrl+f — lives, on exactly the boxes the user needs it most.
	hint := i18n.T("esc — close · ctrl+f — free only · ↑↓/pgup/pgdn — scroll")
	free := 0
	for _, id := range m.picker.Models {
		if config.FreeModel(m.prov, id) {
			free++
		}
	}
	// The filter line carries its own invitation. The keys above say how to move
	// and how to reach the free half; nothing said what typing does, and a list
	// of three hundred that silently shrinks is indistinguishable from a list of
	// three hundred — which is why the filter existed and nobody used it.
	filterLine := " › " + m.picker.query
	if m.picker.query == "" {
		filterLine = " › " + i18n.T("type to filter…")
	}
	// The count names the models, not the box, so it takes its own word: the
	// title's key carries the ⌘ with it, and borrowing it here printed the
	// title's glyph inside the count.
	counts := fmt.Sprintf("%d/%d %s · %d %s",
		len(shown), len(m.picker.Models), i18n.T("models"), free, i18n.T("free"))
	if m.picker.onlyFree {
		counts += " · " + i18n.T("free only")
	}
	head := []string{
		styleHeader.Render(" "+i18n.T("⌘ models")+" ") + styleHint.Render(hint),
		styleHint.Render(truncate(filterLine, inner)),
		styleHint.Render(counts),
		"",
	}

	var entries [][]string
	for i, id := range shown {
		// The marker is the first-line indent rather than part of the text, so
		// the rows stay inside the frame instead of being re-wrapped by it.
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.picker.list.sel:
			marker = " ▸ "
		case id == m.prov.Model:
			marker, style = " ● ", styleTool
		case config.FreeModel(m.prov, id):
			// Dimmed, with no marker of its own. A row already carries two
			// glyphs - the cursor and the model in use - and a third one
			// standing for "this one costs nothing" is a code to learn rather
			// than a fact. Weight says it without taking a column.
			style = styleHint
		}
		rows := wrapIndent(id, inner, marker, "     ")
		if i != m.picker.list.sel {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingList(head, entries, &m.picker.list)
}

// maskedKey hides a secret while still showing that something was typed. The
// width is capped so a pasted key cannot widen the overlay past the terminal.
func maskedKey(s string, width int) string {
	n := len([]rune(s))
	if n == 0 {
		return ""
	}
	if width > 1 && n > width-1 {
		n = width - 1
	}
	return strings.Repeat("•", n)
}

// ggufPickBox renders the file browser: the current directory in the header,
// then the parent, the subdirectories and the .gguf files of that directory.
func (m *uiModel) ggufPickBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, e := range m.setup.gguf.entries {
		marker, style := "   ", lipgloss.NewStyle()
		if i == m.setup.gguf.list.sel {
			marker = " ▸ "
		} else if e.dir {
			style = styleHint
		}
		rows := wrapIndent(e.label, inner, marker, "     ")
		if i != m.setup.gguf.list.sel {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(i18n.T("? gguf file — enter opens, esc back"), m.setup.gguf.dir, entries, &m.setup.gguf.list)
}

// setupBox renders the /setup wizard: the provider list, then a key prompt, or
// the base-URL and model prompts for a custom endpoint.
func (m *uiModel) setupBox() string {
	inner := m.floatingWidth() - panelBorder

	// The file browser paints over whatever prompt opened it, in the same
	// chrome as every other overlay; esc hands the prompt back.
	if m.setup.gguf.open {
		return m.ggufPickBox()
	}

	switch m.setup.stage {
	case setupKey:
		// The key is a secret: show how much has been typed, never what. A
		// shoulder-surfer or a screen share must not leak it, and the transcript
		// stores only the masked form.
		rows := [][]string{{styleHint.Render(i18n.T("key (input hidden, paste or ctrl+v, then enter):"))},
			{maskedKey(m.setup.buf, inner) + "█"}}
		return m.floatingPanel(i18n.T("? provider key"), "", rows, &listView{})

	case setupURL:
		rows := [][]string{{styleHint.Render(i18n.T("base URL, e.g. http://localhost:1234/v1"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? custom endpoint"), "", rows, &listView{})

	case setupModel:
		rows := [][]string{{styleHint.Render(i18n.T("model id on this endpoint"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? model"), "", rows, &listView{})

	case setupChoose:
		var entries [][]string
		for i, id := range m.setup.models {
			marker, st := "    ", lipgloss.NewStyle()
			if i == m.setup.list.sel {
				marker, st = " ▸ ", styleTool
			}
			rows := wrapIndent(id, inner, marker, "     ")
			if i != m.setup.list.sel {
				for j := range rows {
					rows[j] = st.Render(rows[j])
				}
			}
			entries = append(entries, rows)
		}
		entries = append(entries, []string{styleHint.Render(i18n.T("enter — choose, esc — type an id instead"))})
		return m.floatingPanel(i18n.T("? model on this endpoint"), "", entries, &m.setup.list)

	case setupGGUF:
		rows := [][]string{{styleHint.Render(i18n.T("path to the .gguf file — ctrl+o or enter on empty browses the disk"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? gguf file"), "", rows, &listView{})
	}

	opts := config.SetupOptions()
	var entries [][]string
	for i, o := range opts {
		marker, style := "   ", lipgloss.NewStyle()
		// ▸ for the cursor and * for the provider in use, as everywhere else;
		// see floatingPanel. "?" stayed out of it because it already prefixes
		// every wizard prompt's title.
		switch {
		case i == m.setup.list.sel:
			marker, style = " ▸ ", styleTool
		case setupLabel(o) == m.prov.Label:
			marker, style = " * ", styleTool
		}
		rows := wrapIndent(o.Label, inner, marker, "     ")
		if i != m.setup.list.sel {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	// * marks the provider already in use, ? the highlighted row.
	return m.floatingPanel(i18n.T("? provider  (* — current, enter — select, esc — cancel)"), "", entries, &m.setup.list)
}

// langLabels names each selectable language in that language itself, so the row
// is readable no matter which one is active — a list of English names would
// leave a Russian user hunting for "Russian".
var langLabels = map[i18n.Lang]string{
	i18n.English: "English",
	i18n.Russian: "Русский",
}

// openLangPicker opens /lang with the active language already highlighted.
func (m *uiModel) openLangPicker() tea.Cmd {
	m.lang.open = true
	for i, l := range i18n.Langs {
		if l == i18n.Current() {
			m.lang.selected = i
			break
		}
	}
	return nil
}

// langKey drives the /lang overlay. The choice takes effect immediately and is
// persisted, so the rest of the session — and the next start — use it.
func (m *uiModel) langKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+p", "q":
		m.lang.open = false
		return m, nil
	case "up":
		if m.lang.selected > 0 {
			m.lang.selected--
		}
		return m, nil
	case "down":
		if m.lang.selected < len(i18n.Langs)-1 {
			m.lang.selected++
		}
		return m, nil
	case "enter":
		m.lang.open = false
		if m.lang.selected >= len(i18n.Langs) {
			return m, nil
		}
		l := i18n.Langs[m.lang.selected]
		if err := i18n.Set(l); err != nil {
			// The switch already applies for this session; only the persistence
			// failed, so say so and carry on rather than treating it as fatal.
			m.statusText = i18n.T("could not save the language choice")
			m.history = append(m.history, line{kindErr, err.Error()})
		}
		m.history = append(m.history, line{kindSys, i18n.T("language: ") + langLabels[l]})
		// The banner and status text are rendered on every frame, but the
		// welcome block is printed once, so refresh it to show the new language.
		m.printWelcome()
		m.historyDirty = true
		return m, nil
	}
	return m, nil
}

// langBox renders the /lang list: * marks the active language, ▸ the cursor.
func (m *uiModel) langBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, l := range i18n.Langs {
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.lang.selected:
			marker, style = " ▸ ", styleTool
		case l == i18n.Current():
			marker, style = " * ", styleTool
		}
		rows := wrapIndent(langLabels[l], inner, marker, "     ")
		if i != m.lang.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(i18n.T("? language  (* — current, enter — select, esc — cancel)"), "", entries, &listView{sel: m.lang.selected})
}
