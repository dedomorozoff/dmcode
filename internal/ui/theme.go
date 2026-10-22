package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/theme"
)

// applyTheme rebuilds every package-level style from p. The styles are globals
// because a hundred call sites already read them; rebuilding them in one place
// is what keeps a runtime switch from leaving half the interface on the old
// palette.
//
// A nil Background/Foreground is deliberate: the default palette does not
// repaint the terminal, so a session that never chooses a theme keeps the look
// it always had.
func applyTheme(p theme.Palette) {
	cUser = p.User
	cAgent = p.Agent
	cTool = p.Tool
	cDim = p.Dim
	cErr = p.Error
	cAccent = p.Accent
	cBorder = p.Border
	cSuccess = p.Success
	cWarning = p.Warning
	cBgBar = p.BarBG
	cFgBar = p.BarFG
	cDiffAddBg = p.DiffAddBG
	cDiffDelBg = p.DiffDelBG

	styleUser = lipgloss.NewStyle().Bold(true).Foreground(cUser)
	styleAgent = lipgloss.NewStyle().Foreground(cAgent)
	styleTool = lipgloss.NewStyle().Foreground(cTool)
	styleToolRes = lipgloss.NewStyle().Foreground(cDim)
	styleSys = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	styleErr = lipgloss.NewStyle().Bold(true).Foreground(cErr)
	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	styleHint = lipgloss.NewStyle().Foreground(cDim)
	styleSuggest = lipgloss.NewStyle().Bold(true).Foreground(cTool)
	styleAdd = lipgloss.NewStyle().Foreground(cSuccess)
	styleDel = lipgloss.NewStyle().Foreground(cErr)

	styleBadgeReady = lipgloss.NewStyle().Bold(true).Foreground(p.BadgeFG).Background(cSuccess).Padding(0, 1)
	styleBadgeBusy = lipgloss.NewStyle().Bold(true).Foreground(p.BadgeFG).Background(cWarning).Padding(0, 1)
	styleBadgeStop = lipgloss.NewStyle().Bold(true).Foreground(p.BadgeFG).Background(cErr).Padding(0, 1)

	styleStatusBar = lipgloss.NewStyle().Background(cBgBar).Foreground(cFgBar).Padding(0, 1)

	styleSidebar = lipgloss.NewStyle().Border(boxBorder, true).BorderForeground(cBorder).Padding(0, 1).MarginLeft(sidebarGap)
	styleSidebarLabel = lipgloss.NewStyle().Bold(true).Foreground(cTool)
	styleSidebarValue = lipgloss.NewStyle().Foreground(cAgent)

	stylePanel = lipgloss.NewStyle().Border(boxBorder, true).BorderForeground(cBorder)
	styleTopBar = lipgloss.NewStyle().Border(boxBorder, false, false, true, false).BorderForeground(cBorder).Padding(0, 1)

	styleSelect = lipgloss.NewStyle().Bold(true).Foreground(p.Select)
	styleLogo = lipgloss.NewStyle().Foreground(p.Select).Bold(true)
	styleVersion = lipgloss.NewStyle().Foreground(p.Select)

	// Markdown. Bold prose carries the theme's own bold colour when it has one;
	// with the default palette the semantic colours are kept and emphasis is the
	// historic bright yellow.
	emph := p.Bold
	if emph == nil {
		emph = lipgloss.Color("14")
	}
	cEmph = emph
	styleH1 = lipgloss.NewStyle().Bold(true).Foreground(p.Accent)
	styleH2 = lipgloss.NewStyle().Bold(true).Foreground(p.Agent)
	styleH3 = lipgloss.NewStyle().Bold(true)
	if p.Bold != nil {
		styleH3 = styleH3.Foreground(p.Bold)
	}
	styleCodeBlk = lipgloss.NewStyle().Foreground(p.Agent)
	styleBullet = lipgloss.NewStyle().Foreground(p.Dim)
	styleRule = lipgloss.NewStyle().Foreground(p.Border)
	styleItalic = lipgloss.NewStyle().Italic(true)

	// Mode badges (mode.go). They are rebuilt here with the rest so switching a
	// theme does not leave the badge on the palette the session started with.
	styleBadgeAct = lipgloss.NewStyle().Bold(true).Foreground(p.BadgeFG).Background(p.Success).Padding(0, 1)
	styleBadgePlan = lipgloss.NewStyle().Bold(true).Foreground(p.BadgeFG).Background(p.Tool).Padding(0, 1)
	styleBadgeYolo = lipgloss.NewStyle().Bold(true).Foreground(p.BadgeFG).Background(p.Error).Padding(0, 1)
}

// applyThemeEverywhere switches the palette globally and hands it to the
// workspace editor if it exists. The editor has its own package-level styles
// for chrome that must move with the chat's.
func (m *uiModel) applyThemeEverywhere(p theme.Palette) {
	applyTheme(p)
	if m.ed != nil {
		m.ed.ApplyTheme(p)
	}
}

func init() {
	applyTheme(theme.Current())
}
