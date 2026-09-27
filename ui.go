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

	styleSidebar      = lipgloss.NewStyle().Border(boxBorder, true).BorderForeground(cBorder).Padding(0, 1)
	styleSidebarLabel = lipgloss.NewStyle().Bold(true).Foreground(cTool)
	styleSidebarValue = lipgloss.NewStyle().Foreground(cAgent)

	stylePanel  = lipgloss.NewStyle().Border(boxBorder, true).BorderForeground(cBorder)
	styleTopBar = lipgloss.NewStyle().Border(boxBorder, false, false, true, false).BorderForeground(cBorder).Padding(0, 1)
)

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
				line{kindSys, "ctrl+p — палитра · ctrl+b — панель · ctrl+y — копировать ответ"},
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
					line{kindSys, "ctrl+p — палитра · ctrl+b — панель · ctrl+y — копировать ответ"},
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
		m.history = append(m.history, line{kindTool, "⏺ " + msg.name + "(" + msg.args + ")"})
		m.historyDirty = true
	case toolResMsg:
		m.statusText = "ответ: " + msg.name
		out := wrapLines(msg.output, m.contentWidth()-2, 5)
		for _, l := range out {
			m.history = append(m.history, line{kindToolRes, "  ⎿ " + l})
		}
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

func (m *uiModel) chatBoxWidth() int {
	if m.showSidebar && m.width >= 90 {
		w := m.width - 28
		if w > 20 {
			return w
		}
	}
	if m.width > 2 {
		return m.width
	}
	return 98
}

func (m *uiModel) contentWidth() int {
	w := m.chatBoxWidth() - 4
	if w < 10 {
		return 10
	}
	return w
}

func (m *uiModel) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	suggestHeight := 0
	if len(m.suggest) > 0 {
		suggestHeight = 1
	}
	// Header: 2 lines (1 line text + 1 line bottom border)
	// Status bar: 1 line
	// Input box: 3 lines (1 line text + 2 lines border)
	// Viewport panel borders: 2 lines (top + bottom)
	// Suggest line: suggestHeight
	// Total chrome: 2 + 1 + 3 + 2 = 8 lines
	panelHeight := m.height - 8 - suggestHeight
	if panelHeight < 4 {
		panelHeight = 4
	}
	m.vp.SetWidth(m.contentWidth())
	m.vp.SetHeight(panelHeight)
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

func (m *uiModel) renderHistory() string {
	w := m.contentWidth()
	if !m.historyDirty && m.cachedWidth == w && m.cachedHistory != "" {
		return m.cachedHistory
	}
	var b strings.Builder
	for _, l := range m.history {
		var style lipgloss.Style
		wrap := true
		switch l.kind {
		case kindUser:
			style, l.text = styleUser, "› "+l.text
		case kindAgent:
			style = styleAgent
		case kindTool:
			style = styleTool
		case kindToolRes:
			style = styleToolRes
			wrap = false
		case kindSys:
			style = styleSys
		case kindErr:
			style = styleErr
			wrap = false
		case kindLogo:
			style = styleLogo
			wrap = false
		}
		if wrap {
			for _, seg := range wordWrap(l.text, w) {
				b.WriteString(style.Render(seg) + "\n")
			}
		} else {
			b.WriteString(style.Render(l.text) + "\n")
		}
	}
	m.cachedHistory = b.String()
	m.cachedWidth = w
	m.historyDirty = false
	return m.cachedHistory
}

func wordWrap(s string, width int) []string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		var cur strings.Builder
		for _, word := range strings.Split(para, " ") {
			for len(word) > width {
				if cur.Len() > 0 {
					out = append(out, cur.String())
					cur.Reset()
				}
				out = append(out, word[:width])
				word = word[width:]
			}
			if cur.Len()+len(word)+1 > width {
				out = append(out, cur.String())
				cur.Reset()
			}
			if cur.Len() > 0 {
				cur.WriteString(" ")
			}
			cur.WriteString(word)
		}
		out = append(out, cur.String())
	}
	return out
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

func (m *uiModel) modelPickerBox() string {
	styleSel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	var b strings.Builder
	b.WriteString(styleHeader.Render(" ⌘ модели ") + styleHint.Render("  ↑↓ выбор · enter — переключить · esc — закрыть\n"))
	b.WriteString(styleHint.Render(" › "+m.picker.query) + "\n\n")
	filtered := m.filteredModels()
	if len(filtered) == 0 {
		b.WriteString(styleHint.Render("   ничего не найдено\n"))
	}
	for i, id := range filtered {
		marker := "   "
		style := lipgloss.NewStyle()
		if id == m.prov.model {
			marker = " ● "
			style = styleTool
		}
		if i == m.picker.selected {
			b.WriteString(styleSel.Render(" ▸ "+id) + "\n")
			continue
		}
		b.WriteString(style.Render(marker+id) + "\n")
	}
	return stylePanel.Render(strings.TrimRight(b.String(), "\n"))
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func wrapLines(s string, width, maxLines int) []string {
	s = strings.TrimRight(s, "\n")
	var out []string
	for _, l := range strings.Split(s, "\n") {
		for len(l) > width {
			out = append(out, l[:width])
			l = l[width:]
		}
		out = append(out, l)
	}
	if len(out) > maxLines {
		out = append(out[:maxLines], "… (обрезано)")
	}
	return out
}

func (m *uiModel) paletteBox() string {
	styleSel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	var b strings.Builder
	b.WriteString(styleHeader.Render(" ⌘ команды ") + styleHint.Render("  esc — закрыть\n"))
	b.WriteString(styleHint.Render(" › "+m.palette.query) + "\n\n")
	filtered := m.filteredCommands()
	if len(filtered) == 0 {
		b.WriteString(styleHint.Render("   ничего не найдено\n"))
	}
	for i, c := range filtered {
		if i == m.palette.selected {
			b.WriteString(styleSel.Render(" ▸ "+c.name) + styleHint.Render(" — "+c.desc) + "\n")
		} else {
			b.WriteString("   " + c.name + styleHint.Render(" — "+c.desc) + "\n")
		}
	}
	return stylePanel.Render(strings.TrimRight(b.String(), "\n"))
}

func (m *uiModel) sidebarView(height int) string {
	var b strings.Builder

	b.WriteString(styleSidebarLabel.Render("МОДЕЛЬ") + "\n")
	b.WriteString(styleSidebarValue.Render(" "+truncate(m.prov.model, 20)) + "\n\n")

	b.WriteString(styleSidebarLabel.Render("СЕССИЯ") + "\n")
	b.WriteString(styleHint.Render(" "+truncate(m.sessionID, 20)) + "\n")
	b.WriteString(styleHint.Render(fmt.Sprintf(" ходов: %d · тулов: %d", m.turnCount, m.toolCallCount)) + "\n\n")

	b.WriteString(styleSidebarLabel.Render("ПАПКА") + "\n")
	b.WriteString(styleSidebarValue.Render(" "+truncate(m.workDir, 20)) + "\n\n")

	if m.lastTool != "" {
		b.WriteString(styleSidebarLabel.Render("ПОСЛЕДНИЙ ТУЛ") + "\n")
		b.WriteString(styleTool.Render(" ⏺ "+m.lastTool) + "\n\n")
	}

	b.WriteString(styleSidebarLabel.Render("ИНСТРУМЕНТЫ") + "\n")
	for _, t := range m.toolNames {
		b.WriteString(styleHint.Render(" · "+t) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(styleSidebarLabel.Render("ГОРЯЧИЕ КЛАВИШИ") + "\n")
	b.WriteString(styleHint.Render(" ctrl+p  команды") + "\n")
	b.WriteString(styleHint.Render(" ctrl+b  скрыть панель") + "\n")
	b.WriteString(styleHint.Render(" ctrl+y  копировать ответ") + "\n")
	b.WriteString(styleHint.Render(" esc     отмена хода") + "\n")
	b.WriteString(styleHint.Render(" pgup/dn скролл") + "\n")

	return styleSidebar.Width(24).Height(height).Render(b.String())
}

func (m *uiModel) statusBarView() string {
	var badge string
	if m.busy {
		badge = styleBadgeBusy.Render("⏳ РАБОТАЕТ")
	} else if m.statusText == "ход прерван" || m.statusText == "прервано" {
		badge = styleBadgeStop.Render("⏹ ПРЕРВАНО")
	} else {
		badge = styleBadgeReady.Render("● ГОТОВ")
	}

	statusDesc := m.statusText
	if statusDesc == "" {
		if m.busy {
			statusDesc = m.spin.View() + " выполнение..."
		} else {
			statusDesc = "ожидание задачи"
		}
	} else if m.busy {
		statusDesc = m.spin.View() + " " + statusDesc
	}

	modelInfo := styleHint.Render(m.prov.model)
	hotkeys := styleHint.Render("ctrl+p палитра · ctrl+b панель · ctrl+y копия · esc отмена")

	left := badge + "  " + statusDesc
	right := modelInfo + "  │  " + hotkeys

	totalWidth := m.width
	if totalWidth <= 0 {
		totalWidth = 90
	}
	gap := totalWidth - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 2 {
		gap = 2
	}
	content := left + strings.Repeat(" ", gap) + right
	return styleStatusBar.Width(totalWidth).Render(content)
}

func (m *uiModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("dmcode загружается…")
	}
	if m.picker.open {
		v := tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.modelPickerBox()))
		v.AltScreen = true
		return v
	}
	if m.palette.open {
		v := tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.paletteBox()))
		v.AltScreen = true
		return v
	}

	header := styleTopBar.Width(m.width).Render(
		styleHeader.Render("dmcode") + styleHint.Render("  ·  "+m.prov.model+"  ·  "+m.sessionID))

	suggestLine := ""
	if len(m.suggest) > 0 {
		first := styleSuggest.Render(m.suggest[0])
		rest := styleHint.Render("  " + strings.Join(m.suggest[1:min(len(m.suggest), 6)], "  "))
		suggestLine = "  " + first + rest + styleHint.Render("  (tab)") + "\n"
	}

	// Main middle section: chat viewport + optional sidebar
	chatW := m.chatBoxWidth()
	viewportBox := stylePanel.Width(chatW - 2).Height(m.vp.Height()).Render(m.vp.View())

	var middle string
	if m.showSidebar && m.width >= 90 {
		sb := m.sidebarView(m.vp.Height())
		middle = lipgloss.JoinHorizontal(lipgloss.Top, viewportBox, sb)
	} else {
		middle = viewportBox
	}

	statusBar := m.statusBarView()
	inputBox := stylePanel.Width(m.width - 2).Render(m.input.View())

	var body string
	if suggestLine != "" {
		body = lipgloss.JoinVertical(lipgloss.Left,
			header,
			middle,
			statusBar,
			suggestLine,
			inputBox,
		)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left,
			header,
			middle,
			statusBar,
			inputBox,
		)
	}

	v := tea.NewView(body)
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
