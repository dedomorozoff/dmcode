package editor

import (
	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/theme"
)

// ApplyTheme rebuilds the editor's chrome styles from the host palette. Syntax
// colours stay the editor's own: a syntax theme is a different choice from the
// interface palette, and replacing it here would silently undo the one the user
// configured for code.
//
// The default palette leaves the editor exactly as it was. The editor's historic
// chrome has a few values of its own — a white current-line number, a blue
// selection — that are not part of the chat's palette, and a session that never
// chooses a theme must not have them repainted by adding the feature.
func ApplyTheme(p theme.Palette) {
	if p.Name == theme.DefaultName {
		gutterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		curGutterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
		statusStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250"))
		frameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		statusHiStyle = lipgloss.NewStyle().Background(lipgloss.Color("61")).Foreground(lipgloss.Color("255")).Bold(true)
		langStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
		hintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
		activePaneStyle = lipgloss.NewStyle().Background(lipgloss.Color("235"))
		matchStyle = lipgloss.NewStyle().Background(lipgloss.Color("214")).Foreground(lipgloss.Color("0"))
		curMatchStyle = lipgloss.NewStyle().Background(lipgloss.Color("226")).Foreground(lipgloss.Color("0")).Bold(true)
		gitAddStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
		gitModStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
		gitDelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
		diagErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
		diagWarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
		diagInfoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
		bmStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
		okTestStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
		errTestStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
		diffAddBg = lipgloss.NewStyle().Background(lipgloss.Color("22"))
		diffDelBg = lipgloss.NewStyle().Background(lipgloss.Color("52"))
		diffModBg = lipgloss.NewStyle().Background(lipgloss.Color("58"))
		selectionStyle = lipgloss.NewStyle().Background(lipgloss.Color("60")).Foreground(lipgloss.Color("255"))
		blameStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))

		treeConnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		treeIconStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
		treeDirStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
		treeFileStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("253"))
		return
	}

	fg := p.Foreground
	if fg == nil {
		fg = lipgloss.Color("255")
	}
	bold := p.Bold
	if bold == nil {
		bold = p.Warning
	}
	gutterStyle = lipgloss.NewStyle().Foreground(p.Dim)
	curGutterStyle = lipgloss.NewStyle().Foreground(fg).Bold(true)
	statusStyle = lipgloss.NewStyle().Background(p.BarBG).Foreground(p.BarFG)
	frameStyle = lipgloss.NewStyle().Foreground(p.Border)
	statusHiStyle = lipgloss.NewStyle().Background(p.Tool).Foreground(p.BadgeFG).Bold(true)
	langStyle = lipgloss.NewStyle().Foreground(fg).Bold(true)
	hintStyle = lipgloss.NewStyle().Foreground(p.Dim)
	activePaneStyle = lipgloss.NewStyle().Background(p.BarBG)
	matchStyle = lipgloss.NewStyle().Background(p.Warning).Foreground(p.BadgeFG)
	curMatchStyle = lipgloss.NewStyle().Background(p.Bold).Foreground(p.BadgeFG).Bold(true)
	if p.Bold == nil {
		curMatchStyle = lipgloss.NewStyle().Background(p.Warning).Foreground(p.BadgeFG).Bold(true)
	}
	gitAddStyle = lipgloss.NewStyle().Foreground(p.Success).Bold(true)
	gitModStyle = lipgloss.NewStyle().Foreground(p.Warning).Bold(true)
	gitDelStyle = lipgloss.NewStyle().Foreground(p.Error).Bold(true)
	diagErrStyle = lipgloss.NewStyle().Foreground(p.Error).Bold(true)
	diagWarnStyle = lipgloss.NewStyle().Foreground(p.Warning).Bold(true)
	diagInfoStyle = lipgloss.NewStyle().Foreground(p.Tool)
	bmStyle = lipgloss.NewStyle().Foreground(bold).Bold(true)
	okTestStyle = lipgloss.NewStyle().Foreground(p.Success)
	errTestStyle = lipgloss.NewStyle().Foreground(p.Error)
	diffAddBg = lipgloss.NewStyle().Background(p.DiffAddBG)
	diffDelBg = lipgloss.NewStyle().Background(p.DiffDelBG)
	diffModBg = lipgloss.NewStyle().Background(p.Warning)
	selectionStyle = lipgloss.NewStyle().Background(p.Select).Foreground(p.Agent)
	blameStyle = lipgloss.NewStyle().Faint(true).Foreground(p.Dim)

	treeConnStyle = lipgloss.NewStyle().Foreground(p.Dim)
	treeIconStyle = lipgloss.NewStyle().Foreground(p.Warning)
	treeDirStyle = lipgloss.NewStyle().Foreground(p.Tool).Bold(true)
	treeFileStyle = lipgloss.NewStyle().Foreground(p.Agent)
}

// ApplyTheme is the model-bound spelling of the package function, so a host
// holding a Model can switch the palette without importing the globals' name.
func (m *Model) ApplyTheme(p theme.Palette) { ApplyTheme(p) }
