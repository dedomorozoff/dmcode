package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// helpState drives the F1 reference: an open flag and a scroll offset. The
// content is static — there is nothing to filter or type — so the only state
// is where the user is looking.
type helpState struct {
	open bool
	top  int // first visible row, in rendered rows
}

// openHelp raises the F1 reference from the top.
func (m *uiModel) openHelp() {
	m.help.open = true
	m.help.top = 0
}

// helpKey drives the overlay. The reference is read-only: a key it does not
// recognise is ignored rather than typed into the hidden prompt underneath.
func (m *uiModel) helpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "f1", "ctrl+p", "q":
		m.help.open = false
		m.help.top = 0
		return m, nil
	case "ctrl+c":
		return m, m.quitCmd()
	case "up", "pgup":
		m.help.top = max(m.help.top-10, 0)
		return m, nil
	case "down", "pgdown":
		m.help.top += 10
		return m, nil
	}
	return m, nil
}

// helpBox renders the reference in the same chrome as every other overlay,
// with the head measured rather than counted so a narrow box cannot grow past
// the screen it has to fit inside.
func (m *uiModel) helpBox() string {
	head := styleHeader.Render(" "+i18n.T("? help")+" ") + styleHint.Render(i18n.T("esc — close · pgup/pgdown — scroll"))
	body := m.helpRows(m.floatingWidth() - panelBorder)

	// Two head rows (the title and a blank spacer), and one row held back for
	// the overflow marker so revealing it can never push the border off the
	// screen — the same arithmetic floatingPanel uses.
	headRows := lipgloss.Height(stylePanel.Width(m.floatingWidth()).Render(strings.Join([]string{head, ""}, "\n"))) - panelBorder
	room := max(m.height-panelBorder-headRows-1, 1)

	rows := []string{head, ""}
	if len(body) <= room {
		rows = append(rows, body...)
		return stylePanel.Width(m.floatingWidth()).Render(strings.Join(rows, "\n"))
	}

	if m.help.top > len(body)-room {
		m.help.top = max(len(body)-room, 0)
	}
	end := m.help.top + room
	rows = append(rows, body[m.help.top:end]...)
	rows = append(rows, styleHint.Render(fmt.Sprintf("   ↓ %d %s", len(body)-end, i18n.T("more"))))
	return stylePanel.Width(m.floatingWidth()).Render(strings.Join(rows, "\n"))
}

// helpKeyColumn is how wide the key column is, so the descriptions line up in
// one readable column instead of a ragged left edge.
const helpKeyColumn = 12

// helpRows builds the whole reference as styled rows. Every description is a
// translated key, so a Russian interface shows Russian text; the key labels
// themselves are universal and stay as they are.
func (m *uiModel) helpRows(inner int) []string {
	var rows []string
	section := func(label string) {
		rows = append(rows, styleSidebarLabel.Render(i18n.T(label)))
	}
	entry := func(key, desc string) {
		col := key
		if w := ansi.StringWidth(col); w < helpKeyColumn {
			col += strings.Repeat(" ", helpKeyColumn-w)
		} else {
			col += " "
		}
		line := col + i18n.T(desc)
		for _, r := range wrapIndent(line, inner, "", strings.Repeat(" ", helpKeyColumn+1)) {
			rows = append(rows, styleHint.Render(r))
		}
	}

	section("KEYS")
	entry("ctrl+p", "commands")
	entry("ctrl+b", "project panel")
	entry("ctrl+e", "code editor")
	entry("ctrl+y", "copy the reply")
	entry("ctrl+z", "undo the last message")
	entry("ctrl+l", "clear the screen")
	entry("ctrl+n", "new session")
	entry("ctrl+v", "paste a picture from the clipboard")
	entry("alt+g", "open the next change at that line")
	entry("tab", "plan/act mode")
	entry("shift+tab", "yolo mode — act without questions")
	entry("esc", "stop the turn, close a dialog")
	entry("↑/↓", "prompt history")
	entry("pgup/pgdn", "scroll · home/end — top/bottom")
	entry("f1", "this reference")
	rows = append(rows, "")

	section("COMMANDS")
	entry("/setup", "choose a provider")
	entry("/models", "list the models · /model <id> — switch")
	entry("/editor", "open the code editor")
	entry("/changes", "next change in the editor")
	entry("/cd <path>", "change the working folder")
	entry("/image <path>", "attach a picture")
	entry("/unimage", "drop the last picture")
	entry("/copy", "copy the agent's reply")
	entry("/new [name]", "start a new session")
	entry("/sessions", "switch between saved sessions")
	entry("/resume <id>", "open a saved session")
	entry("/rewind", "undo the last message")
	entry("/clear", "clear the screen")
	entry("/history", "recent prompts")
	entry("/mode", "switch plan/act mode")
	entry("/sidebar", "toggle the project panel")
	entry("/mouse", "toggle wheel scrolling")
	entry("/stats", "turn timing and context")
	entry("/todo", "the current plan")
	entry("/tools", "list the tools")
	entry("/proxy", "HTTP proxy dialog")
	entry("/lang", "interface language")
	entry("/debug", "transcript diagnostics")
	entry("/help", "print this reference to the transcript")
	entry("/quit", "exit dmcode")
	return rows
}
