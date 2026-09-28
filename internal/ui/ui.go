package ui

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
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/llm"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"

	adkagent "google.golang.org/adk/v2/agent"
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

// kindName labels a line kind for the /debug output. A reply showing up under
// the wrong name here is what identifies it as never having reached the
// markdown renderer.
func kindName(k lineKind) string {
	switch k {
	case kindUser:
		return "user"
	case kindAgent:
		return "agent"
	case kindTool:
		return "tool"
	case kindToolRes:
		return "toolres"
	case kindSys:
		return "sys"
	case kindErr:
		return "err"
	case kindLogo:
		return "logo"
	}
	return "?"
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

// deltaMsg carries one text part of a round to the event loop. It is a spoken
// rather than a wrapper of one, so the field names read the same on both sides
// of the goroutine boundary.
type deltaMsg = spoken

// turnText folds one LLM round's event stream into what the transcript still
// has to be told about.
//
// The model speaks twice about the same words: once as a run of partial deltas
// while it writes, and once in the finalised response that closes the round and
// lets the runner dispatch tools. Appending the final text on top of what the
// partials already delivered doubles the reply — glued together without a line
// break, which is what turned "## Done" into "## Done## Done" and left a table
// with no delimiter row, so the markdown stopped being recognised on every turn
// that used a tool.
//
// The accumulator is therefore per round, not per turn, and the closing response
// replaces the round rather than extending it. Replacement rather than append is
// what also covers a provider whose finalised text is not a prefix-extension of
// its deltas at all — there is nothing to trim, and appending the whole thing
// would duplicate the reply again.
type turnText struct{ streamed string }

// spoken is what one text part asks the transcript to do.
type spoken struct {
	// text is what to write. It is a delta unless replace is set.
	text string
	// replace makes text the whole of the round so far rather than an addition
	// to it, which is how a closing response corrects its own deltas.
	replace bool
}

// nothing is what a text part that adds nothing to the transcript asks for.
var nothing = spoken{}

// add reports what to do with one text part. A partial is always new. A
// finalised response closes the round: it is a no-op when the deltas already
// carried the text, a delta of the missing suffix when the provider appended
// something at the end, and a replacement of the round when the two disagree.
func (t *turnText) add(text string, partial bool) spoken {
	if partial {
		if text == "" {
			return nothing
		}
		t.streamed += text
		return spoken{text: text}
	}
	// The round ends here, whatever the comparison below decides — including
	// when the response carried no text at all, which is what a round that was
	// nothing but a tool call looks like. Skipping the reset in that case would
	// leave the next round comparing itself against this one's prose.
	streamed := t.streamed
	t.streamed = ""
	if text == "" {
		return nothing
	}
	switch {
	case streamed == text:
		return nothing
	case streamed == "":
		// Nothing was streamed, so this is the only copy there will be.
		return spoken{text: text}
	case strings.HasPrefix(text, streamed):
		return spoken{text: text[len(streamed):]}
	default:
		return spoken{text: text, replace: true}
	}
}

// toolPart is a tool call or a tool result an event carried alongside its text.
// The text is dispatched before these, so a model that explains itself and calls
// a tool in the same breath is read in that order.
type toolPart struct {
	call *genai.FunctionCall
	resp *genai.FunctionResponse
}

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
	Models []string
	err    error
}

type modelSwitchedMsg struct {
	name   string
	runner *runner.Runner
	pool   []config.Provider
	err    error
}

type errMsg string

func (e errMsg) Error() string { return string(e) }

// setupCheckMsg reports whether the endpoint accepted the key before it was
// written to .env. Saving an unverified key is worse than not saving it at all:
// a wrong key for the chosen provider overwrites the working OPENAI_API_KEY, and
// the failure only surfaces on the next message, by which point the previous
// configuration is gone.
type setupCheckMsg struct {
	vars map[string]string
	opt  config.SetupOption
	err  error
	// forced is set when the user asked to save despite a failed check, for
	// endpoints that do not implement /models and cannot be probed.
	forced bool
}

// failoverMsg announces that a turn moved to another endpoint. It is a message
// rather than a direct history append because the switch happens on the turn
// goroutine, and only Update may touch the transcript.
type failoverMsg struct {
	from   config.Provider
	to     config.Provider
	reason string
}

// toolCheckMsg reports the result of the background tool-calling check. A free
// endpoint that lists models but cannot call tools makes every turn prose, so
// the user is told rather than left to discover it mid-task.
type toolCheckMsg struct {
	ok         bool
	conclusive bool
	Model      string
	Label      string
}

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
	prov      config.Provider
	// pool is every endpoint this session may use, in preference order. prov is
	// the member currently answering, so a failover updates both and the next
	// /model switch keeps the reserves instead of dropping back to a single
	// endpoint.
	pool      []config.Provider
	tools     []tool.Tool
	prog      *tea.Program
	toolNames []string
	palette   paletteState
	picker    modelPicker
	setup     setupState
	lang      langState
	suggest   []suggestion
	// suggestSel is the highlighted row of the command list. It is reset on every
	// keystroke, so a narrowed list always starts at the top.
	suggestSel int
	stick      bool

	// Prompt history: every prompt the user has sent, persisted in
	// ~/.dmcode/history.jsonl and recalled with ↑/↓ like a shell. histPos ==
	// len(promptHistory) means the input holds the live draft.
	promptHistory []string
	histPos       int
	draft         string

	// UX & state components
	showSidebar   bool
	cancelTurn    context.CancelFunc
	turnCount     int
	toolCallCount int
	lastTool      string

	// workDir is the absolute directory the session is scoped to, and
	// workDirShort its last path element for the one-line display. The full
	// path is kept because the tools act on absolute paths: showing only the
	// folder name left the user unable to tell which of two identically named
	// projects the agent was editing.
	workDir      string
	workDirShort string

	// readOnlyTools is the plan-mode instrument set, kept alongside the full
	// one so a Tab press can swap them without rebuilding anything.
	readOnlyTools []tool.Tool
	mouseEnabled  bool
	statusText    string

	// ctx outlives Init so a mode switch can rebuild the agent on the same
	// context the session started on, instead of handing the pool a fresh
	// background that nothing can cancel.
	ctx  context.Context
	mode agentMode

	// History render cache for streaming performance
	cachedHistory string
	cachedWidth   int
	historyDirty  bool
}

type modelPicker struct {
	open     bool
	query    string
	selected int
	Models   []string
}

type paletteState struct {
	open     bool
	query    string
	selected int
}

// langState drives the /lang overlay: a short list with no text field, since
// the choice set is fixed and small.
type langState struct {
	open     bool
	selected int
}

// setup stages, walked in order by /setup.
const (
	setupPick = iota
	setupKey
	setupURL
	setupModel
)

// setupState drives /setup inside the TUI. The wizard used to exist only as a
// stdin prompt, while the error message that points at it promised a command
// that was never there; the two now share one option list and one apply path.
type setupState struct {
	open     bool
	stage    int
	selected int
	opt      config.SetupOption
	buf      string
	// force saves the configuration even though the endpoint rejected the key.
	// It is only set after a failed check, so a bad key is still one retry away
	// from being saved on the first attempt.
	force bool
}

func (s *setupState) reset() {
	*s = setupState{}
}

type command struct {
	name string
	desc string
	run  func(m *uiModel) tea.Cmd
}

func (m *uiModel) commands() []command {
	return []command{
		{name: "setup", desc: i18n.T("choose a provider (free, no key needed)"), run: func(m *uiModel) tea.Cmd {
			return m.openSetup()
		}},
		{name: "models", desc: i18n.T("list models"), run: func(m *uiModel) tea.Cmd {
			return m.fetchModelsCmd()
		}},
		{name: "copy", desc: i18n.T("copy the agent's reply (ctrl+y)"), run: func(m *uiModel) tea.Cmd {
			m.copyLastResponse()
			return nil
		}},
		{name: "sidebar", desc: i18n.T("toggle the sidebar (ctrl+b)"), run: func(m *uiModel) tea.Cmd {
			m.showSidebar = !m.showSidebar
			m.layout()
			m.followVP()
			return nil
		}},
		{name: "mode", desc: i18n.T("switch plan/act mode (tab)"), run: func(m *uiModel) tea.Cmd {
			return m.toggleMode()
		}},
		{name: "cd", desc: i18n.T("change the working folder"), run: func(m *uiModel) tea.Cmd {
			return m.changeDir("")
		}},
		{name: "mouse", desc: i18n.T("toggle mouse wheel scrolling"), run: func(m *uiModel) tea.Cmd {
			m.mouseEnabled = !m.mouseEnabled
			return nil
		}},
		{name: "new", desc: i18n.T("start a new session"), run: func(m *uiModel) tea.Cmd {
			m.sessionID = newSessionID()
			m.turnCount = 0
			m.toolCallCount = 0
			m.history = append(m.history, line{kindSys, i18n.T("— session reset —")})
			m.historyDirty = true
			return nil
		}},
		{name: "clear", desc: i18n.T("clear the screen"), run: func(m *uiModel) tea.Cmd {
			m.history = nil
			m.historyDirty = true
			return nil
		}},
		{name: "history", desc: i18n.T("recent prompts (up/down to recall)"), run: func(m *uiModel) tea.Cmd {
			m.showRecentPrompts()
			return nil
		}},
		{name: "lang", desc: i18n.T("interface language"), run: func(m *uiModel) tea.Cmd {
			return m.openLangPicker()
		}},
		{name: "help", desc: i18n.T("show the hotkeys"), run: func(m *uiModel) tea.Cmd {
			m.history = append(m.history,
				line{kindSys, i18n.T("ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply")},
				line{kindSys, i18n.T("esc — stop the current turn · up/down — prompt history · pgup/pgdown — scroll")},
				line{kindSys, i18n.T("mouse — select and copy text right in the terminal")},
				line{kindSys, "/setup, /models, /model <id>, /history, /copy, /sidebar, /lang, /new, /clear, /quit"})
			m.historyDirty = true
			return nil
		}},
		{name: "tools", desc: i18n.T("list the available tools"), run: func(m *uiModel) tea.Cmd {
			for _, t := range m.toolNames {
				m.history = append(m.history, line{kindSys, "· " + t})
			}
			m.historyDirty = true
			return nil
		}},
		{name: "quit", desc: i18n.T("quit"), run: func(m *uiModel) tea.Cmd { return tea.Quit }},
	}
}

const logo = `  ███████╗ ███╗   ███╗ ██████╗ ██████╗ ███████╗ ██████╗
  ██╔═══██║████╗ ████║██╔════╝██╔═══██╗██╔═══██╗██╔═══╝
  ██║   ██║██╔████╔██║██║     ██║   ██║██║   ██║█████╗
  ██║   ██║██║╚██╔╝██║██║     ██║   ██║██║   ██║██╔══╝
  ███████╔╝██║ ╚═╝ ██║╚██████╗╚██████╔╝███████╔╝██████╗
  ╚═════╝  ╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝`

var styleLogo = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)

func newSessionID() string {
	return fmt.Sprintf("sess-%d", rand.Int63())
}

func InitialModel(r *runner.Runner, svc session.Service, p config.Provider, tools []tool.Tool, readOnly []tool.Tool, toolNames []string) *uiModel {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = i18n.T("describe the task… (/help for commands, esc to cancel)")
	ti.Focus()
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	vp := viewport.New(viewport.WithHeight(10))

	// tools.Root() is the boundary the tools enforce, so the sidebar reports
	// the same directory the agent is actually confined to rather than a second
	// independently-derived answer that could drift from it.
	wd := dmtools.Root()
	if wd == "" {
		wd, _ = os.Getwd()
	}

	m := &uiModel{
		input:         ti,
		spin:          sp,
		vp:            vp,
		stick:         true,
		sessionID:     newSessionID(),
		runner:        r,
		svc:           svc,
		prov:          p,
		pool:          []config.Provider{p},
		tools:         tools,
		readOnlyTools: readOnly,
		toolNames:     toolNames,
		showSidebar:   true,
		mouseEnabled:  true,
		workDir:       wd,
		workDirShort:  filepath.Base(wd),
		historyDirty:  true,
	}
	m.printWelcome()
	m.promptHistory = loadPromptHistory()
	m.histPos = len(m.promptHistory)
	return m
}

// printWelcome prints the banner and the one-line hint into the transcript. It is
// a method rather than inline literal so a language switch can re-emit it: the
// banner is otherwise written once, at construction, in the old language.
func (m *uiModel) printWelcome() {
	m.history = append(m.history,
		line{kindSys, ""},
		line{kindLogo, logo},
		line{kindSys, ""},
		line{kindSys, i18n.T("ctrl+p commands · ctrl+b panel · ctrl+y copy · up/down history · esc stop")},
		line{kindSys, ""})
}

func (m *uiModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spin.Tick, tea.RequestWindowSize, m.toolCheckCmd())
}

// upgradeColorProfile asks the terminal what it actually supports.
//
// Bubble Tea starts from an assumed profile and only refines it when asked, so
// without this the app renders in the conservative base palette — and a
// conservative profile is what made bold and bright text look flat, because
// there was no foreground to set or brighten in the first place.
//
// The request is only sent for a profile that is not already truecolor: some
// terminals (Apple Terminal.app among them) answer these queries wrongly and
// can garble the output, so a terminal that is already good enough is left
// alone. The reply arrives as tea.ColorProfileMsg and is ignored here — the
// renderer applies it.
func upgradeColorProfile(p colorprofile.Profile) tea.Cmd {
	if p == colorprofile.TrueColor {
		return nil
	}
	return tea.Batch(
		tea.RequestCapability("RGB"),
		tea.RequestCapability("Tc"),
	)
}

// toolCheckCmd verifies in the background that the chosen provider can emit a
// tool call, because a host that ignores the tools array turns every turn into
// prose and dmcode is then useless. It never blocks startup: the keyless
// backend can take ~18s to cold-start, and the user must be able to type while
// that happens.
//
// Only the chat wire is checked. Providers on the /v1/responses wire are
// key-based and go through ADK's own client, which is not what verifyTools
// speaks, and their tool support is not in doubt.
// handleToolCheck reports a failed tool-calling check, and stays quiet on a
// pass or an inconclusive run. A stale result is dropped: the user may have
// switched models while the check was in flight, and an advisory about a model
// they are no longer using is worse than none.
func (m *uiModel) handleToolCheck(msg toolCheckMsg) {
	if msg.Model != m.prov.Model || msg.Label != m.prov.Label {
		return
	}
	if !msg.conclusive {
		return
	}
	if msg.ok {
		return
	}
	m.statusText = i18n.T("provider without tools")
	m.history = append(m.history, line{kindErr, fmt.Sprintf(
		i18n.T("⚠ %s (%s) cannot call tools: tasks will stay prose with no file edits. /setup — pick another."),
		msg.Label, msg.Model)})
	m.historyDirty = true
}

func (m *uiModel) toolCheckCmd() tea.Cmd {
	p := m.prov
	if p.Wire() != config.APIChat || p.Model == "" {
		return nil
	}
	return func() tea.Msg {
		ok, conclusive := llm.VerifyTools(p.BaseURL, p.APIKey, p.Model, llm.ToolProbeTimeout)
		return toolCheckMsg{ok: ok, conclusive: conclusive, Model: p.Model, Label: p.Label}
	}
}

// handleFailover records that a turn moved to another endpoint. The model and
// label in the sidebar follow the switch, because after it the reserve is the
// host actually answering — showing the old one would send /model and /models
// to an endpoint this session has already walked away from.
//
// The pool is rewritten rather than left alone: a member that just failed
// belongs at the back of the queue, so a later /model switch reuses the same
// order the failover discovered instead of starting the user back on the host
// that just failed.
func (m *uiModel) handleFailover(msg failoverMsg) tea.Cmd {
	m.prov = msg.to
	rest := make([]config.Provider, 0, len(m.pool))
	rest = append(rest, msg.to)
	for _, p := range m.pool {
		if p.BaseURL == msg.to.BaseURL && p.Model == msg.to.Model {
			continue
		}
		if p.BaseURL == msg.from.BaseURL && p.Model == msg.from.Model {
			continue
		}
		rest = append(rest, p)
	}
	rest = append(rest, msg.from)
	m.pool = rest
	m.statusText = i18n.T("failover: ") + msg.to.Label
	m.history = append(m.history, line{kindSys, fmt.Sprintf(
		i18n.T("⚡ %s is unavailable (%s) — %s (%s) answered"),
		msg.from.Label, msg.reason, msg.to.Label, msg.to.Model)})
	m.historyDirty = true
	// The reserve has not been checked for tool calling, and a host that
	// ignores tools turns every turn into prose.
	return m.toolCheckCmd()
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
		m.statusText = i18n.T("nothing to copy")
		return
	}
	if err := clipboard.WriteAll(text); err != nil {
		m.history = append(m.history, line{kindErr, i18n.T("clipboard error: ") + err.Error()})
	} else {
		m.statusText = i18n.T("reply copied to the clipboard!")
		m.history = append(m.history, line{kindSys, i18n.T("📋 the last reply was copied to the clipboard")})
	}
	m.historyDirty = true
	m.followVP()
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// extra carries a follow-up command a case wants to run after the switch,
	// batched with the textinput's own command at the tail.
	var extra tea.Cmd
	switch msg := msg.(type) {
	case tea.ColorProfileMsg:
		// The renderer has already adopted the reported profile; all that is left
		// is to ask for the finer capabilities when this one is not enough.
		return m, upgradeColorProfile(msg.Profile)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// No followVP here on purpose: the tail of Update re-syncs the viewport
		// for every message, which is what re-wraps the transcript at the new
		// width. Calling it twice would render the whole history twice.
		m.layout()
	case tea.PasteMsg:
		// A terminal paste arrives as PasteMsg, not as a run of key presses, so
		// the overlays that own plain string buffers never saw it — a key pasted
		// into /setup was silently dropped. The main prompt is a textinput and
		// handles this at the tail of Update, but an open overlay returns early
		// and never gets there, so route it by hand.
		if m.pasteTarget() != nil {
			m.pasteInto(string(msg.Content))
			return m, nil
		}
	case tea.MouseWheelMsg:
		// The wheel is routed through the viewport's own handler, which already
		// knows the shift-modifier horizontal case. What it cannot know is that
		// scrolling up must release the follow-the-tail stick: without this the
		// tail of Update would call followVP and snap straight back to the
		// bottom on the very next frame, making the wheel look dead.
		if !m.mouseEnabled {
			break
		}
		switch msg.Button {
		case tea.MouseWheelDown:
			m.scrollBy(m.vp.MouseWheelDelta)
		case tea.MouseWheelUp:
			m.scrollBy(-m.vp.MouseWheelDelta)
		default:
			// Left/right wheel and the horizontal modifiers stay with the
			// viewport, which scrolls sideways without touching the stick.
			m.vp, _ = m.vp.Update(msg)
			m.syncVP()
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.setup.open {
			model, cmd := m.setupKey(msg)
			return model, cmd
		}
		if m.palette.open {
			model, cmd := m.paletteKey(msg)
			return model, cmd
		}
		if m.picker.open {
			model, cmd := m.pickerKey(msg)
			return model, cmd
		}
		if m.lang.open {
			model, cmd := m.langKey(msg)
			return model, cmd
		}
		switch msg.String() {
		case "ctrl+v":
			// ctrl+v is not a paste inside a raw-mode TUI; the terminal sends the
			// control character and nothing else. Read the clipboard directly, so
			// the shortcut works wherever bracketed paste is unavailable.
			if m.pasteTarget() != nil {
				text, err := clipboard.ReadAll()
				if err != nil {
					m.statusText = i18n.T("clipboard error: ") + err.Error()
					return m, nil
				}
				m.pasteInto(text)
				return m, nil
			}
		case "esc":
			// The command list closes on escape before anything else, so a user
			// who opened it by accident gets their prompt back without losing the
			// text they typed or stopping a turn they did not mean to stop.
			if len(m.suggest) > 0 {
				m.suggest = nil
				return m, nil
			}
			if m.busy {
				if m.cancelTurn != nil {
					m.cancelTurn()
					m.cancelTurn = nil
				}
				m.busy = false
				m.statusText = i18n.T("turn stopped")
				m.history = append(m.history, line{kindSys, i18n.T("⏹ turn stopped by the user (Esc)")})
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
				m.statusText = i18n.T("turn stopped")
				m.history = append(m.history, line{kindSys, i18n.T("⏹ turn stopped by the user (Ctrl+C)")})
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
			m.scrollBy(-10)
			return m, nil
		case "pgdown":
			m.scrollBy(10)
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
			// Tab used to complete a suggestion here and switch the mode
			// everywhere else, so one key meant two different things depending on
			// what was in the input. The command list is now picked with the arrows
			// and enter, which leaves tab meaning one thing everywhere: the mode.
			return m, m.toggleMode()
		case "up", "down":
			if m.busy {
				return m, nil
			}
			// While the command list is up the arrows belong to it, not to the
			// prompt history. There is no way to reach both at once, and the list
			// is the transient one: it disappears the moment the text stops being a
			// command, and then the arrows are history again.
			if m.moveSuggest(map[string]int{"up": -1, "down": 1}[msg.String()]) {
				return m, nil
			}
			if msg.String() == "up" && len(m.promptHistory) > 0 && m.histPos > 0 {
				if m.histPos == len(m.promptHistory) {
					m.draft = m.input.Value()
				}
				m.histPos--
				m.input.SetValue(m.promptHistory[m.histPos])
				m.input.CursorEnd()
			}
			if msg.String() == "down" && m.histPos < len(m.promptHistory) {
				m.histPos++
				if m.histPos == len(m.promptHistory) {
					m.input.SetValue(m.draft)
				} else {
					m.input.SetValue(m.promptHistory[m.histPos])
				}
				m.input.CursorEnd()
			}
			return m, nil
		case "enter":
			// A highlighted row in the command list wins over whatever is in the
			// input. Typing "/" and pressing enter runs the first command, which
			// is what it always did; arrowing down first runs whichever one is
			// highlighted. Without this the arrows would only move a cursor and
			// the user would still have to type the command exactly.
			if m.suggestSel < len(m.suggest) && len(m.suggest) > 0 {
				text := strings.TrimSpace(m.suggest[m.suggestSel].text)
				m.input.SetValue(text)
				m.suggest = nil
				m.updateSuggest()
				// A /model row carries its argument already; anything else goes
				// through the command switch below as typed.
			}
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
					line{kindSys, i18n.T("ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply")},
					line{kindSys, i18n.T("esc — stop the current turn · up/down — prompt history · pgup/pgdown — scroll")},
					line{kindSys, i18n.T("tab — plan/act mode · wheel — scroll · /mouse — toggle the wheel")},
					line{kindSys, "/setup, /models, /model <id>, /history, /copy, /sidebar, /mode, /cd <path>, /new, /clear, /quit"})
				m.historyDirty = true
				m.followVP()
				return m, nil
			case "/setup":
				cmd := m.openSetup()
				m.followVP()
				return m, cmd
			case "/copy":
				m.copyLastResponse()
				return m, nil
			case "/clear":
				m.history = nil
				m.historyDirty = true
				m.followVP()
				return m, nil
			case "/history":
				m.showRecentPrompts()
				m.followVP()
				return m, nil
			case "/new":
				m.sessionID = newSessionID()
				m.turnCount = 0
				m.toolCallCount = 0
				m.history = append(m.history, line{kindSys, i18n.T("— session reset —")})
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
			case "/mouse":
				m.mouseEnabled = !m.mouseEnabled
				if m.mouseEnabled {
					m.statusText = i18n.T("mouse on — the wheel scrolls")
				} else {
					m.statusText = i18n.T("mouse off — the wheel is ignored")
				}
				return m, nil
			}
			if arg, ok := strings.CutPrefix(text, "/mode"); ok {
				if m.busy {
					return m, nil
				}
				return m, m.setMode(strings.TrimPrefix(strings.TrimSpace(arg), " "))
			}
			if arg, ok := strings.CutPrefix(text, "/cd"); ok {
				if m.busy {
					return m, nil
				}
				return m, m.changeDir(strings.TrimSpace(arg))
			}
			if strings.HasPrefix(text, "/debug") {
				// Diagnostics for the transcript pipeline: which line kind the
				// reply is stored under, and whether the markdown renderer saw
				// it. A reply that renders as raw markup is stored under some
				// kind other than kindAgent, so every non-empty line is listed
				// rather than guessed at.
				m.history = append(m.history, line{kindSys, fmt.Sprintf(
					"width=%d renderCalls=%d lines=%d",
					m.contentWidth(), renderCalls, len(m.history))})
				shown := 0
				for i := len(m.history) - 1; i >= 0 && shown < 6; i-- {
					l := m.history[i]
					if strings.TrimSpace(l.text) == "" {
						continue
					}
					row := m.rowStyle(l.kind)
					m.history = append(m.history, line{kindSys, fmt.Sprintf(
						"  [%d] %-7s md=%-5v %q",
						i, kindName(l.kind), row.markdown, truncate(oneLine(l.text), 46))})
					shown++
				}
				m.historyDirty = true
				m.followVP()
				return m, nil
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
			m.savePrompt(text)
			return m, m.startTurn(text)
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case deltaMsg:
		m.applyAgentText(msg)
	case toolCallMsg:
		m.lastTool = msg.name
		m.toolCallCount++
		m.statusText = i18n.T("calling: ") + msg.name
		m.history = append(m.history, line{kindTool, msg.name + "(" + msg.args + ")"})
		m.historyDirty = true
	case toolResMsg:
		m.statusText = i18n.T("returned: ") + msg.name
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
			m.picker = modelPicker{open: true, Models: msg.Models}
		}
	case setupCheckMsg:
		cmd := m.handleSetupCheck(msg)
		return m, cmd
	case modelSwitchedMsg:
		if msg.err != nil {
			m.history = append(m.history, line{kindErr, "Model: " + msg.err.Error()})
		} else {
			m.prov.Model = msg.name
			m.runner = msg.runner
			if len(msg.pool) > 0 {
				m.pool = msg.pool
			}
			// Preserve m.sessionID so conversation context is retained!
			m.history = append(m.history, line{kindSys, i18n.T("model activated: ") + msg.name + i18n.T(" (context kept)")})
			// A different model may or may not support tools, so check again.
			extra = m.toolCheckCmd()
		}

		m.historyDirty = true
	case turnDoneMsg:
		m.busy = false
		m.cancelTurn = nil
		if msg.err != nil {
			if msg.err == context.Canceled {
				m.statusText = i18n.T("stopped")
				m.history = append(m.history, line{kindSys, i18n.T("⏹ turn stopped")})
			} else {
				m.statusText = i18n.T("error")
				m.history = append(m.history, line{kindErr, "error: " + msg.err.Error()})
			}
		} else {
			m.statusText = i18n.T("ready")
		}
		m.history = append(m.history, line{kindSys, ""})
		m.historyDirty = true
	case errMsg:
		m.busy = false
		m.statusText = i18n.T("error")
		m.history = append(m.history, line{kindErr, "error: " + msg.Error()})
		m.historyDirty = true
	case toolCheckMsg:
		m.handleToolCheck(msg)
	case failoverMsg:
		extra = m.handleFailover(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateSuggest()
	m.followVP()
	return m, tea.Batch(cmd, extra)
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
// suggestMaxRows caps the command list. It is a floor on the transcript's
// height rather than a scrollable region: the list is transient and a user
// arrowing through it wants to see the reply above it, not a scrolling pane.
const suggestMaxRows = 6

// suggestHeight is the number of rows the command list occupies, so the viewport
// can be sized against it.
//
// The list is bounded by what the terminal can actually give up: layout() floors
// the chat panel at minViewRows, and a list that pushed past that would make the
// whole frame one row taller than the terminal — the input box, and the cursor
// in it, would fall off the bottom. Below the bound the list is dropped
// entirely rather than squeezed: the commands are still reachable by typing them,
// which is the whole reason a picker is a convenience and not the only path.
func (m *uiModel) suggestHeight() int {
	if len(m.suggest) == 0 {
		return 0
	}
	// Two rows is the floor: a header naming the keys above a list with no room
	// for a single command is worse than no list at all — it looks broken, and
	// the keys it advertises lead nowhere. One command and no marker, then.
	const minSuggestRows = 2
	// One row is the header naming the keys; the rest are the commands.
	want := min(len(m.suggest)+1, suggestMaxRows)
	room := m.height - chromeHeight - minViewRows
	if room < minSuggestRows {
		return 0
	}
	return min(want, room)
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

// applyAgentText does what a round's text part asks for: append a delta, or
// replace the reply of the round in progress when the closing response disagrees
// with the deltas that were streamed for it.
//
// A replacement rewrites the last agent line and drops the unbroken agent lines
// behind it, which were the earlier deltas of the same round arriving after a
// tool line had already split them off. It stops at the first non-agent line: a
// tool result the user has already read is not this round's text and stays where
// it is.
func (m *uiModel) applyAgentText(s spoken) {
	if s.text == "" {
		return
	}
	if s.replace {
		if i := m.lastAgentLine(); i >= 0 {
			m.history[i].text = s.text
			end := i + 1
			for end < len(m.history) && m.history[end].kind == kindAgent {
				end++
			}
			m.history = append(m.history[:i+1], m.history[end:]...)
			m.historyDirty = true
			return
		}
	}
	m.appendAgentText(s.text)
}

// lastAgentLine is the index of the newest agent line, or -1 when there is none.
func (m *uiModel) lastAgentLine() int {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].kind == kindAgent {
			return i
		}
	}
	return -1
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

// scrollBy moves the transcript n rows and keeps the follow-the-tail stick in
// step with where that left the view.
//
// One helper for keys and the wheel: the stick is what decides whether a new
// token pulls the view to the bottom, so any scroll that forgets to update it
// silently undoes itself on the next streamed message.
func (m *uiModel) scrollBy(n int) {
	if n < 0 {
		m.vp.ScrollUp(-n)
	} else {
		m.vp.ScrollDown(n)
	}
	m.stick = m.vp.AtBottom()
	m.syncVP()
}

func (m *uiModel) followVP() {
	m.syncVP()
	if m.stick {
		m.vp.GotoBottom()
	}
}

// suggestion is one row of the command picker: the text that goes into the input
// when it is chosen, and what it is, for the line beside it.
type suggestion struct {
	text string
	desc string
}

// updateSuggest rebuilds the command list shown above the input.
//
// The list is a picker, not a completion hint: the arrow keys move through it and
// enter runs whatever is highlighted, so there is no tab to press and nothing to
// remember about which key does what. Typing narrows it, and the first match is
// preselected, so "/" followed by enter still runs the first command — the
// keyboard-only path a user already had keeps working unchanged.
func (m *uiModel) updateSuggest() {
	m.suggest = nil
	m.suggestSel = 0
	if m.palette.open || m.picker.open || m.setup.open {
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
				m.suggest = append(m.suggest, suggestion{"/" + c.name, c.desc})
			}
		}
		return
	}
	if id, ok := strings.CutPrefix(text, "/model "); ok {
		q := strings.TrimSpace(id)
		for _, id := range m.picker.Models {
			if q == "" || strings.Contains(id, q) {
				m.suggest = append(m.suggest, suggestion{"/model " + id, i18n.T("switch to this model")})
			}
			if len(m.suggest) >= 8 {
				break
			}
		}
	}
}

// moveSuggest moves the highlight through the command list, stopping at the ends
// rather than wrapping: a list that jumps from the last row back to the first on
// one keypress is disorienting when the list is long and the intent was to stop.
func (m *uiModel) moveSuggest(delta int) bool {
	if len(m.suggest) == 0 {
		return false
	}
	next := m.suggestSel + delta
	if next < 0 {
		next = 0
	}
	if next > len(m.suggest)-1 {
		next = len(m.suggest) - 1
	}
	m.suggestSel = next
	return true
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
	// markdown marks text the model produced, which is rendered as markdown
	// rather than as literal prose.
	markdown bool
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
		return transcriptRow{style: styleAgent, markdown: true}
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
	// Markdown is only applied where the model produced it. Tool output and the
	// user's own prompts are literal text — a path or a JSON payload that happens
	// to contain "**" must survive untouched.
	if row.markdown {
		rows := renderMarkdown(text, width, row.style)
		for i, r := range rows {
			rows[i] = row.first + r
		}
		return rows
	}
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
			// A markdown row arrives already styled by the renderer; wrapping it
			// again would nest the escapes and break the width accounting.
			if row.markdown {
				b.WriteString(r + "\n")
				continue
			}
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

// openSetup starts /setup at the provider list.
func (m *uiModel) openSetup() tea.Cmd {
	m.setup.reset()
	m.setup.open = true
	m.statusText = "/setup"
	return nil
}

// setupKey walks the wizard. Escape always backs out, including from a text
// stage: a user who picked the wrong provider must never be trapped in a prompt
// with no way out.
// pasteTarget returns the buffer a paste should land in, or nil when no overlay
// has a text field focused — in which case the message belongs to the main
// textinput, which pastes on its own.
func (m *uiModel) pasteTarget() *string {
	switch {
	// The language list has no text field, so a paste has nowhere to go. It is
	// checked first so it cannot be shadowed by a stale overlay flag.
	case m.lang.open:
		return nil
	case m.setup.open && m.setup.stage != setupPick:
		return &m.setup.buf
	case m.picker.open:
		return &m.picker.query
	case m.palette.open:
		return &m.palette.query
	}
	return nil
}

// pasteInto appends pasted text to the focused overlay field.
//
// Pasted content is collapsed to a single line first. A key copied from a web
// page usually carries a trailing newline, and it would otherwise be written
// into .env as a value containing a line break — which DotEnvPair then reads as
// a broken assignment, so the key silently fails to load on the next start.
func (m *uiModel) pasteInto(text string) {
	dst := m.pasteTarget()
	if dst == nil {
		return
	}
	*dst += oneLine(text)
	// Typing resets the cursor, so a paste has to as well or the highlight
	// stays on the first row of a filtered list.
	if m.picker.open {
		m.picker.selected = 0
	}
	if m.palette.open {
		m.palette.selected = 0
	}
}

// oneLine flattens pasted text into a single line: CRLF and LF become nothing,
// stray carriage returns are dropped, and the result is trimmed. A value pasted
// into a one-field prompt must not be able to smuggle in a second line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}

func (m *uiModel) setupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	opts := config.SetupOptions()
	switch msg.String() {
	case "esc":
		m.setup.reset()
		m.statusText = i18n.T("setup cancelled")
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}

	switch m.setup.stage {
	case setupPick:
		switch msg.String() {
		case "up":
			if m.setup.selected > 0 {
				m.setup.selected--
			}
			return m, nil
		case "down":
			if m.setup.selected < len(opts)-1 {
				m.setup.selected++
			}
			return m, nil
		case "enter":
			m.setup.opt = opts[m.setup.selected]
			switch {
			case !m.setup.opt.Keyless:
				m.setup.stage = setupKey
				m.setup.buf = ""
			case m.setup.opt.BaseURL == "":
				m.setup.stage = setupURL
				m.setup.buf = ""
			default:
				return m, m.applySetup()
			}
			return m, nil
		}

	case setupKey:
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(m.setup.buf) == "" {
				m.statusText = i18n.T("no key entered")
				return m, nil
			}
			return m, m.applySetup()
		case "backspace":
			if r := []rune(m.setup.buf); len(r) > 0 {
				m.setup.buf = string(r[:len(r)-1])
			}
			return m, nil
		}

	case setupURL:
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(m.setup.buf) == "" {
				m.statusText = i18n.T("no base URL entered")
				return m, nil
			}
			m.setup.opt.BaseURL = strings.TrimSpace(m.setup.buf)
			m.setup.stage = setupModel
			m.setup.buf = ""
			return m, nil
		case "backspace":
			if r := []rune(m.setup.buf); len(r) > 0 {
				m.setup.buf = string(r[:len(r)-1])
			}
			return m, nil
		}

	case setupModel:
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(m.setup.buf) == "" {
				m.statusText = i18n.T("no model entered")
				return m, nil
			}
			m.setup.opt.Model = strings.TrimSpace(m.setup.buf)
			return m, m.applySetup()
		case "backspace":
			if r := []rune(m.setup.buf); len(r) > 0 {
				m.setup.buf = string(r[:len(r)-1])
			}
			return m, nil
		}
	}

	if len(msg.Text) > 0 && m.setup.stage != setupPick {
		m.setup.buf += msg.Text
	}
	return m, nil
}

// applySetup validates the chosen provider and then persists it to .env,
// exports it for this process, and rebuilds the agent so the change takes
// effect without a restart. The key is never echoed into the transcript, and
// the write is 0600 because .env holds a secret.
//
// A provider that needs a key is probed first. Writing an unverified key would
// overwrite whatever OPENAI_API_KEY held — commonly a working one — and the
// 401 would only appear on the next message, by which point the previous
// configuration is unrecoverable without retyping it.
func (m *uiModel) applySetup() tea.Cmd {
	opt := m.setup.opt
	vars := config.SetupVars(opt, strings.TrimSpace(m.setup.buf))

	// Keyless endpoints were just chosen from a list known to work, and a
	// custom endpoint may not implement /models at all, so only a provider with
	// a real key is worth probing.
	if opt.Keyless || opt.EnvKey == "" || m.setup.force {
		return m.commitSetup(opt, vars)
	}

	m.statusText = i18n.T("checking the key…")
	probe := config.Provider{
		BaseURL: vars["OPENAI_BASE_URL"],
		APIKey:  vars[opt.EnvKey],
		Model:   config.OrDefaultModel(vars["DMCODE_MODEL"]),
		API:     opt.API,
	}
	return func() tea.Msg {
		_, err := config.ListModels(probe)
		return setupCheckMsg{vars: vars, opt: opt, err: err}
	}
}

// handleSetupCheck acts on the key probe. A rejection keeps the wizard open at
// the key prompt with .env untouched, so the user can paste the right key
// instead of discovering later that the working one is gone.
func (m *uiModel) handleSetupCheck(msg setupCheckMsg) tea.Cmd {
	if msg.err == nil {
		return m.commitSetup(msg.opt, msg.vars)
	}

	name := setupLabel(msg.opt)
	m.history = append(m.history,
		line{kindErr, i18n.T("the endpoint rejected the key: ") + name + " — " + msg.err.Error()},
		line{kindSys, i18n.T(".env was left unchanged.")})
	// Back to the key prompt, cleared, so the rejected key is not resubmitted by
	// accident on the next attempt.
	m.setup.stage = setupKey
	m.setup.buf = ""
	m.setup.force = true
	m.statusText = i18n.T("key rejected")
	if msg.opt.Signup != "" {
		m.history = append(m.history, line{kindSys, i18n.T("Get a key here: ") + msg.opt.Signup})
	}
	m.history = append(m.history, line{kindSys, i18n.T("press enter on an empty field to save it anyway")})
	m.historyDirty = true
	m.followVP()
	return nil
}

// commitSetup writes the validated provider to .env and rebuilds the agent.
func (m *uiModel) commitSetup(opt config.SetupOption, vars map[string]string) tea.Cmd {
	// Merge into any existing .env rather than replacing it, so a key the user
	// set for another provider is not silently dropped.
	lines, err := config.ReadDotEnv()
	if err != nil {
		m.statusText = i18n.T("error reading .env")
		m.history = append(m.history, line{kindErr, "setup: " + err.Error()})
		m.setup.reset()
		m.historyDirty = true
		return nil
	}
	merged, _ := config.MergeDotEnv(lines, vars)

	var sb strings.Builder
	for _, l := range merged {
		sb.WriteString(l + "\n")
	}
	if werr := os.WriteFile(".env", []byte(sb.String()), 0o600); werr != nil {
		m.statusText = i18n.T("could not write .env")
		m.history = append(m.history, line{kindErr, "setup: " + werr.Error()})
		m.setup.reset()
		m.historyDirty = true
		return nil
	}
	for _, k := range config.SortedKeys(vars) {
		os.Setenv(k, vars[k])
	}

	p := config.Provider{
		BaseURL:   vars["OPENAI_BASE_URL"],
		Model:     config.OrDefaultModel(vars["DMCODE_MODEL"]),
		API:       opt.API,
		Label:     setupLabel(opt),
		Reasoning: vars["DMCODE_REASONING_EFFORT"],
	}
	if !opt.Keyless {
		p.APIKey = vars[opt.EnvKey]
	}
	m.prov = p
	m.setup.reset()
	m.statusText = i18n.T("provider: ") + p.Label
	m.history = append(m.history, line{kindSys, i18n.T("provider saved to .env: ") + p.Label})
	m.historyDirty = true

	ctx := context.Background()
	a, berr := dmagent.BuildAgent(ctx, p, m.tools)
	if berr != nil {
		m.history = append(m.history, line{kindErr, "setup: " + berr.Error()})
		return nil
	}
	r, rerr := runner.New(runner.Config{
		AppName:           "dmcode",
		Agent:             a,
		SessionService:    m.svc,
		AutoCreateSession: true,
	})
	if rerr != nil {
		m.history = append(m.history, line{kindErr, "setup: " + rerr.Error()})
		return nil
	}
	m.runner = r
	// A new provider needs its own tool-calling verdict.
	return m.toolCheckCmd()
}

// setupLabel names a wizard option for the sidebar, where the provider is shown.
func setupLabel(opt config.SetupOption) string {
	if opt.BaseURL == "" {
		return i18n.T("Custom endpoint")
	}
	host := opt.BaseURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return host
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
			m.history = append(m.history, line{kindSys, i18n.T("· model → ") + id})
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
	for _, id := range m.picker.Models {
		if q == "" || strings.Contains(strings.ToLower(id), q) {
			out = append(out, id)
		}
	}
	return out
}

func (m *uiModel) fetchModelsCmd() tea.Cmd {
	p := m.prov
	return func() tea.Msg {
		Models, err := config.ListModels(p)
		return modelsListMsg{Models: Models, err: err}
	}
}

// switchModelCmd rebuilds the agent on a different model of the current
// provider. The pool travels with it: the user picked a model, not a single
// endpoint, and silently dropping the reserves would leave a later 429 with
// nowhere to go.
func (m *uiModel) switchModelCmd(id string) tea.Cmd {
	p := m.prov
	p.Model = id
	// Retain this session's ordering: active host first, the one that just
	// failed last. The pool member being edited is the one currently answering.
	pool := make([]config.Provider, 0, len(m.pool))
	edited := false
	for _, member := range m.pool {
		if !edited && member.BaseURL == p.BaseURL && member.APIKey == p.APIKey {
			pool = append(pool, p)
			edited = true
			continue
		}
		pool = append(pool, member)
	}
	if !edited {
		pool = append([]config.Provider{p}, pool...)
	}
	svc, ts := m.svc, m.activeTools()
	return func() tea.Msg {
		ctx := context.Background()
		a, err := dmagent.BuildPooledAgent(ctx, pool, ts, m.switchNotifier(), m.mode.agentMode())
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
		return modelSwitchedMsg{name: id, runner: r, pool: pool}
	}
}

// switchNotifier hands a failover to the event loop. It is nil-safe: a switch
// can only happen once a turn is running, and a program that is not up yet
// cannot have one.
func (m *uiModel) switchNotifier() func(llm.SwitchEvent) {
	prog := m.prog
	return func(ev llm.SwitchEvent) {
		if prog == nil {
			return
		}
		prog.Send(failoverMsg{from: ev.From, to: ev.To, reason: ev.Reason})
	}
}

func (m *uiModel) startTurn(text string) tea.Cmd {
	p := m.prog
	r, userID, sessionID := m.runner, "user", m.sessionID
	turnCtx, cancel := context.WithCancel(context.Background())
	m.cancelTurn = cancel
	m.turnCount++
	m.statusText = i18n.T("generating a reply…")
	return func() tea.Msg {
		userMsg := genai.NewContentFromText(text, genai.RoleUser)
		// The accumulator is per LLM round, and a round ends at every
		// finalised response — see turnText.
		var tt turnText
		for ev, err := range r.Run(turnCtx, userID, sessionID, userMsg, adkagent.RunConfig{
			StreamingMode: adkagent.StreamingModeSSE,
		}) {
			if err != nil {
				return turnDoneMsg{err}
			}
			if ev.LLMResponse.Content == nil {
				continue
			}
			// The text of one event is dispatched as a unit, before the tool
			// parts it shares the event with: the accumulator has to be reset
			// once per event, and a finalised response carrying both text and
			// a function call would otherwise close the round twice.
			var said strings.Builder
			var parts []toolPart
			for _, part := range ev.LLMResponse.Content.Parts {
				switch {
				case part.Text != "":
					said.WriteString(part.Text)
				case part.FunctionCall != nil:
					parts = append(parts, toolPart{call: part.FunctionCall})
				case part.FunctionResponse != nil:
					parts = append(parts, toolPart{resp: part.FunctionResponse})
				}
			}
			if s := tt.add(said.String(), ev.LLMResponse.Partial); s.text != "" {
				p.Send(deltaMsg(s))
			}
			for _, tp := range parts {
				switch {
				case tp.call != nil:
					args, _ := json.Marshal(tp.call.Args)
					p.Send(toolCallMsg{name: tp.call.Name, args: truncate(string(args), 160)})
				case tp.resp != nil:
					p.Send(toolResMsg{name: tp.resp.Name, output: renderToolResponse(tp.resp)})
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

	row(styleSidebarLabel, i18n.T("MODEL"))
	value(styleSidebarValue, " ", m.prov.Model)
	if m.prov.Label != "" {
		row(styleHint, truncate("via "+m.prov.Label, sbInner))
	}
	b.WriteString("\n")

	row(styleSidebarLabel, i18n.T("SESSION"))
	row(styleHint, " "+truncate(m.sessionID, sbInner-1))
	row(styleHint, fmt.Sprintf(i18n.T(" turns: %d"), m.turnCount))
	row(styleHint, fmt.Sprintf(i18n.T(" tools: %d"), m.toolCallCount))
	b.WriteString("\n")

	row(styleSidebarLabel, i18n.T("FOLDER"))
	// The full path, trimmed from the left: a Windows path is far wider than the
	// panel, and the tail is the part that identifies the project.
	value(styleSidebarValue, " ", shortenPath(m.workDir, sbInner-1))
	b.WriteString("\n")

	if m.lastTool != "" {
		row(styleSidebarLabel, i18n.T("LAST TOOL"))
		value(styleTool, " ⏺ ", m.lastTool)
		b.WriteString("\n")
	}

	row(styleSidebarLabel, i18n.T("TOOLS"))
	for _, t := range m.activeToolNames() {
		row(styleHint, " · "+t)
	}
	b.WriteString("\n")

	row(styleSidebarLabel, i18n.T("HOTKEYS"))
	row(styleHint, i18n.T(" ctrl+p  commands"))
	row(styleHint, i18n.T(" ctrl+b  hide panel"))
	row(styleHint, i18n.T(" ctrl+y  copy reply"))
	row(styleHint, i18n.T(" tab     plan/act"))
	row(styleHint, i18n.T(" esc     stop turn"))
	row(styleHint, i18n.T(" pgup/dn scroll"))

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

// statusBarView renders the single-row footer.
//
// It carries the mode and the state, and nothing else. The model name used to sit
// here too, which was the third place on screen showing it (the header and the
// sidebar already do), and a hotkey list that repeated what /help is for. Both
// were cut rather than rearranged: a status bar that only changes when the agent
// or the user does something is a bar worth reading, and a permanently
// half-illegible run of `ctrl+p │ ctrl+y │ esc` is not — the keys it advertised
// dropped off the edge anyway as the terminal narrowed.
//
// Everything here competes for one line, so the free-text status is dropped
// first and the two badges are the last thing standing. Overflowing is not
// cosmetic: lipgloss word-wraps the surplus onto a second row, which makes the
// whole frame one row taller than the terminal and scrolls the input off the
// bottom.
// stateBadge is the one piece of status the bar always carries: what the agent
// is doing right now. It is a function so the width arithmetic in the bar and in
// its tests both measure the rendered string rather than a copy of it — an emoji
// in "⏳ WORKING" is two cells wide, and guessing one cell short is how a row ends
// up a column wider than the terminal and wraps.
func (m *uiModel) stateBadge() string {
	switch {
	case m.busy:
		return styleBadgeBusy.Render(i18n.T("⏳ WORKING"))
	case m.statusText == i18n.T("turn stopped"), m.statusText == i18n.T("stopped"):
		return styleBadgeStop.Render(i18n.T("⏹ STOPPED"))
	default:
		return styleBadgeReady.Render(i18n.T("● READY"))
	}
}

func (m *uiModel) statusBarView() string {
	width := max(m.width, 16)
	inner := width - 2 // the bar's own left/right padding

	badge := m.stateBadge()

	statusDesc := m.statusText
	switch {
	case statusDesc == "" && m.busy:
		statusDesc = m.spin.View() + " " + i18n.T("running…")
	case statusDesc == "":
		statusDesc = i18n.T("waiting for a task")
	case m.busy:
		statusDesc = m.spin.View() + " " + statusDesc
	}

	// The mode leads: it is the one thing a user must never have to hunt for,
	// because it decides whether the agent is allowed to touch their files.
	left := m.modeBadge() + "  " + badge
	if ansi.StringWidth(left)+2+ansi.StringWidth(statusDesc) > inner && ansi.StringWidth(left)+2 <= inner {
		return styleStatusBar.Width(width).Render(left)
	}
	if ansi.StringWidth(left)+2 > inner {
		// Too narrow even for both badges: the mode is kept, the state goes.
		return styleStatusBar.Width(width).Render(truncate(m.modeBadge(), inner))
	}
	return styleStatusBar.Width(width).Render(left + "  " + truncate(statusDesc, inner-ansi.StringWidth(left)-2))
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
	model := truncate(m.prov.Model, max(avail-ansi.StringWidth(sep)-8, 4))
	id := truncate(m.sessionID, max(avail-ansi.StringWidth(sep)-ansi.StringWidth(model), 4))

	return styleTopBar.Width(width).Render(
		styleHeader.Render(name) + styleHint.Render(sep+model+sep+id))
}

// suggestView renders the command list above the input. The highlighted row is
// the one enter will run, and it is marked rather than merely coloured, so the
// list is still readable on a terminal whose font has no distinct bold face.
func (m *uiModel) suggestView() string {
	if len(m.suggest) == 0 {
		return ""
	}
	const indent = 2
	// Every row is padded or cut to exactly the terminal width: a row one cell
	// wider than the rest shifts the whole frame sideways, and JoinVertical pads
	// to the widest block. The width is measured with ansi.StringWidth on plain
	// text, never on a styled string, because truncate counts bytes and an escape
	// sequence is not a cell.
	body := max(m.width-indent, 4)  // header rows, no gutter
	row := max(m.width-indent-3, 4) // command rows carry a 3-cell marker

	rows := []string{strings.Repeat(" ", indent) + fitCells(truncate(i18n.T("↑↓ choose · enter run · esc dismiss"), body), body)}

	height := m.suggestHeight()
	budget := height - 1 // the header above already took one
	start := 0
	if m.suggestSel >= budget {
		// Scroll so the highlighted row is always on screen; without this the
		// cursor walks off the bottom of the list and the user loses it.
		start = m.suggestSel - budget + 1
	}
	if start > len(m.suggest)-budget {
		start = max(len(m.suggest)-budget, 0)
	}

	// The "↓ more" marker is only worth a row if one is left over. Shrinking the
	// list to make room for it is what keeps it from being cut off by the height
	// cap below — a marker that never renders is worse than none, because the
	// list then looks complete when it is not.
	more := len(m.suggest) - (start + budget)
	if more > 0 && budget > 1 {
		budget--
		more = len(m.suggest) - (start + budget)
	}

	// The description is a hint, not the row: it goes first when the terminal is
	// too narrow to carry both, and the command name is never truncated away.
	for i := start; i < len(m.suggest) && i < start+budget; i++ {
		s := m.suggest[i]
		gutter := "   "
		if i == m.suggestSel {
			gutter = " ▸ "
		}
		text := s.text
		if s.desc != "" {
			if room := row - ansi.StringWidth(text) - 4; room >= 8 {
				text += "  — " + truncate(s.desc, room)
			}
		}
		rows = append(rows, strings.Repeat(" ", indent)+gutter+fitCells(text, row))
	}
	if more > 0 {
		rows = append(rows, strings.Repeat(" ", indent)+
			fitCells(truncate(i18n.T("↓ more")+" "+fmt.Sprint(more), body), body))
	}
	// Height is capped separately: on a very short terminal the rows are dropped
	// from the bottom rather than wrapped, which would push the input off screen.
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

// fitCells right-fills s with spaces to exactly w cells, and cuts it if it is
// longer. The width is measured with ansi.StringWidth rather than len, so an
// already-styled string is not counted in bytes.
func fitCells(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return truncate(s, max(w, 1))
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
		styleHeader.Render(" "+title+" ") + styleHint.Render(i18n.T("esc — close")),
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
		rows = append(rows, styleHint.Render(i18n.T("   nothing found")))
	}
	if hidden := total - len(entries); hidden > 0 {
		rows = append(rows, styleHint.Render(i18n.T("   ↓ more ")+fmt.Sprint(hidden)))
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
	return m.floatingPanel(i18n.T("⌘ commands"), m.palette.query, entries, m.palette.selected)
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
		case id == m.prov.Model:
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
	return m.floatingPanel(i18n.T("⌘ models"), m.picker.query, entries, m.picker.selected)
}

// setupBox renders the /setup wizard: the provider list, then a key prompt, or
// the base-URL and model prompts for a custom endpoint.
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

func (m *uiModel) setupBox() string {
	inner := m.floatingWidth() - panelBorder

	switch m.setup.stage {
	case setupKey:
		// The key is a secret: show how much has been typed, never what. A
		// shoulder-surfer or a screen share must not leak it, and the transcript
		// stores only the masked form.
		rows := [][]string{{styleHint.Render(i18n.T("key (input hidden, paste or ctrl+v, then enter):"))},
			{maskedKey(m.setup.buf, inner) + "█"}}
		return m.floatingPanel(i18n.T("? provider key"), "", rows, 0)

	case setupURL:
		rows := [][]string{{styleHint.Render(i18n.T("base URL, e.g. http://localhost:1234/v1"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? custom endpoint"), "", rows, 0)

	case setupModel:
		rows := [][]string{{styleHint.Render(i18n.T("model id on this endpoint"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? model"), "", rows, 0)
	}

	opts := config.SetupOptions()
	var entries [][]string
	for i, o := range opts {
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.setup.selected:
			marker, style = " ? ", styleTool
		case setupLabel(o) == m.prov.Label:
			marker, style = " * ", styleTool
		}
		rows := wrapIndent(o.Label, inner, marker, "     ")
		if i != m.setup.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	// * marks the provider already in use, ? the highlighted row.
	return m.floatingPanel(i18n.T("? provider  (* — current, enter — select, esc — cancel)"), "", entries, m.setup.selected)
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

// langBox renders the /lang list: * marks the active language, ? the cursor.
func (m *uiModel) langBox() string {
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, l := range i18n.Langs {
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.lang.selected:
			marker, style = " ? ", styleTool
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
	return m.floatingPanel(i18n.T("? language  (* — current, enter — select, esc — cancel)"), "", entries, m.lang.selected)
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
		return tea.NewView(i18n.T("dmcode is starting…"))
	}
	if m.picker.open || m.palette.open || m.setup.open || m.lang.open {
		box := m.paletteBox()
		switch {
		case m.picker.open:
			box = m.modelPickerBox()
		case m.setup.open:
			box = m.setupBox()
		case m.lang.open:
			box = m.langBox()
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
	if m.suggestHeight() > 0 {
		parts = append(parts, m.suggestView())
	}
	parts = append(parts, stylePanel.Width(m.width).Render(m.input.View()))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
	v.AltScreen = true
	// Cell motion is what makes the terminal report the wheel at all; without it
	// no MouseWheelMsg is ever produced and scrolling cannot work. The cost is
	// that drag-select no longer works in the terminal itself, which is why
	// /mouse can turn this back off for anyone who needs to copy by hand.
	if m.mouseEnabled {
		v.MouseMode = tea.MouseModeCellMotion
	} else {
		v.MouseMode = tea.MouseModeNone
	}
	return v
}

func RunTUI(ctx context.Context, p config.Provider, pool []config.Provider, agentTools, readOnlyTools []tool.Tool, toolNames []string) error {
	if len(pool) == 0 {
		pool = []config.Provider{p}
	}
	// The notifier needs the program, which needs the model, which needs the
	// agent: the program is wired in afterwards, and a failover cannot happen
	// before the first turn anyway.
	m := InitialModel(nil, nil, p, agentTools, readOnlyTools, toolNames)
	m.pool = pool
	m.ctx = ctx

	prog := tea.NewProgram(m)
	m.prog = prog

	r, err := m.newRunner(pool, agentTools, modeAct)
	if err != nil {
		return err
	}
	m.runner = r

	_, err = prog.Run()
	return err
}

// newRunner assembles the agent and the session runner for a tool set.
//
// The session service is created once and reused across mode switches: keeping
// it is what preserves the conversation, so flipping to plan and back does not
// cost the user the context they had built up.
func (m *uiModel) newRunner(pool []config.Provider, ts []tool.Tool, mode agentMode) (*runner.Runner, error) {
	a, err := dmagent.BuildPooledAgent(m.ctx, pool, ts, m.switchNotifier(), mode.agentMode())
	if err != nil {
		return nil, err
	}
	if m.svc == nil {
		m.svc = session.InMemoryService()
	}
	return runner.New(runner.Config{
		AppName:           "dmcode",
		Agent:             a,
		SessionService:    m.svc,
		AutoCreateSession: true,
	})
}

// rebuildRunner re-creates the agent for the current mode and directory, keeping
// the session id and the session service so the conversation survives.
func (m *uiModel) rebuildRunner() tea.Cmd {
	if m.pool == nil {
		return nil
	}
	r, err := m.newRunner(m.pool, m.activeTools(), m.mode)
	if err != nil {
		m.statusText = err.Error()
		return nil
	}
	m.runner = r
	return nil
}
