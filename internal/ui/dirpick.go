package ui

// dirpick.go — the /cd folder browser. A path is not a choice: a user who has
// to type a folder they have not memorised has to open another terminal to
// look at it first. Bare /cd opens a directory browser instead, and /cd <path>
// keeps the typed fast path — the same split /proxy made between its dialog
// and its one-line form.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// dirPickState browses the disk for the folder /cd should move to. It lives on
// the model so the position and the highlight survive redraws, the way every
// other overlay's state does.
type dirPickState struct {
	open     bool
	dir      string
	entries  []dirEntry
	selected int
}

// dirEntry is one row of the folder browser. The first row is the action that
// picks the folder shown in the header; every other row is a directory entered
// by pressing enter.
type dirEntry struct {
	label  string
	path   string
	action bool
}

// openDirPick brings up the browser at the session's current folder, which is
// the only starting point that is never wrong: the folder the user is already
// working in is either where they meant to go or the root of where they meant
// to go from.
func (m *uiModel) openDirPick() tea.Cmd {
	m.dirPick = dirPickState{open: true}
	m.loadDirPick(m.workDir)
	return nil
}

// loadDirPick reads dir into the browser. A failed read keeps the old listing
// on screen and reports the reason, so a permission problem does not blank out
// the dialog the user is mid-navigation in.
func (m *uiModel) loadDirPick(dir string) {
	entries, err := readDirPick(dir)
	if err != nil {
		m.statusText = i18n.T("could not read the directory: ") + err.Error()
		return
	}
	m.dirPick.dir = dir
	m.dirPick.entries = entries
	m.dirPick.selected = 0
}

// readDirPick lists what the browser shows: the "use this folder" action for
// the current directory, the parent, then the subdirectories of this directory,
// sorted. Dotfiles are noise for navigation — the same rule the GGUF browser
// applies — and files are not offered at all: the destination of /cd is always
// a directory.
func readDirPick(dir string) ([]dirEntry, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sep := string(filepath.Separator)
	var dirs []dirEntry
	for _, e := range dirEntries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, dirEntry{label: name + sep, path: filepath.Join(dir, name)})
	}
	slices.SortFunc(dirs, func(a, b dirEntry) int { return strings.Compare(a.label, b.label) })

	out := make([]dirEntry, 0, len(dirs)+2)
	out = append(out, dirEntry{label: i18n.T("→ use this folder"), path: dir, action: true})
	if parent := filepath.Dir(dir); parent != dir {
		out = append(out, dirEntry{label: ".." + sep, path: parent})
	}
	return append(out, dirs...), nil
}

// dirPickKey drives the folder browser. enter descends into a directory or
// picks the folder named in the header — the pick flows straight into
// changeDir, the same path a hand-typed /cd target takes. esc closes the
// browser and hands the prompt back.
func (m *uiModel) dirPickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.dirPick
	switch msg.String() {
	case "esc":
		p.open = false
		return m, nil
	case "up":
		if p.selected > 0 {
			p.selected--
		}
		return m, nil
	case "down":
		if p.selected < len(p.entries)-1 {
			p.selected++
		}
		return m, nil
	case "enter":
		if p.selected >= len(p.entries) {
			return m, nil
		}
		e := p.entries[p.selected]
		if e.action {
			p.open = false
			return m, m.changeDir(e.path)
		}
		m.loadDirPick(e.path)
		return m, nil
	}
	return m, nil
}

// dirPickBox renders the folder browser: the current directory in the header,
// then the pick action, the parent and the subdirectories of that directory.
func (m *uiModel) dirPickBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, e := range m.dirPick.entries {
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.dirPick.selected:
			marker = " ▸ "
		case e.action:
			// The pick row is the answer to the question the header asks, so it
			// reads as the option rather than as a place to go.
			style = styleTool
		default:
			style = styleHint
		}
		rows := wrapIndent(e.label, inner, marker, "     ")
		if i != m.dirPick.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(i18n.T("choose a folder — enter opens, esc back"), m.dirPick.dir, entries, m.dirPick.selected)
}
