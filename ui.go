package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

type lineKind int

const (
	kindUser lineKind = iota
	kindAgent
	kindTool
	kindToolRes
	kindSys
	kindErr
	kindLogo
)

type line struct {
	kind lineKind
	text string
}

var (
	cUser    = lipgloss.Color("12")
	cAgent   = lipgloss.Color("15")
	cTool    = lipgloss.Color("6")
	cDim     = lipgloss.Color("8")
	cErr     = lipgloss.Color("9")
	cAccent  = lipgloss.Color("5")
	cBorder  = lipgloss.Color("240")
	cSuccess = lipgloss.Color("10")
	cWarning = lipgloss.Color("11")
	cBgBar   = lipgloss.Color("236")
	cFgBar   = lipgloss.Color("253")

	styleUser    = lipgloss.NewStyle().Bold(true).Foreground(cUser)
	styleAgent   = lipgloss.NewStyle().Foreground(cAgent)
	styleTool    = lipgloss.NewStyle().Foreground(cTool)
	styleToolRes = lipgloss.NewStyle().Foreground(cDim)
	styleSys     = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	styleErr     = lipgloss.NewStyle().Bold(true).Foreground(cErr)
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	styleHint    = lipgloss.NewStyle().Foreground(cDim)
	styleSuggest = lipgloss.NewStyle().Bold(true).Foreground(cTool)

	styleBadgeReady = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(cSuccess).Padding(0, 1)
	styleBadgeBusy  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(cWarning).Padding(0, 1)
	styleBadgeStop  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(cErr).Padding(0, 1)

	styleStatusBar = lipgloss.NewStyle().Background(cBgBar).Foreground(cFgBar).Padding(0, 1)

	boxBorder = lipgloss.NormalBorder()

	// The sidebar carries a one-column left margin so its border never touches
	// the chat panel's; without it the two boxes read as one ragged block.
	styleSidebar      = lipgloss.NewStyle().Border(boxBorder, true).BorderForeground(cBorder).Padding(0, 1).MarginLeft(sidebarGap)
	styleSidebarLabel = lipgloss.NewStyle().Bold(true).Foreground(cTool)
	styleSidebarValue = lipgloss.NewStyle().Foreground(cAgent)

	stylePanel  = lipgloss.NewStyle().Border(boxBorder, true).BorderForeground(cBorder)
	styleTopBar = lipgloss.NewStyle().Border(boxBorder, false, false, true, false).BorderForeground(cBorder).Padding(0, 1)
)

// Frame geometry. lipgloss treats a style's Width/Height as the *total* block
// size with borders already included, so every number here is an outer
// measurement and the inner sizes are derived from it. Getting this backwards
// is what left the sidebar two rows short of the chat panel and every row of
// the frame a few columns narrower than the terminal.
const (
	sidebarBoxWidth = 24 // outer width of the sidebar box, margins excluded
	sidebarGap      = 1  // blank columns between the chat panel and the sidebar
	minSidebarTerm  = 90 // below this terminal width the sidebar is dropped
	minChatWidth    = 36 // the chat panel is not squeezed below this

	headerHeight = 2 // 1 row of text + 1 bottom border
	statusHeight = 1 // single row
	inputHeight  = 3 // 1 row of text + 2 borders
	panelBorder  = 2 // the chat panel's top and bottom border
	minViewRows  = 1 // a framed panel is an empty row between two borders

	// chromeHeight is everything on screen that is not transcript: the header,
	// the status bar, the input box and the chat panel's own two borders.
	chromeHeight = headerHeight + statusHeight + inputHeight + panelBorder
)

// statusHotkeys are dropped from the status bar one by one as the terminal
// narrows, so the model name survives longest.
var statusHotkeys = []string{"ctrl+p", "ctrl+b", "ctrl+y", "esc"}

type deltaMsg struct{ text string }
type toolCallMsg struct {
	name string
	args string
}
type toolResMsg struct {
	name   string
	output string
}
type turnDoneMsg struct{ err error }

type modelsListMsg struct {
	models []string
	err    error
}

type modelSwitchedMsg struct {
	name   string
	runner *runner.Runner
	err    error
}

type errMsg string

func (e errMsg) Error() string { return string(e) }

type uiModel struct {
	history   []line
	input     textinput.Model
	vp        viewport.Model
	spin      spinner.Model
	busy      bool
	width     int
	height    int
	sessionID string
	runner    *runner.Runner
	svc       session.Service
	prov      provider
	tools     []tool.Tool
	prog      *tea.Program
	toolNames []string
	palette   paletteState
	picker    modelPicker
	suggest   []string
	stick     bool

	// UX & state components
	showSidebar   bool
	cancelTurn    context.CancelFunc
	turnCount     int
	toolCallCount int
	lastTool      string
	statusText    string
	workDir       string

	// History render cache for streaming performance
	cachedHistory string
	cachedWidth   int
	historyDirty  bool
}

type modelPicker struct {
	open     bool
	query    string
	selected int
	models   []string
}

type paletteState struct {
	open     bool
	query    string
	selected int
}

type command struct {
	name string
	desc string
	run  func(m *uiModel) tea.Cmd
}

func (m *uiModel) commands() []command {
	return []command{
		{name: "models", desc: "список моделей", run: func(m *uiModel) tea.Cmd {
			return m.fetchModelsCmd()
		}},
		{name: "copy", desc: "скопировать ответ агента (ctrl+y)", run: func(m *uiModel) tea.Cmd {
			m.copyLastResponse()
			return nil
		}},
		{name: "sidebar", desc: "боковая панель (ctrl+b)", run: func(m *uiModel) tea.Cmd {
			m.showSidebar = !m.showSidebar
			m.layout()
			m.followVP()
			return nil
		}},
		{name: "new", desc: "новая сессия", run: func(m *uiModel) tea.Cmd {
			m.sessionID = newSessionID()
			m.turnCount = 0
			m.toolCallCount = 0
			m.history = append(m.history, line{kindSys, "— сессия сброшена —"})
			m.historyDirty = true
			return nil
		}},
		{name: "clear", desc: "очистить экран", run: func(m *uiModel) tea.Cmd {
			m.history = nil
			m.historyDirty = true
			return nil
		}},
		{name: "help", desc: "подсказки", run: func(m *uiModel) tea.Cmd {
			m.history = append(m.history,
				line{kindSys, "ctrl+p — команды · ctrl+b — панель · ctrl+y — копировать ответ"},
				line{kindSys, "esc — прервать текущий ход · pgup/pgdown — скролл"},
				line{kindSys, "мышь — выделение и копирование текста прямо в терминале"},
				line{kindSys, "/models, /model <id>, /copy, /sidebar, /new, /clear, /quit"})
			m.historyDirty = true
			return nil
		}},
		{name: "tools", desc: "список инструментов", run: func(m *uiModel) tea.Cmd {
			for _, t := range m.toolNames {
				m.history = append(m.history, line{kindSys, "· " + t})
			}
			m.historyDirty = true
			return nil
		}},
		{name: "quit", desc: "выход", run: func(m *uiModel) tea.Cmd { return tea.Quit }},
	}
}

const logo = `  ██████╗ ███╗   ███╗ ██████╗ ██████╗ ██████╗ ███████╗
  ██╔════╝ ████╗ ████║██╔════╝██╔═══██╗██╔══██╗██╔════╝
  ██║  ███╗██╔████╔██║██║     ██║   ██║██║  ██║█████╗
  ██║   ██║██║╚██╔╝██║██║     ██║   ██║██║  ██║██╔══╝
  ╚██████╔╝██║ ╚═╝ ██║╚██████╗╚██████╔╝██████╔╝███████╗
   ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝  coding agent`

var styleLogo = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)

func newSessionID() string {
	return fmt.Sprintf("sess-%d", rand.Int63())
}

func initialModel(r *runner.Runner, svc session.Service, p provider, tools []tool.Tool, toolNames []string) *uiModel {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "опиши задачу… (/help — команды, esc — отмена)"
	ti.Focus()
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	vp := viewport.New(viewport.WithHeight(10))

	wd, _ := os.Getwd()
	if wd == "" {
		wd = "."
	}

	m := &uiModel{
		input:        ti,
		spin:         sp,
		vp:           vp,
		stick:        true,
		sessionID:    newSessionID(),
		runner:       r,
		svc:          svc,
		prov:         p,
		tools:        tools,
		toolNames:    toolNames,
		showSidebar:  true,
		workDir:      filepath.Base(wd),
		historyDirty: true,
	}
	m.history = append(m.history,
		line{kindSys, ""},
		line{kindLogo, logo},
		line{kindSys, ""},
		line{kindSys, "ctrl+p команды · ctrl+b панель · ctrl+y копировать · esc отмена"},
		line{kindSys, ""})
	return m
}

func (m *uiModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spin.Tick, tea.RequestWindowSize)
}

func (m *uiModel) lastAgentText() string {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].kind == kindAgent && strings.TrimSpace(m.history[i].text) != "" {
			return m.history[i].text
		}
	}
	return ""
}

func (m *uiModel) copyLastResponse() {
	text := m.lastAgentText()
	if text == "" {
		m.statusText = "нет ответа для копирования"
		return
	}
	if err := clipboard.WriteAll(text); err != nil {
		m.history = append(m.history, line{kindErr, "ошибка буфера: " + err.Error()})
	} else {
		m.statusText = "ответ скопирован в буфер обмена!"
		m.history = append(m.history, line{kindSys, "📋 последний ответ скопирован в буфер обмена"})
	}
	m.historyDirty = true
	m.followVP()
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// No followVP here on purpose: the tail of Update re-syncs the viewport
		// for every message, which is what re-wraps the transcript at the new
		// width. Calling it twice would render the whole history twice.
		m.layout()
	case tea.KeyPressMsg:
		if m.palette.open {
			model, cmd := m.paletteKey(msg)
			return model, cmd
		}
		if m.picker.open {
			model, cmd := m.pickerKey(msg)
			return model, cmd
		}
		switch msg.String() {
		case "esc":
			if m.busy {
				if m.cancelTurn != nil {
					m.cancelTurn()
					m.cancelTurn = nil
				}
				m.busy = false
				m.statusText = "ход прерван"
				m.history = append(m.history, line{kindSys, "⏹ ход прерван пользователем (Esc)"})
				m.historyDirty = true
				m.followVP()
				return m, nil
			}
		case "ctrl+c":
			if m.busy {
				if m.cancelTurn != nil {
					m.cancelTurn()
					m.cancelTurn = nil
				}
				m.busy = false
				m.statusText = "ход прерван"
				m.history = append(m.history, line{kindSys, "⏹ ход прерван пользователем (Ctrl+C)"})
				m.historyDirty = true
				m.followVP()
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+p":
			m.palette = paletteState{open: true}
			return m, nil
		case "ctrl+b":
			m.showSidebar = !m.showSidebar
			m.layout()
			m.followVP()
			return m, nil
		case "ctrl+y":
			m.copyLastResponse()
			return m, nil
		case "pgup":
			m.vp.ScrollUp(10)
			m.stick = false
			m.syncVP()
			return m, nil
		case "pgdown":
			m.vp.ScrollDown(10)
			m.stick = m.vp.AtBottom()
			m.syncVP()
			return m, nil
		case "home", "ctrl+home":
			m.vp.GotoTop()
			m.stick = false
			m.syncVP()
			return m, nil
		case "end", "ctrl+end":
			m.vp.GotoBottom()
			m.stick = true
			m.syncVP()
			return m, nil
		case "tab":
			if len(m.suggest) > 0 {
				m.input.SetValue(m.suggest[0])
				m.updateSuggest()
				return m, nil
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			m.suggest = nil
			if text == "" || m.busy {
				return m, nil
			}
			switch text {
			case "/quit", "/exit":
				return m, tea.Quit
			case "/help":
				m.history = append(m.history,
					line{kindSys, "ctrl+p — палитра команд · ctrl+b — панель · ctrl+y — копировать ответ"},
					line{kindSys, "esc — прервать текущий ход · pgup/pgdown — скролл"},
					line{kindSys, "мышь — выделение и копирование текста прямо в терминале"},
					line{kindSys, "/models, /model <id>, /copy, /sidebar, /new, /clear, /quit"})
				m.historyDirty = true
				m.followVP()
				return m, nil
			case "/copy":
				m.copyLastResponse()
				return m, nil
			case "/clear":
				m.history = nil
				m.historyDirty = true
				m.followVP()
				return m, nil
			case "/new":
				m.sessionID = newSessionID()
				m.turnCount = 0
				m.toolCallCount = 0
				m.history = append(m.history, line{kindSys, "— сессия сброшена —"})
				m.historyDirty = true
				m.followVP()
				return m, nil
			case "/sidebar":
				m.showSidebar = !m.showSidebar
				m.layout()
				m.followVP()
				return m, nil
			case "/models":
				if m.busy {
					return m, nil
				}
				return m, m.fetchModelsCmd()
			}
			if id, ok := strings.CutPrefix(text, "/model "); ok {
				id = strings.TrimSpace(id)
				if id == "" || m.busy {
					return m, nil
				}
				return m, m.switchModelCmd(id)
			}
			m.history = append(m.history, line{kindUser, text})
			m.historyDirty = true
			m.busy = true
			m.followVP()
			return m, m.startTurn(text)
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case deltaMsg:
		m.appendAgentText(msg.text)
	case toolCallMsg:
		m.lastTool = msg.name
		m.toolCallCount++
		m.statusText = "вызов: " + msg.name
		m.history = append(m.history, line{kindTool, msg.name + "(" + msg.args + ")"})
		m.historyDirty = true
	case toolResMsg:
		m.statusText = "ответ: " + msg.name
		// Stored raw: renderHistory owns the "⎿" gutter and the wrapping, so
		// pre-wrapping here would both double the prefix and measure against a
		// width that ignores it.
		m.history = append(m.history, line{kindToolRes, msg.output})
		m.historyDirty = true
	case modelsListMsg:
		if msg.err != nil {
			m.history = append(m.history, line{kindErr, "models: " + msg.err.Error()})
			m.historyDirty = true
			m.followVP()
		} else {
			m.picker = modelPicker{open: true, models: msg.models}
		}
	case modelSwitchedMsg:
		if msg.err != nil {
			m.history = append(m.history, line{kindErr, "model: " + msg.err.Error()})
		} else {
			m.prov.model = msg.name
			m.runner = msg.runner
			// Preserve m.sessionID so conversation context is retained!
			m.history = append(m.history, line{kindSys, "модель переключена: " + msg.name + " (контекст сохранён)"})
		}
		m.historyDirty = true
	case turnDoneMsg:
		m.busy = false
		m.cancelTurn = nil
		if msg.err != nil {
			if msg.err == context.Canceled {
				m.statusText = "прервано"
				m.history = append(m.history, line{kindSys, "⏹ ход прерван"})
			} else {
				m.statusText = "ошибка"
				m.history = append(m.history, line{kindErr, "error: " + msg.err.Error()})
			}
		} else {
			m.statusText = "готов"
		}
		m.history = append(m.history, line{kindSys, ""})
		m.historyDirty = true
	case errMsg:
		m.busy = false
		m.statusText = "ошибка"
		m.history = append(m.history, line{kindErr, "error: " + msg.Error()})
		m.historyDirty = true
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateSuggest()
	m.followVP()
	return m, cmd
}

// sidebarVisible reports whether the sidebar is actually on screen. Having it
// switched on is not enough: below minSidebarTerm there is no room for it next
// to a usable chat panel, so it is dropped rather than squeezing the transcript
// down to a column of text.
func (m *uiModel) sidebarVisible() bool {
	return m.showSidebar && m.width >= minSidebarTerm && m.width-sidebarBoxWidth-sidebarGap >= minChatWidth
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

// suggestHeight is the row the tab-completion hint occupies, or 0 when hidden.
// On a terminal too short to hold the whole frame the hint is dropped: the
// input box matters more than the completion, and a frame one row too tall
// scrolls the input off the bottom of the screen.
func (m *uiModel) suggestHeight() int {
	if len(m.suggest) == 0 {
		return 0
	}
	if m.height < chromeHeight+2 {
		return 0
	}
	return 1
}

// layout sizes the viewport so that header + chat panel + status bar +
// suggestion row + input box add up to exactly the terminal height. The
// viewport receives the panel's *inner* row count; View adds the borders back.
func (m *uiModel) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	rows := max(m.height-chromeHeight-m.suggestHeight(), minViewRows)
	m.vp.SetWidth(m.contentWidth())
	m.vp.SetHeight(rows)
	// The input scrolls horizontally within one row; without an explicit width
	// a long prompt grows the box and pushes the frame off the bottom.
	m.input.SetWidth(max(m.contentWidth()-ansi.StringWidth(m.input.Prompt), 8))
	m.historyDirty = true
}

func (m *uiModel) appendAgentText(delta string) {
	if delta == "" {
		return
	}
	if len(m.history) > 0 && m.history[len(m.history)-1].kind == kindAgent {
		m.history[len(m.history)-1].text += delta
	} else {
		m.history = append(m.history, line{kindAgent, delta})
	}
	m.historyDirty = true
}

func (m *uiModel) syncVP() {
	m.vp.SetContent(m.renderHistory())
}

func (m *uiModel) followVP() {
	m.syncVP()
	if m.stick {
		m.vp.GotoBottom()
	}
}

func (m *uiModel) updateSuggest() {
	m.suggest = nil
	if m.palette.open || m.picker.open {
		return
	}
	text := m.input.Value()
	if m.busy || !strings.HasPrefix(text, "/") {
		return
	}
	if !strings.Contains(text, " ") {
		prefix := strings.TrimPrefix(text, "/")
		for _, c := range m.commands() {
			if strings.HasPrefix(c.name, prefix) {
				m.suggest = append(m.suggest, "/"+c.name)
			}
		}
		return
	}
	if id, ok := strings.CutPrefix(text, "/model "); ok {
		q := strings.TrimSpace(id)
		for _, id := range m.picker.models {
			if q == "" || strings.Contains(id, q) {
				m.suggest = append(m.suggest, "/model "+id)
			}
			if len(m.suggest) >= 8 {
				break
			}
		}
	}
}

// transcriptRow describes how one history entry is laid out in the chat panel:
// a gutter in front of the first row and a matching hanging indent on the rows
// that follow, so wrapped text stays aligned under its own marker instead of
// jumping back to column zero.
type transcriptRow struct {
	style lipgloss.Style
	first string
	rest  string
	// verbatim marks pre-formatted art (the logo), which keeps its own line
	// breaks: re-wrapping box-drawing runes would scramble the picture.
	verbatim bool
}

func (m *uiModel) rowStyle(kind lineKind) transcriptRow {
	switch kind {
	case kindUser:
		return transcriptRow{style: styleUser, first: "› ", rest: "  "}
	case kindTool:
		return transcriptRow{style: styleTool, first: "⏺ ", rest: "  "}
	case kindToolRes:
		return transcriptRow{style: styleToolRes, first: "  ⎿ ", rest: "    "}
	case kindAgent:
		return transcriptRow{style: styleAgent}
	case kindSys:
		return transcriptRow{style: styleSys}
	case kindErr:
		return transcriptRow{style: styleErr}
	case kindLogo:
		return transcriptRow{style: styleLogo, verbatim: true}
	}
	return transcriptRow{style: styleAgent}
}

// rowRows turns one history entry into the transcript rows it occupies. Prose is
// wrapped to width under a hanging indent. Pre-formatted art keeps its own line
// breaks and is dropped whole when it cannot fit: a banner sliced to the panel
// edge, or broken across rows, reads as corruption rather than as a logo. The
// header names the app regardless, so losing the art costs nothing.
func rowRows(text string, width int, row transcriptRow) []string {
	if !row.verbatim {
		return wrapIndent(text, width, row.first, row.rest)
	}
	rows := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for _, r := range rows {
		if ansi.StringWidth(r) > width {
			return nil
		}
	}
	return rows
}

func (m *uiModel) renderHistory() string {
	w := m.contentWidth()
	if !m.historyDirty && m.cachedWidth == w && m.cachedHistory != "" {
		return m.cachedHistory
	}
	var b strings.Builder
	for _, l := range m.history {
		row := m.rowStyle(l.kind)
		for _, r := range rowRows(l.text, w, row) {
			b.WriteString(row.style.Render(r) + "\n")
		}
	}
	m.cachedHistory = b.String()
	m.cachedWidth = w
	m.historyDirty = false
	return m.cachedHistory
}

// wrapIndent breaks text into rows no wider than width cells, prefixing the
// very first row with first and every later row with rest.
func wrapIndent(text string, width int, first, rest string) []string {
	if width < 1 {
		width = 1
	}
	// Rows are wrapped against the wider of the two indents, so neither the
	// gutter row nor a continuation row can spill past the frame.
	avail := width - max(ansi.StringWidth(first), ansi.StringWidth(rest))
	if avail < 1 {
		avail = 1
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		for _, row := range wrapCells(para, avail) {
			indent := rest
			if len(out) == 0 {
				indent = first
			}
			out = append(out, indent+row)
		}
	}
	return out
}

// wrapCells breaks one logical line into rows of at most width terminal cells.
// It prefers word boundaries and only splits inside a word when that word
// cannot fit on a row of its own. Widths are counted in cells rather than bytes
// so that Cyrillic, CJK and emoji all measure the way the terminal draws them.
func wrapCells(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	s = strings.TrimRight(s, " \t")
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	var rows []string
	cur, curW := "", 0
	flush := func() {
		rows = append(rows, cur)
		cur, curW = "", 0
	}
	for _, word := range strings.Split(s, " ") {
		wordW := ansi.StringWidth(word)
		if wordW == 0 {
			// A run of spaces is kept, leading ones included: they are often the
			// indent of a pre-formatted line, and the caller adds its own
			// gutter on top of the text rather than inside it.
			if curW < width {
				cur += " "
				curW++
			}
			continue
		}
		switch {
		case curW == 0 && wordW <= width:
			cur, curW = word, wordW
		case curW > 0 && curW+1+wordW <= width:
			cur += " " + word
			curW += 1 + wordW
		default:
			if curW > 0 {
				flush()
			}
			for wordW > width {
				head, rest := cutCells(word, width)
				rows = append(rows, head)
				word, wordW = rest, ansi.StringWidth(rest)
			}
			cur, curW = word, wordW
		}
	}
	flush()
	return rows
}

// cutCells splits s at the last grapheme boundary that fits in width cells, so
// a rune is never halved.
func cutCells(s string, width int) (head, rest string) {
	if width < 1 {
		width = 1
	}
	if ansi.StringWidth(s) <= width {
		return s, ""
	}
	return ansi.Truncate(s, width, ""), ansi.TruncateLeft(s, width, "")
}

func (m *uiModel) paletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredCommands()
	switch msg.String() {
	case "esc", "ctrl+p":
		m.palette.open = false
		return m, nil
	case "up":
		if m.palette.selected > 0 {
			m.palette.selected--
		}
		return m, nil
	case "down":
		if m.palette.selected < len(filtered)-1 {
			m.palette.selected++
		}
		return m, nil
	case "enter":
		m.palette.open = false
		if m.palette.selected < len(filtered) {
			c := filtered[m.palette.selected]
			m.history = append(m.history, line{kindSys, "· " + c.name})
			m.historyDirty = true
			cmd := c.run(m)
			m.followVP()
			return m, cmd
		}
		return m, nil
	case "backspace":
		if q := []rune(m.palette.query); len(q) > 0 {
			m.palette.query = string(q[:len(q)-1])
			m.palette.selected = 0
		}
		return m, nil
	}
	if len(msg.Text) > 0 {
		m.palette.query += msg.Text
		m.palette.selected = 0
	}
	return m, nil
}

func (m *uiModel) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredModels()
	switch msg.String() {
	case "esc", "ctrl+p":
		m.picker.open = false
		return m, nil
	case "up":
		if m.picker.selected > 0 {
			m.picker.selected--
		}
		return m, nil
	case "down":
		if m.picker.selected < len(filtered)-1 {
			m.picker.selected++
		}
		return m, nil
	case "enter":
		m.picker.open = false
		if m.picker.selected < len(filtered) {
			id := filtered[m.picker.selected]
			m.history = append(m.history, line{kindSys, "· модель → " + id})
			m.historyDirty = true
			cmd := m.switchModelCmd(id)
			m.followVP()
			return m, cmd
		}
		return m, nil
	case "backspace":
		if q := []rune(m.picker.query); len(q) > 0 {
			m.picker.query = string(q[:len(q)-1])
			m.picker.selected = 0
		}
		return m, nil
	}
	if len(msg.Text) > 0 {
		m.picker.query += msg.Text
		m.picker.selected = 0
	}
	return m, nil
}

func (m *uiModel) filteredModels() []string {
	q := strings.ToLower(m.picker.query)
	var out []string
	for _, id := range m.picker.models {
		if q == "" || strings.Contains(strings.ToLower(id), q) {
			out = append(out, id)
		}
	}
	return out
}

func (m *uiModel) fetchModelsCmd() tea.Cmd {
	p := m.prov
	return func() tea.Msg {
		models, err := listModels(p)
		return modelsListMsg{models: models, err: err}
	}
}

func (m *uiModel) switchModelCmd(id string) tea.Cmd {
	p := m.prov
	p.model = id
	svc, tools := m.svc, m.tools
	return func() tea.Msg {
		ctx := context.Background()
		a, err := buildAgent(ctx, p, tools)
		if err != nil {
			return modelSwitchedMsg{name: id, err: err}
		}
		r, err := runner.New(runner.Config{
			AppName:           "dmcode",
			Agent:             a,
			SessionService:    svc,
			AutoCreateSession: true,
		})
		if err != nil {
			return modelSwitchedMsg{name: id, err: err}
		}
		return modelSwitchedMsg{name: id, runner: r}
	}
}

func (m *uiModel) startTurn(text string) tea.Cmd {
	p := m.prog
	r, userID, sessionID := m.runner, "user", m.sessionID
	turnCtx, cancel := context.WithCancel(context.Background())
	m.cancelTurn = cancel
	m.turnCount++
	m.statusText = "генерация ответа..."
	return func() tea.Msg {
		userMsg := genai.NewContentFromText(text, genai.RoleUser)
		prevText := ""
		for ev, err := range r.Run(turnCtx, userID, sessionID, userMsg, agent.RunConfig{
			StreamingMode: agent.StreamingModeSSE,
		}) {
			if err != nil {
				return turnDoneMsg{err}
			}
			if ev.LLMResponse.Content == nil {
				continue
			}
			for _, part := range ev.LLMResponse.Content.Parts {
				switch {
				case part.Text != "":
					if ev.LLMResponse.Partial {
						p.Send(deltaMsg{part.Text})
						prevText += part.Text
					} else if part.Text != prevText {
						p.Send(deltaMsg{part.Text})
						prevText = ""
					}
				case part.FunctionCall != nil:
					args, _ := json.Marshal(part.FunctionCall.Args)
					p.Send(toolCallMsg{name: part.FunctionCall.Name, args: truncate(string(args), 160)})
				case part.FunctionResponse != nil:
					p.Send(toolResMsg{name: part.FunctionResponse.Name, output: renderToolResponse(part.FunctionResponse)})
				}
			}
		}
		return turnDoneMsg{nil}
	}
}

func renderToolResponse(fr *genai.FunctionResponse) string {
	b, err := json.Marshal(fr.Response)
	if err != nil {
		return fmt.Sprint(fr.Response)
	}
	return truncate(string(b), 600)
}

// truncate cuts s to at most n terminal cells. Counting cells instead of bytes
// matters because the strings routed through here hold Cyrillic (2 bytes per
// rune) and box-drawing runes: a byte cut lands mid-rune and the terminal
// prints replacement characters.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}

// sidebarView renders the info panel. height is the *outer* height it has to
// occupy, borders included, and it must be the same value the chat panel is
// given — the two are framed side by side and any difference in their heights
// leaves one bottom border floating above the other.
//
// Three lipgloss details drive the shape. Height only pads a block up, it never
// crops; the word wrap happens at Width minus the borders and padding, so a row
// count taken before framing is not the row count that reaches the screen; and
// the left margin is applied after everything else, adding a column but no
// row. So the body is wrapped and measured on its own, trimmed to the budget,
// and only then framed.
func (m *uiModel) sidebarView(height int) string {
	const (
		sbPad   = 1 // horizontal padding
		sbEdge  = 1 // one vertical border per side
		sbInner = sidebarBoxWidth - 2*sbPad - 2*sbEdge
	)

	var b strings.Builder
	row := func(style lipgloss.Style, text string) {
		b.WriteString(style.Render(text) + "\n")
	}
	// value writes an indented entry, wrapping it under its own marker so a
	// long model or tool name stays readable instead of being cut mid-word. The
	// text is cut to the room the marker leaves, so the ellipsis lands on the
	// last row instead of being stranded on a continuation of its own.
	value := func(style lipgloss.Style, marker, text string) {
		indent := strings.Repeat(" ", ansi.StringWidth(marker))
		for _, r := range wrapIndent(truncate(text, sbInner-ansi.StringWidth(marker)), sbInner, marker, indent) {
			b.WriteString(style.Render(r) + "\n")
		}
	}

	row(styleSidebarLabel, "МОДЕЛЬ")
	value(styleSidebarValue, " ", m.prov.model)
	if m.prov.label != "" {
		row(styleHint, truncate("via "+m.prov.label, sbInner))
	}
	b.WriteString("\n")

	row(styleSidebarLabel, "СЕССИЯ")
	row(styleHint, " "+truncate(m.sessionID, sbInner-1))
	row(styleHint, fmt.Sprintf(" ходов: %d", m.turnCount))
	row(styleHint, fmt.Sprintf(" тулов: %d", m.toolCallCount))
	b.WriteString("\n")

	row(styleSidebarLabel, "ПАПКА")
	value(styleSidebarValue, " ", m.workDir)
	b.WriteString("\n")

	if m.lastTool != "" {
		row(styleSidebarLabel, "ПОСЛЕДНИЙ ТУЛ")
		value(styleTool, " ⏺ ", m.lastTool)
		b.WriteString("\n")
	}

	row(styleSidebarLabel, "ИНСТРУМЕНТЫ")
	for _, t := range m.toolNames {
		row(styleHint, " · "+t)
	}
	b.WriteString("\n")

	row(styleSidebarLabel, "ГОРЯЧИЕ КЛАВИШИ")
	row(styleHint, " ctrl+p  команды")
	row(styleHint, " ctrl+b  скрыть панель")
	row(styleHint, " ctrl+y  копировать ответ")
	row(styleHint, " esc     отмена хода")
	row(styleHint, " pgup/dn скролл")

	// Measuring at sbInner wraps and pads every row to exactly the width the
	// framed box will have available, so the split below counts real rows.
	body := lipgloss.NewStyle().Width(sbInner).Render(strings.TrimRight(b.String(), "\n"))
	if height <= 0 {
		return styleSidebar.Width(sidebarBoxWidth).Render(body)
	}

	lines := strings.Split(body, "\n")
	if budget := height - 2*sbEdge; len(lines) > budget {
		if budget < 1 {
			budget = 1
		}
		lines = lines[:budget]
		// Mark the cut so a trimmed panel is not read as a complete one.
		lines[budget-1] = styleHint.Render("…")
	}

	return styleSidebar.Width(sidebarBoxWidth).Height(height).Render(strings.Join(lines, "\n"))
}

// statusBarView renders the single-row footer. Everything in it competes for
// one line, so the model name and the hotkey list are fitted against the space
// that is actually left and the rest is dropped. Overflowing is not cosmetic:
// lipgloss word-wraps the surplus onto a second row, which makes the whole
// frame one row taller than the terminal and scrolls the input off the bottom.
func (m *uiModel) statusBarView() string {
	width := max(m.width, 16)
	inner := width - 2 // the bar's own left/right padding
	const sep = "  │  "

	var badge string
	switch {
	case m.busy:
		badge = styleBadgeBusy.Render("⏳ РАБОТАЕТ")
	case m.statusText == "ход прерван", m.statusText == "прервано":
		badge = styleBadgeStop.Render("⏹ ПРЕРВАНО")
	default:
		badge = styleBadgeReady.Render("● ГОТОВ")
	}

	statusDesc := m.statusText
	switch {
	case statusDesc == "" && m.busy:
		statusDesc = m.spin.View() + " выполнение..."
	case statusDesc == "":
		statusDesc = "ожидание задачи"
	case m.busy:
		statusDesc = m.spin.View() + " " + statusDesc
	}

	left := badge + "  " + statusDesc
	// Two columns are always held back as the gutter between the two halves, so
	// the gap can never collapse to zero and push the row over the wrap point.
	avail := inner - ansi.StringWidth(left) - 2
	if avail < 8 {
		return styleStatusBar.Width(width).Render(truncate(left, inner))
	}

	// Right half, assembled most- to least-important so the hotkeys are what
	// disappears on a narrow terminal.
	var right string
	fit := func(s string) bool {
		cand := s
		if right != "" {
			cand = right + sep + s
		}
		if ansi.StringWidth(cand) > avail {
			return false
		}
		right = cand
		return true
	}
	if fit(truncate(m.prov.model, avail)) {
		for _, k := range statusHotkeys {
			if !fit(k) {
				break
			}
		}
	}

	content := left
	if right != "" {
		gap := inner - ansi.StringWidth(left) - ansi.StringWidth(right)
		content = left + strings.Repeat(" ", max(gap, 2)) + right
	}
	return styleStatusBar.Width(width).Render(content)
}

// headerView renders the app name, the model and the session id on one row.
// As with the status bar, the variable-length parts are truncated rather than
// allowed to wrap the row onto a second one.
func (m *uiModel) headerView() string {
	width := max(m.width, 16)
	inner := width - 2 // the bar's own left/right padding
	const (
		sep  = "  ·  "
		name = "dmcode"
	)

	avail := inner - ansi.StringWidth(name) - 2*ansi.StringWidth(sep)
	if avail < 12 {
		return styleTopBar.Width(width).Render(styleHeader.Render(name))
	}

	// The session id is the less useful of the two on a narrow screen, so the
	// model keeps the columns and the id is reduced to a readable stub.
	model := truncate(m.prov.model, max(avail-ansi.StringWidth(sep)-8, 4))
	id := truncate(m.sessionID, max(avail-ansi.StringWidth(sep)-ansi.StringWidth(model), 4))

	return styleTopBar.Width(width).Render(
		styleHeader.Render(name) + styleHint.Render(sep+model+sep+id))
}

// suggestView renders the tab-completion row. It is a single row, so the tail
// of the list is cut instead of wrapped.
func (m *uiModel) suggestView() string {
	if len(m.suggest) == 0 {
		return ""
	}
	const (
		indent  = 2
		tail    = "  (tab)"
		maxShow = 6
	)

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", indent))
	used := indent
	for i, s := range m.suggest {
		if i >= maxShow {
			break
		}
		seg := s
		if i > 0 {
			seg = "  " + s
		}
		if used+ansi.StringWidth(seg)+ansi.StringWidth(tail) > m.width {
			break
		}
		if i == 0 {
			b.WriteString(styleSuggest.Render(s))
		} else {
			b.WriteString(styleHint.Render(seg))
		}
		used += ansi.StringWidth(seg)
	}
	b.WriteString(styleHint.Render(tail))
	return b.String()
}

// floatingWidth is the outer width of the palette and the model picker. Both
// list model identifiers, which are unbounded, so their rows are wrapped inside
// a frame that fits the terminal instead of growing past it.
func (m *uiModel) floatingWidth() int {
	return max(min(m.width, 68), 24)
}

// floatingPanel frames a palette-style box: a two-row header, a list body and
// the border. The body is trimmed by rendered row rather than by entry, because
// one long model id can take two rows and a count taken before wrapping is not
// the count that reaches the screen. Trimming stops at the selected entry: a
// panel scrolled away from its cursor is worse than one listing fewer models.
func (m *uiModel) floatingPanel(title, query string, entries [][]string, sel int) string {
	inner := m.floatingWidth() - panelBorder
	styleSel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))

	head := []string{
		styleHeader.Render(" "+title+" ") + styleHint.Render("esc — закрыть"),
		styleHint.Render(truncate(" › "+query, inner)),
		"",
	}

	// One row is held back for the "still hidden" marker, so revealing it can
	// never push the bottom border off the screen.
	room := max(m.height-panelBorder-len(head)-1, 1)
	total := len(entries)
	for len(entries) > 0 && len(entries)-1 != sel && rowCount(entries) > room {
		entries = entries[:len(entries)-1]
	}

	rows := make([]string, 0, len(head)+room+1)
	rows = append(rows, head...)
	for i, group := range entries {
		for _, r := range group {
			if i == sel {
				r = styleSel.Render(r)
			}
			rows = append(rows, r)
		}
	}
	if len(entries) == 0 {
		rows = append(rows, styleHint.Render("   ничего не найдено"))
	}
	if hidden := total - len(entries); hidden > 0 {
		rows = append(rows, styleHint.Render("   ↓ ещё "+fmt.Sprint(hidden)))
	}

	return stylePanel.Width(m.floatingWidth()).Render(strings.Join(rows, "\n"))
}

// rowCount counts the rendered rows a list of entry groups occupies.
func rowCount(entries [][]string) int {
	n := 0
	for _, e := range entries {
		n += len(e)
	}
	return n
}

func (m *uiModel) paletteBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, c := range m.filteredCommands() {
		marker := "   "
		if i == m.palette.selected {
			marker = " ▸ "
		}
		entries = append(entries, wrapIndent(c.name+" — "+c.desc, inner, marker, "     "))
	}
	return m.floatingPanel("⌘ команды", m.palette.query, entries, m.palette.selected)
}

func (m *uiModel) modelPickerBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, id := range m.filteredModels() {
		// The marker is the first-line indent rather than part of the text, so
		// the rows stay inside the frame instead of being re-wrapped by it.
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.picker.selected:
			marker, style = " ▸ ", lipgloss.NewStyle()
		case id == m.prov.model:
			marker, style = " ● ", styleTool
		}
		rows := wrapIndent(id, inner, marker, "     ")
		if i != m.picker.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel("⌘ модели", m.picker.query, entries, m.picker.selected)
}

func (m *uiModel) filteredCommands() []command {
	pattern := strings.ToLower(m.palette.query)
	var out []command
	for _, c := range m.commands() {
		if pattern == "" || strings.Contains(strings.ToLower(c.name), pattern) || strings.Contains(strings.ToLower(c.desc), pattern) {
			out = append(out, c)
		}
	}
	return out
}

func (m *uiModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("dmcode загружается…")
	}
	if m.picker.open || m.palette.open {
		box := m.paletteBox()
		if m.picker.open {
			box = m.modelPickerBox()
		}
		v := tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box))
		v.AltScreen = true
		return v
	}

	// Every row of the frame is exactly m.width wide and together they are
	// exactly m.height rows: the chat panel and the sidebar are framed to the
	// same outer height, and the viewport gets the panel's inner rows.
	rows := m.vp.Height()
	panel := stylePanel.Width(m.chatBoxWidth()).Height(rows + panelBorder).Render(m.vp.View())

	middle := panel
	if m.sidebarVisible() {
		middle = lipgloss.JoinHorizontal(lipgloss.Top, panel, m.sidebarView(rows+panelBorder))
	}

	// JoinVertical pads every block out to the widest one, so a single
	// overflowing row is enough to shift the whole frame sideways.
	parts := []string{m.headerView(), middle, m.statusBarView()}
	if m.suggestHeight() == 1 {
		parts = append(parts, m.suggestView())
	}
	parts = append(parts, stylePanel.Width(m.width).Render(m.input.View()))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
	v.AltScreen = true
	// Note: We deliberately do NOT set v.MouseMode = tea.MouseModeCellMotion.
	// This enables native terminal mouse text selection and copying without blocking user selection!
	return v
}

func runTUI(ctx context.Context, p provider, tools []tool.Tool, toolNames []string) error {
	a, err := buildAgent(ctx, p, tools)
	if err != nil {
		return err
	}
	svc := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:           "dmcode",
		Agent:             a,
		SessionService:    svc,
		AutoCreateSession: true,
	})
	if err != nil {
		return err
	}
	m := initialModel(r, svc, p, tools, toolNames)
	prog := tea.NewProgram(m)
	m.prog = prog
	_, err = prog.Run()
	return err
}
