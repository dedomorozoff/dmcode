package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/discover"
	"github.com/dedomorozoff/dmcode/internal/editor/editor"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/imgprev"
	"github.com/dedomorozoff/dmcode/internal/llm"
	"github.com/dedomorozoff/dmcode/internal/memsession"
	"github.com/dedomorozoff/dmcode/internal/todo"
	"github.com/dedomorozoff/dmcode/internal/tools"
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
	// kindImage is the preview of a picture the user attached. It is appended
	// last because the values are iota and tests compare them by number.
	kindImage
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
	case kindImage:
		return "image"
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
	// The change tally. Additions and removals are coloured apart because a
	// signed count read at a glance is the whole point of showing them, and
	// "+12 -3" in one colour is just a number pair.
	styleAdd = lipgloss.NewStyle().Foreground(cSuccess)
	styleDel = lipgloss.NewStyle().Foreground(cErr)

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
	sidebarBoxWidth = 31 // outer width of the sidebar box, margins excluded
	sidebarGap      = 1  // blank columns between the chat panel and the sidebar
	minSidebarTerm  = 90 // below this terminal width the sidebar is dropped
	minChatWidth    = 36 // the chat panel is not squeezed below this

	headerHeight = 2 // 1 row of text + 1 bottom border
	statusHeight = 1 // single row
	inputHeight  = 3 // 1 row of text + 2 borders
	panelBorder  = 2 // the chat panel's top and bottom border
	minViewRows  = 1 // a framed panel is an empty row between two borders

	// pendingPanelBorder is the pending-image strip's own top and bottom border.
	// It is separate from panelBorder because the strip is a panel of its own
	// above the input box, not more of the chat panel.
	pendingPanelBorder = 2

	// chromeHeight is everything on screen that is not transcript: the status
	// bar, the input box and the chat panel's own two borders. The header and the
	// pending-image strip are added on top when they are shown — see
	// chromeHeight(), the one place the three combine.
	chromeHeight = statusHeight + inputHeight + panelBorder
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

// retryMsg reports an endpoint about to be tried again, so the wait shows up
// as itself rather than as a spinner that looks hung.
type retryMsg struct{ ev llm.RetryEvent }

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

// launchGGUF is the launcher behind the GGUF setup option. It is a variable so
// the tests can stand in for a model load, which no test wants to wait out.
var launchGGUF = discover.LaunchGGUF

// ggufReadyMsg reports the outcome of starting llama-server on the .gguf file
// the user named in /setup. The load happens off the event loop because it can
// take minutes; the message is how the loop learns it is done.
type ggufReadyMsg struct {
	prov config.Provider
	err  error
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
	pool  []config.Provider
	tools []tool.Tool
	// mcpToolsets are the external MCP servers, resolved lazily per turn.
	// They live beside tools because they are not flattened into it: the
	// MCP connection opens on the first turn that needs it.
	mcpToolsets []tool.Toolset
	prog        *tea.Program
	toolNames   []string
	palette     paletteState
	picker      modelPicker
	setup       setupState
	lang        langState
	suggest     []suggestion
	// suggestSel is the highlighted row of the command list. It is reset on every
	// keystroke, so a narrowed list always starts at the top.
	suggestSel int
	// suggestFor is the input text the list was last built from. It is what keeps
	// a cursor blink from resetting suggestSel: the rebuild runs on every message,
	// not only on the ones that change the text.
	suggestFor string
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
	// yoloTools is the yolo-mode instrument set: the full one minus ask_user, so
	// the mode's promise — the agent will not stop to ask — is a property of what
	// it can reach rather than a line in the instruction it is asked to believe.
	yoloTools []tool.Tool
	// selection is a mouse drag over the frame. The anchor is where the button
	// went down and focus is where it is now; the two are stored in that order
	// rather than normalised, because a drag that goes up and to the left still
	// selects the text between them and has to copy in reading order, not in the
	// order the mouse happened to travel.
	selAnchorX, selAnchorY int
	selFocusX, selFocusY   int
	// selDown is the button being held. selMoved is whether it has travelled far
	// enough to mean something: without it every plain click would copy one
	// character, and the clipboard would be useless for the rest of the session.
	selDown  bool
	selMoved bool
	// frame is the last frame handed to the renderer. It is kept because the
	// selection is extracted from what is actually on screen, not from the
	// history: the same text is wrapped, padded and styled differently by the
	// time it is a row, and copying the unwrapped source would give the user
	// something that is not what they highlighted.
	frame        string
	mouseEnabled bool
	statusText   string

	// sessions is the conversation store behind m.svc, kept as its own field
	// because it can do what session.Service cannot: rewind a turn and list
	// what earlier runs left behind. m.svc stays the interface the runner
	// needs; this is the same object seen from the side that can change it.
	sessions *memsession.Service
	// sessionsList is the /sessions overlay. It lives on the model rather than
	// being rebuilt per frame so the highlight and the filter survive redraws.
	sessionsList sessionsState
	// ask is the choice overlay, open while the agent waits for an answer.
	// Answers to it go back over a channel, so the field also carries the
	// pending reply.
	ask askState
	// askTimeout is how long a question waits before the recommended option is
	// chosen for the user. Zero is off, which is the default.
	askTimeout time.Duration
	// pendingMode is a mode switch the agent asked for, applied between turns.
	pendingMode pendingMode
	// recoveredSessions counts the conversations found on disk at startup, so
	// the welcome can say that /sessions has something in it instead of
	// leaving the user to wonder whether their history survived.
	recoveredSessions int
	// promptMarks records, for every prompt sent, where its line sits in the
	// transcript. ctrl+z cuts the transcript at the last mark and the session
	// at the matching event, and the two have to agree — a transcript that kept
	// a turn the model forgot would show the user a conversation that, from the
	// model's side, never happened.
	promptMarks []promptMark

	// ctx outlives Init so a mode switch can rebuild the agent on the same
	// context the session started on, instead of handing the pool a fresh
	// background that nothing can cancel.
	ctx  context.Context
	mode agentMode
	// beforeYolo is the mode to come back to when yolo is switched off, so
	// shift+tab does not silently undo a deliberate plan-mode choice. It is only
	// ever read while the mode is yolo.
	beforeYolo agentMode

	// History render cache for streaming performance
	cachedHistory string
	cachedWidth   int
	historyDirty  bool
	cachedRows    []cachedLine
	proxy         proxyState
	// pending are the images attached to the next turn. They live on the model
	// rather than inside the input text so a preview can be drawn and the bytes
	// sent without re-reading the file, and so a rewind can put them back.
	pending []pendingImage
	// profile is the colour profile the terminal reported, which decides how an
	// image preview is drawn. It is recorded rather than asked for at draw time
	// because the renderer is the only thing that knows it.
	profile colorprofile.Profile
	// watchedPath is the picture path last looked at in the input. It exists so
	// that a path already attached is not attached again on every keystroke — the
	// input is watched on each one, and re-decoding the same file for each is a
	// stall the user can see.
	watchedPath string
	// ed is the workspace: the merged dmed editor with its project tree, git
	// panel and terminal. It is created on the first ctrl+e and lives for the
	// rest of the session — tabs, tree state and the PTY survive the switches
	// between the editor's main area and the chat (ed.Chat).
	ed *editor.Model
	// hostCall guards the re-entrancy in the host callbacks: the editor calls
	// back into the chat's key handling, which is the same Update — the flag
	// tells it the message has already been routed past the editor once.
	hostCall bool
	// pendingEditor is a switch into the editor mode asked for from inside the
	// editor's own Update (a host callback: ctrl+e, the /editor command). The
	// editor's Update returns its own copy and editorUpdate would clobber a
	// direct flip of m.ed — so the flip is deferred until the copy lands.
	pendingEditor bool
	winW, winH    int
}

// cachedLine holds the rendered rows of one history line. It is index-aligned
// with m.history: a streamed token rewrites the text of the last agent line,
// so on the next frame every earlier line still matches its cache entry and is
// reused as-is, and only the line that changed is re-rendered and re-wrapped.
type cachedLine struct {
	kind  lineKind
	text  string
	width int
	rows  []string
}

type modelPicker struct {
	open     bool
	query    string
	selected int
	Models   []string
}

// promptMark ties a sent prompt to the transcript line it produced, so a rewind
// can cut both the visible history and the model's memory at the same place.
type promptMark struct {
	text string
	// idx is the length m.history had before the prompt's own line was
	// appended, so the rewind truncates to exactly that and keeps everything
	// above it — including the "session reset" notices /new leaves behind.
	idx int
	// images are the pictures that turn carried, restored on a rewind. Without
	// them a prompt sent as nothing but a screenshot comes back as an empty
	// input box: the store has no text for it, so the fallback to mark.text is
	// also empty, and the user has to find the file again.
	images []imgprev.Attachment
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
	setupGGUF
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
	// gguf is the file browser behind the GGUF path prompt. reset() wipes it
	// along with the wizard, so a cancelled /setup never leaves the dialog up.
	gguf ggufPickState
}

func (s *setupState) reset() {
	*s = setupState{}
}

// ggufPickState browses the disk for a .gguf file while /setup sits at the
// path prompt. Directories are entered with enter; a .gguf file is picked the
// same way. It lives inside setupState so a wizard reset closes it too.
type ggufPickState struct {
	open     bool
	dir      string
	entries  []ggufEntry
	selected int
}

type ggufEntry struct {
	label string // what the row shows; directories carry a trailing separator
	path  string // absolute path used for navigation and for the final pick
	dir   bool
}

// proxyState drives the /proxy dialog: a short menu of actions, then a text
// stage for whichever one needs a value. It exists because "set one with:
// /proxy <url>" asked the user to remember a command syntax on top of the
// proxy address itself, and to know in advance whether the address worked.
const (
	proxyPick = iota
	proxyURL
	proxyNo
)

type proxyState struct {
	open     bool
	stage    int
	selected int
	buf      string
}

func (s *proxyState) reset() {
	*s = proxyState{}
}

type command struct {
	name string
	desc string
	run  func(m *uiModel) tea.Cmd
}

// editorUpdate forwards a message into the embedded editor and keeps the
// pointer fresh: the editor's Update has a value receiver, so the state that
// survived the message is the value it returns.
func (m *uiModel) editorUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.ed.Update(msg)
	if em, ok := nm.(editor.Model); ok {
		m.ed = &em
	}
	if m.pendingEditor {
		m.pendingEditor = false
		m.enterEditor()
	}
	return m, cmd
}

// quitCmd tears the workspace down — the terminal's shell above all — and
// ends the program. Every exit path goes through it, so closing dmcode never
// leaves a shell running behind the terminal.
func (m *uiModel) quitCmd() tea.Cmd {
	if m.ed != nil {
		m.ed.Shutdown()
	}
	return tea.Quit
}

// openEditor switches the workspace into the editor mode. Called either from
// the plain chat (the editor does not exist yet) or from inside the editor's
// own Update — a host callback — where a direct flip of m.ed would be
// clobbered by the copy the editor's Update returns, so the flip defers.
func (m *uiModel) openEditor() tea.Cmd {
	init := m.ensureEditor()
	if m.hostCall {
		m.pendingEditor = true
		return init
	}
	m.enterEditor()
	return init
}

// enterEditor flips the (already existing) workspace into the editor mode:
// the tabs for the files the agent changed open on top. The project tree and
// the other panels stay as the user last left them — closed until asked for
// with ctrl+b, F9 or the status-bar icons.
func (m *uiModel) enterEditor() {
	m.ed.Chat = false
	m.ed.OpenChangedTabs()
}

// ensureEditor creates the workspace the first time it is needed, on the
// folder the session is scoped to, and wires the chat side of it: the keys
// and clicks its main-area callbacks hand back, and the transcript rendered
// at the editor's geometry. The returned command is the editor's Init — its
// watchers are channel listeners that must run exactly once.
func (m *uiModel) ensureEditor() tea.Cmd {
	if m.ed != nil {
		return nil
	}
	var args []string
	if m.workDir != "" {
		args = append(args, m.workDir)
	}
	ed := editor.New(args...)
	ed.Embed = true
	ed.Chat = true
	ed.ApplyTerminalCompat()
	// The panels start closed: the first screen is the chat, the panels are
	// one key or one status-bar icon away.
	ed.ClosePanels()
	if m.winW > 0 {
		nm, _ := ed.Update(tea.WindowSizeMsg{Width: m.winW, Height: m.winH})
		if em, ok := nm.(editor.Model); ok {
			ed = em
		}
	}
	ed.Host = &editor.Host{
		Key: func(msg tea.KeyPressMsg) tea.Cmd {
			m.hostCall = true
			defer func() { m.hostCall = false }()
			_, cmd := m.Update(msg)
			return cmd
		},
		Click: func(msg tea.MouseClickMsg) tea.Cmd {
			x0, y0, _, _ := m.ed.MainArea()
			msg.X, msg.Y = msg.X-x0, msg.Y-y0
			return m.chatClick(msg)
		},
		Wheel: func(msg tea.MouseWheelMsg) tea.Cmd {
			return m.chatWheel(msg)
		},
		Motion: func(msg tea.MouseMotionMsg) tea.Cmd {
			x0, y0, _, _ := m.ed.MainArea()
			msg.X, msg.Y = msg.X-x0, msg.Y-y0
			return m.chatMotion(msg)
		},
		Release: func(msg tea.MouseReleaseMsg) tea.Cmd {
			x0, y0, _, _ := m.ed.MainArea()
			msg.X, msg.Y = msg.X-x0, msg.Y-y0
			return m.chatRelease(msg)
		},
		Paste: func(text string) tea.Cmd {
			if m.pasteTarget() != nil {
				m.pasteInto(text)
				return nil
			}
			return m.pasteImageCmd()
		},
		View:         m.chatFrame,
		ChangedFiles: func() []string { return tools.ChangedFiles() },
	}
	m.ed = &ed
	return ed.Init()
}

// chatClick starts a transcript selection drag with the button that went down.
// Right and middle are left to the terminal so a user who wants the terminal's
// own paste menu keeps it.
func (m *uiModel) chatClick(msg tea.MouseClickMsg) tea.Cmd {
	if !m.mouseEnabled {
		return nil
	}
	if tea.Mouse(msg).Button == tea.MouseLeft {
		m.selAnchorX, m.selAnchorY = msg.X, msg.Y
		m.selFocusX, m.selFocusY = msg.X, msg.Y
		m.selDown, m.selMoved = true, false
	}
	return nil
}

func (m *uiModel) chatMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if !m.mouseEnabled || !m.selDown {
		return nil
	}
	m.selFocusX, m.selFocusY = msg.X, msg.Y
	if msg.X != m.selAnchorX || msg.Y != m.selAnchorY {
		m.selMoved = true
	}
	return nil
}

func (m *uiModel) chatRelease(msg tea.MouseReleaseMsg) tea.Cmd {
	if !m.mouseEnabled || !m.selDown {
		return nil
	}
	m.selDown = false
	if !m.selMoved {
		// A click, not a drag: clear the highlight and leave the clipboard be.
		m.selMoved = false
		return nil
	}
	m.selFocusX, m.selFocusY = msg.X, msg.Y
	text := m.selectedText()
	m.selMoved = false
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if err := writeClipboardText(text); err != nil {
		m.statusText = i18n.T("clipboard error: ") + err.Error()
		return nil
	}
	// The character count, because a selection can be a single word or three
	// screens of build output and "copied" alone does not say which happened.
	m.statusText = fmt.Sprintf(i18n.T("selection copied (%d characters)"), len([]rune(text)))
	return nil
}

// chatWheel scrolls the transcript. The wheel is routed through the viewport's
// own handler, which already knows the shift-modifier horizontal case; what it
// cannot know is that scrolling up must release the follow-the-tail stick.
func (m *uiModel) chatWheel(msg tea.MouseWheelMsg) tea.Cmd {
	if !m.mouseEnabled {
		return nil
	}
	switch msg.Button {
	case tea.MouseWheelDown:
		m.scrollBy(m.vp.MouseWheelDelta)
	case tea.MouseWheelUp:
		m.scrollBy(-m.vp.MouseWheelDelta)
	default:
		m.vp, _ = m.vp.Update(msg)
		m.syncVP()
	}
	return nil
}

// chatFrame renders the chat inside the workspace main area. While the editor
// exists, the model's width and height are the main area's size — the frame
// callback is the only place they are set, straight from the editor geometry.
func (m *uiModel) chatFrame(w, h int) []string {
	if m.width != w || m.height != h {
		m.width, m.height = w, h
		m.layout()
	}
	rows := strings.Split(m.buildFrame(), "\n")
	for len(rows) < h {
		rows = append(rows, "")
	}
	if len(rows) > h {
		rows = rows[:h]
	}
	return rows
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
		{name: "editor", desc: i18n.T("open the code editor: tree, git, terminal (ctrl+e)"), run: func(m *uiModel) tea.Cmd {
			return m.openEditor()
		}},
		{name: "mode", desc: i18n.T("switch plan/act mode (tab)"), run: func(m *uiModel) tea.Cmd {
			return m.toggleMode()
		}},
		{name: "cd", desc: i18n.T("change the working folder"), run: func(m *uiModel) tea.Cmd {
			return m.changeDir("")
		}},
		{name: "image", desc: i18n.T("attach a picture to the next message (/image <path>)"), run: func(m *uiModel) tea.Cmd {
			return m.attachImageCmd("")
		}},
		{name: "unimage", desc: i18n.T("drop the last attached picture"), run: func(m *uiModel) tea.Cmd {
			m.dropPendingImage()
			return nil
		}},
		{name: "mouse", desc: i18n.T("toggle mouse wheel scrolling"), run: func(m *uiModel) tea.Cmd {
			m.mouseEnabled = !m.mouseEnabled
			return nil
		}},
		{name: "new", desc: i18n.T("start a new session, the old one is kept (ctrl+n)"), run: func(m *uiModel) tea.Cmd {
			m.newSession("")
			return nil
		}},
		{name: "sessions", desc: i18n.T("switch between saved sessions"), run: func(m *uiModel) tea.Cmd {
			return m.openSessions()
		}},
		{name: "resume", desc: i18n.T("open a session by id (/resume <id>)"), run: func(m *uiModel) tea.Cmd {
			// The palette runs a command with no argument, and this one is
			// meaningless without an id — so it says how to use itself rather
			// than opening a prompt the palette would immediately lose.
			m.statusText = i18n.T("usage: /resume <id> — /sessions lists the ids")
			return nil
		}},
		{name: "rewind", desc: i18n.T("undo the last message (ctrl+z)"), run: func(m *uiModel) tea.Cmd {
			m.rewind()
			return nil
		}},
		{name: "clear", desc: i18n.T("clear the screen (ctrl+l)"), run: func(m *uiModel) tea.Cmd {
			m.clearScreen()
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
				line{kindSys, i18n.T("ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply · ctrl+l — clear · ctrl+n — new session")},
				line{kindSys, i18n.T("esc — stop the current turn · up/down — prompt history · pgup/pgdown — scroll")},
				line{kindSys, i18n.T("mouse — select and copy text right in the terminal")},
				line{kindSys, i18n.T("proxy: /proxy opens a dialog · /proxy <url> sets it directly · /proxy off stops it")},
				line{kindSys, i18n.T("image: /image <path> attaches a picture · a dropped path is taken from the prompt · ctrl+v pastes one from the clipboard · /unimage drops the last")},
				line{kindSys, "/setup, /models, /model <id>, /history, /copy, /sidebar, /lang, /new [name], /sessions, /rewind, /clear, /quit"})
			m.historyDirty = true
			return nil
		}},
		{name: "todo", desc: i18n.T("show the current plan"), run: func(m *uiModel) tea.Cmd {
			m.showPlan()
			return nil
		}},
		{name: "tools", desc: i18n.T("list the available tools"), run: func(m *uiModel) tea.Cmd {
			for _, t := range m.toolNames {
				m.history = append(m.history, line{kindSys, "· " + t})
			}
			m.historyDirty = true
			return nil
		}},
		{name: "proxy", desc: i18n.T("set up the HTTP proxy (a dialog: on, off, bypass)"), run: func(m *uiModel) tea.Cmd {
			return m.openProxy()
		}},
		{name: "quit", desc: i18n.T("quit"), run: func(m *uiModel) tea.Cmd { return m.quitCmd() }},
	}
}

const logo = `  ███████╗ ███╗   ███╗ ██████╗ ██████╗ ███████╗ ██████╗
  ██╔═══██║████╗ ████║██╔════╝██╔═══██╗██╔═══██╗██╔═══╝
  ██║   ██║██╔████╔██║██║     ██║   ██║██║   ██║█████╗
  ██║   ██║██║╚██╔╝██║██║     ██║   ██║██║   ██║██╔══╝
  ███████╔╝██║ ╚═╝ ██║╚██████╗╚██████╔╝███████╔╝██████╗
  ╚═════╝  ╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝`

var styleLogo = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)

// styleVersion is the version line under the brand: the brand's own color
// without the weight, so it reads as a caption under the name, not as a
// second name.
var styleVersion = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))

// miniLogo is the startup art redrawn for the sidebar: the full art is 55
// columns and folds into mush at half size — the outlines fill in and the
// letters merge — so the panel carries its own small rendition in the same
// solid-block style. Only ▀▄█ are used: the one-cell width of these three is
// the one thing every terminal agrees on, and a logo whose width depends on
// the terminal is not a logo.
const miniLogo = `█▀█ ▄▄ ▄▄ ▄▄▄ ▄▄▄ ▄▄█ ▄▄▄
█ ▐ █ █ █ █   █ █ █ █ █▄
█▄█ █ █ █ █▄▄ █▄█ █▄█ █▄▄`

func newSessionID() string {
	return fmt.Sprintf("sess-%d", rand.Int63())
}

// InitialModel builds the session model.
//
// yolo is variadic so a caller that has no yolo instrument set — every test but
// the one that exercises yolo, and any headless build — keeps the old call
// working. A session with none of it simply cannot enter the mode, and
// toggleYolo says so rather than showing a badge that promises a behaviour it
// cannot deliver.
func InitialModel(r *runner.Runner, svc session.Service, p config.Provider, tools []tool.Tool, readOnly []tool.Tool, toolNames []string, yolo ...[]tool.Tool) *uiModel {
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
		// Act by default, and the mode shift+tab returns to when yolo is left.
		// Starting in yolo would be a session the user never asked for.
		beforeYolo: modeAct,
	}
	if len(yolo) > 0 {
		m.yoloTools = yolo[0]
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
		line{kindSys, i18n.T("ctrl+e editor · ctrl+p commands · ctrl+b panel · ctrl+y copy · up/down history · esc stop")},
		line{kindSys, ""})
}

func (m *uiModel) Init() tea.Cmd {
	// The workspace exists from the first frame — the terminal, the tree and
	// the git panel are one key (or one status-bar icon) away — but every
	// panel starts closed: the first screen is the chat, not furniture.
	return tea.Batch(textinput.Blink, m.spin.Tick, tea.RequestWindowSize, m.toolCheckCmd(), m.ensureEditor())
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
	if err := writeClipboardText(text); err != nil {
		m.history = append(m.history, line{kindErr, i18n.T("clipboard error: ") + err.Error()})
	} else {
		m.statusText = i18n.T("reply copied to the clipboard!")
		m.history = append(m.history, line{kindSys, i18n.T("📋 the last reply was copied to the clipboard")})
	}
	m.historyDirty = true
	m.followVP()
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// The workspace editor owns keys, mouse and geometry for as long as it
	// runs; in chat mode (ed.Chat) it passes the main-area keys and clicks
	// back through the Host callbacks below. The rest of the traffic — agent
	// events, ticks, transport messages — keeps flowing through the chat
	// below, so a running turn is not stalled while the user is reading code.
	if m.ed != nil && !m.hostCall {
		switch msg := msg.(type) {
		case editor.CloseEditorMsg:
			// ctrl+q in the editor: back to the chat, and the panels fold —
			// closed is the chat mode's default.
			m.ed.Chat = true
			m.ed.DropPanelFocus()
			m.ed.ClosePanels()
			return m, nil
		case editor.ToggleEditorMsg:
			// The status-bar icon at the head of the strip. The mode is the
			// host's to flip — the editor only asks — and it reads the current
			// one off Chat, so the same message serves both directions.
			if m.ed.Chat {
				init := m.ensureEditor()
				m.enterEditor()
				return m, init
			}
			m.ed.Chat = true
			m.ed.DropPanelFocus()
			m.ed.ClosePanels()
			return m, nil
		case tea.WindowSizeMsg:
			// The editor owns the geometry in embedded mode; the chat is
			// re-laid-out by its frame callback at the main area's size.
			m.winW, m.winH = msg.Width, msg.Height
			return m.editorUpdate(msg)
		case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
			return m.editorUpdate(msg)
		}
		// The editor's lifecycle traffic — terminal output, file watches, LSP
		// diagnostics, git transfers — must reach it in chat mode too, or the
		// listener chains die and the panels freeze. The chat's own switch
		// below ignores what it does not know.
		if editor.OwnsMsg(msg) {
			_, edCmd := m.editorUpdate(msg)
			if edCmd != nil {
				return m, edCmd
			}
		}
	}
	// extra carries a follow-up command a case wants to run after the switch,
	// batched with the textinput's own command at the tail.
	var extra tea.Cmd
	switch msg := msg.(type) {
	case tea.ColorProfileMsg:
		// Recorded because an image preview is drawn in whatever the terminal can
		// actually show: truecolor escapes sent to a 16-colour terminal come out
		// as a field of wrong-coloured blocks, and the ramp is the fallback that
		// still carries the picture.
		m.profile = msg.Profile
		// The renderer has already adopted the reported profile; all that is left
		// is to ask for the finer capabilities when this one is not enough.
		return m, upgradeColorProfile(msg.Profile)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.winW, m.winH = msg.Width, msg.Height
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
		// This — and not the ctrl+v key — is where a pasted *picture* arrives in
		// any terminal that binds ctrl+v itself, which is most of them: the key
		// never reaches the program, the terminal converts it, and a screenshot
		// on the clipboard comes out as this message with nothing in it or with
		// the filename of a file that was copied rather than captured. Asking
		// the clipboard here is what makes the paste work at all on those.
		//
		// No return: the text is inserted by the textinput at the tail of Update,
		// and a clipboard holding both would otherwise lose the text.
		if cmd := m.pasteImageCmd(); cmd != nil {
			extra = cmd
		}
	case tea.MouseClickMsg:
		return m, m.chatClick(msg)
	case tea.MouseMotionMsg:
		return m, m.chatMotion(msg)
	case tea.MouseReleaseMsg:
		return m, m.chatRelease(msg)
	case tea.MouseWheelMsg:
		return m, m.chatWheel(msg)
	case tea.KeyPressMsg:
		if m.setup.open {
			model, cmd := m.setupKey(msg)
			return model, cmd
		}
		if m.proxy.open {
			model, cmd := m.proxyKey(msg)
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
		if m.sessionsList.open {
			model, cmd := m.sessionsKey(msg)
			return model, cmd
		}
		if m.ask.open {
			model, cmd := m.askKey(msg)
			return model, cmd
		}
		switch msg.String() {
		case "ctrl+v":
			// ctrl+v is not a paste inside a raw-mode TUI; the terminal sends the
			// control character and nothing else. Read the clipboard directly, so
			// the shortcut works wherever bracketed paste is unavailable.
			//
			// The picture is asked for first and the text second, in one command:
			// a clipboard holding a screenshot has no text to give, and one holding
			// a command line has no picture, so the two never really compete.
			if m.pasteTarget() != nil {
				text, err := readClipboardText()
				if err != nil {
					m.statusText = i18n.T("clipboard error: ") + err.Error()
					return m, nil
				}
				m.pasteInto(text)
				return m, nil
			}
			return m, m.ctrlPasteCmd()
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
			return m, m.quitCmd()
		case "ctrl+p":
			m.palette = paletteState{open: true}
			return m, nil
		case "ctrl+b":
			m.showSidebar = !m.showSidebar
			m.layout()
			m.followVP()
			return m, nil
		case "ctrl+e":
			return m, m.openEditor()
		case "ctrl+y":
			m.copyLastResponse()
			return m, nil
		case "ctrl+z":
			m.rewind()
			return m, nil
		case "ctrl+l":
			// The terminal's own clear-screen, bound to the same thing: an
			// empty prompt asking to be emptied is not a special case of
			// /clear, it is the same request.
			m.clearScreen()
			return m, nil
		case "ctrl+n":
			// Refused mid-turn for the same reason /new is: the runner is
			// mid-conversation on this session id, and pointing it at a new
			// one underneath would strand the turn.
			if m.busy {
				return m, nil
			}
			m.newSession("")
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
		case "shift+tab":
			// The only way into yolo, and the only way out. It is not on the
			// plan/act pair because it is not another way to work through a
			// request — it is a different contract about interruptions, and a
			// contract the user has to be able to see and reverse at a keystroke.
			return m, m.toggleYolo()
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
			// /unimage is a command about an attachment, and the prompt it is most
			// often wanted in already holds a dropped path — typing it there appends
			// to the path, so a whole-line match never fires. Worse than a no-op: the
			// line is no longer a command, so it goes to the model as a question about
			// a file called "/unimage", carrying the picture that was to be removed.
			//
			// It is taken out of the prompt before anything else runs, so what is left
			// in the input is what the user was actually saying.
			if rest, ok := cutCommandToken(text, "/unimage"); ok {
				m.input.SetValue(rest)
				m.dropPendingImage()
				return m, nil
			}
			switch text {
			case "/quit", "/exit":
				return m, m.quitCmd()
			case "/help":
				m.history = append(m.history,
					line{kindSys, i18n.T("ctrl+p — commands · ctrl+b — panel · ctrl+y — copy reply · ctrl+l — clear · ctrl+n — new session")},
					line{kindSys, i18n.T("esc — stop the current turn · up/down — prompt history · pgup/pgdown — scroll")},
					line{kindSys, i18n.T("tab — plan/act mode · wheel — scroll · /mouse — toggle the wheel")},
					line{kindSys, i18n.T("ctrl+e or the ▣ icon — the editor; inside it F1 — editor keys, ctrl+e or ctrl+q — back here")},
					line{kindSys, i18n.T("proxy: /proxy opens a dialog · /proxy <url> sets it directly · /proxy off stops it")},
					line{kindSys, i18n.T("image: /image <path> attaches a picture · a dropped path is taken from the prompt · ctrl+v pastes one from the clipboard · /unimage drops the last")},
					line{kindSys, "/setup, /models, /model <id>, /history, /copy, /editor, /sidebar, /mode, /cd <path>, /new [name], /sessions, /resume <id>, /rewind, /quit"})
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
				m.clearScreen()
				return m, nil
			case "/history":
				m.showRecentPrompts()
				m.followVP()
				return m, nil
			case "/proxy":
				return m, m.openProxy()
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
			// The commands below take an argument or are matched by prefix, so
			// they are tested outside the switch: a case here would have to
			// enumerate every spelling, and "/new" would swallow "/newer" as
			// itself.
			//
			// The old session is kept rather than wiped: /sessions brings it
			// back, so a /new pressed by mistake costs one keypress, not a
			// conversation.
			if arg, ok := strings.CutPrefix(text, "/new"); ok {
				if m.busy {
					return m, nil
				}
				m.newSession(strings.TrimSpace(arg))
				return m, nil
			}
			if arg, ok := strings.CutPrefix(text, "/todo"); ok {
				// "todo clear" empties the list; anything after the word is a
				// subcommand this build does not have, and saying so beats
				// silently printing the plan.
				if strings.TrimSpace(arg) == "clear" {
					todo.Default.Clear()
					m.statusText = i18n.T("the plan is empty")
					m.history = append(m.history, line{kindSys, i18n.T("the plan is empty")})
					m.historyDirty = true
					m.followVP()
					return m, nil
				}
				if rest := strings.TrimSpace(arg); rest != "" {
					m.statusText = i18n.T("usage: /todo, /todo clear")
					return m, nil
				}
				m.showPlan()
				return m, nil
			}
			if _, ok := strings.CutPrefix(text, "/rewind"); ok {
				if m.busy {
					return m, nil
				}
				m.rewind()
				return m, nil
			}
			if arg, ok := strings.CutPrefix(text, "/resume"); ok {
				if m.busy {
					return m, nil
				}
				m.switchSession(strings.TrimSpace(arg))
				return m, nil
			}
			if _, ok := strings.CutPrefix(text, "/sessions"); ok {
				if m.busy {
					return m, nil
				}
				return m, m.openSessions()
			}
			if arg, ok := strings.CutPrefix(text, "/mode"); ok {
				if m.busy {
					return m, nil
				}
				return m, m.setMode(strings.TrimPrefix(strings.TrimSpace(arg), " "))
			}
			// /proxy keeps the typed fast paths (a URL, "off", "no <list>") working
			// beside the dialog, so a user who knows the syntax is not forced through
			// a menu. The prefix has to be cut here: handing the whole line to
			// applyProxy would try to parse "/proxy" as part of the address.
			if arg, ok := strings.CutPrefix(text, "/proxy"); ok {
				arg = strings.TrimSpace(arg)
				if arg == "" {
					return m, m.openProxy()
				}
				return m, m.applyProxy(arg)
			}
			if arg, ok := strings.CutPrefix(text, "/cd"); ok {
				if m.busy {
					return m, nil
				}
				return m, m.changeDir(strings.TrimSpace(arg))
			}
			if arg, ok := strings.CutPrefix(text, "/image"); ok {
				// An attach does not start a turn, so this one is allowed while
				// busy: a user who has just been shown a failure mid-turn is
				// exactly the user who wants to try a different picture next.
				return m, m.attachImageCmd(arg)
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
			// A picture dropped into the prompt as a path is taken out of the text
			// and attached, so what the model reads is the question without a
			// filename in it. A path that is not an image stays in the prompt:
			// silently swallowing it would turn a typo into a turn about nothing.
			text, imgs := m.takeImagePaths(text)
			imgs = append(imgs, m.pendingImages()...)
			m.pending = nil
			if strings.TrimSpace(text) == "" && len(imgs) == 0 {
				// Nothing to send: either the prompt was empty, or every token in
				// it was a path that failed to load — and that reason is already
				// on screen.
				return m, nil
			}
			// Every picture the turn carries is written to the transcript here, and
			// only here. A pending one was already drawn in the strip above the
			// input; drawing it again would put two copies of the same picture on
			// screen, and dropping the attachment would remove one and leave the
			// other. This is the moment it joins the conversation.
			m.showPreviews(imgs)
			// The mark goes in before the previews, so its idx points at the first
			// of them and a rewind truncates the whole turn away together: the
			// pictures, the prompt that sent them, and the reply. Were the mark
			// recorded after the previews they would survive the cut and stay on
			// screen describing a turn that no longer exists.
			m.promptMarks = append(m.promptMarks,
				promptMark{text: text, idx: len(m.history), images: imgs})
			m.history = append(m.history, line{kindUser, text})
			m.historyDirty = true
			m.busy = true
			// The pictures have left the pending strip for the transcript, so the
			// rows it was holding go back before the viewport is re-synced against
			// them.
			m.layout()
			m.followVP()
			m.savePrompt(text)
			return m, m.startTurn(text, imgs)
		}
	case imageAttachedMsg:
		if msg.err != nil {
			// Not reported here: a path the user is midway through typing will
			// fail to open and then succeed a moment later, and an error line for
			// every intermediate state is noise. The status line already carries
			// the last failure.
			return m, nil
		}
		if msg.fromInput && msg.path != m.watchedPath {
			// The prompt moved on while this file was being read. Attaching now
			// would put a preview under a path the user has already deleted, and
			// the next keystroke would then delete it again — a flicker of a
			// picture they took back.
			return m, nil
		}
		if msg.fromInput {
			m.addPendingFrom(msg.a, true, msg.path)
			return m, nil
		}
		m.addPending(msg.a, false)
		return m, nil
	case imageErrMsg:
		m.reportImage(i18n.T("clipboard"), msg.err)
		return m, nil
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
	case ggufReadyMsg:
		cmd := m.handleGGUFReady(msg)
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
	case askRequestMsg:
		return m, m.openAsk(msg)
	case askTickMsg:
		return m, m.handleAskTick()
	case subAgentMsg:
		m.handleSubAgent(msg.ev)
	case retryMsg:
		// The wait is announced rather than swallowed: a silent pause after an
		// error reads as a hang, and a user who cannot tell a retry from a
		// freeze stops trusting the spinner.
		m.statusText = fmt.Sprintf(i18n.T("retrying in %s — %s"),
			formatWait(msg.ev.Wait), msg.ev.Reason)
		m.history = append(m.history, line{kindSys, fmt.Sprintf(
			"↻ %s %d/%d %s %s",
			i18n.T("attempt"),
			msg.ev.Attempt, msg.ev.Max,
			i18n.T("in"), formatWait(msg.ev.Wait),
		)})
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
		// A session file that could not be written is reported here, at the end
		// of the turn that hit it, rather than from inside the write.
		m.reportStoreProblem()
		// A mode switch the agent asked for lands here and nowhere else: this is
		// the one point where the runner is not mid-turn, so the agent can be
		// rebuilt around a different instrument set without stranding a call.
		if cmd := m.applyPendingMode(); cmd != nil {
			return m, cmd
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
	case proxyCheckMsg:
		m.handleProxyCheck(msg)
	case failoverMsg:
		extra = m.handleFailover(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateSuggest()
	// After the input's own update, so the candidate is the text the user has
	// actually typed — and before followVP, so a strip appearing in the same
	// frame is measured against a viewport that has already given up its rows.
	watch := m.watchInputImage()
	m.followVP()
	return m, tea.Batch(cmd, extra, watch)
}

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
// the reply they are answering, not scroll a pane that swallowed it.
const suggestMaxRows = 8

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
	m.vp.SetContent(m.renderHistory() + m.progressTail())
}

// progressTail is the progress note as part of the transcript: the rows that
// go right under the last message while a turn runs, and nothing when it does
// not. Living in the content rather than in a row of the frame is the point —
// the note scrolls with the conversation it describes and leaves no gap behind
// when the turn ends.
func (m *uiModel) progressTail() string {
	if !m.busy {
		return ""
	}
	// Wrapped rather than cut: a tool with a long name stays readable, and a
	// note of two rows still scrolls away with everything else. The wrap width
	// and the indent match renderHistory, so the note lines up with the
	// transcript it closes.
	var rows []string
	for _, r := range wrapIndent(m.progressNote(), m.contentWidth()-chatIndent, "", "") {
		rows = append(rows, strings.Repeat(" ", chatIndent)+styleHint.Render(r))
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n")
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
// The input text the current command list was built from. It is what tells a
// keystroke apart from the rest of the program's chatter: the text input emits a
// cursor blink roughly twice a second, and every one of those messages reaches
// the rebuild below. Without the memo the highlight was thrown away twice a
// second, so a single press of Down appeared to snap the cursor back to the top.

func (m *uiModel) updateSuggest() {
	m.rebuildSuggest(m.input.Value())
}

// watchInputImage looks at the input for a picture path and attaches it, so the
// preview appears while the user is still writing the prompt rather than only
// after they press enter.
//
// It runs on every keystroke, so the guard is cheap on purpose: a path is only
// read once its extension says it could be an image, and only a change of that
// candidate triggers anything. Decoding on every keypress would stall on each
// character of a path still being typed.
//
// A change also *un-attaches* the previous candidate. A path in the prompt is a
// statement the user is still editing: delete the path and the picture must go
// with it, or the strip goes on showing something they have taken back.
func (m *uiModel) watchInputImage() tea.Cmd {
	cand := ""
	for _, tok := range splitTokens(m.input.Value()) {
		if looksLikeImage(tok.text) {
			cand = tok.text
			break
		}
	}
	if cand == m.watchedPath {
		return nil
	}
	m.watchedPath = cand

	dropped := m.dropInputPending()
	// Only re-laid-out when something actually went: a keystroke that merely
	// changed the path candidate and read nothing should not resize the viewport
	// for no reason.
	if dropped {
		m.layout()
		m.followVP()
	}
	if cand == "" {
		return nil
	}
	return m.watchImageCmd(cand)
}

// rebuildSuggest rebuilds the command list for text, keeping the highlighted row
// when the list it was picked from is the same one. Typing narrows the list, and
// a selection that pointed past the new end has to go — but nothing else may
// move it.
func (m *uiModel) rebuildSuggest(text string) {
	keepSel := text == m.suggestFor
	m.suggest = nil
	if !keepSel {
		m.suggestSel = 0
	}
	m.suggestFor = text

	if m.palette.open || m.picker.open || m.setup.open || m.proxy.open {
		return
	}
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
		m.clampSuggest()
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
	m.clampSuggest()
}

// clampSuggest pulls the highlighted row back inside the list. Typing narrows the
// list, so a selection that survived a rebuild can point past its new end, and an
// index out of range would either panic or silently select the wrong command.
func (m *uiModel) clampSuggest() {
	if m.suggestSel >= len(m.suggest) {
		m.suggestSel = max(len(m.suggest)-1, 0)
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
	// verbatim marks pre-formatted art (the logo, an image preview), which keeps
	// its own line breaks: re-wrapping box-drawing runes would scramble the
	// picture.
	verbatim bool
	// markdown marks text the model produced, which is rendered as markdown
	// rather than as literal prose.
	markdown bool
	// prestyled marks art that carries its own colour. The transcript wraps every
	// other row in row.style, which would put the panel's foreground in front of
	// the picture's own and reset it at the end of the row — a preview of a
	// screenshot comes out tinted and half its colours replaced by the default.
	prestyled bool
	// captionLast marks a block whose final row is a caption rather than part of
	// the picture, so a block too wide to draw can fall back to that row instead of
	// disappearing. The logo has no such row and so keeps being dropped whole.
	captionLast bool
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
	case kindImage:
		// An empty style on purpose, paired with prestyled: the preview brings its
		// own colours, so there is nothing to add and something to break.
		return transcriptRow{
			style:       lipgloss.NewStyle(),
			verbatim:    true,
			prestyled:   true,
			captionLast: true,
		}
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
			// An image preview whose last row is its caption falls back to the
			// caption: the picture is too wide to draw honestly, but "a picture was
			// sent, this one" is still worth saying. Dropping the whole block
			// would leave a turn sent as nothing but a screenshot with nothing at
			// all on screen to say so.
			if row.captionLast && len(rows) > 0 {
				last := rows[len(rows)-1]
				if ansi.StringWidth(last) <= width {
					return []string{last}
				}
			}
			return nil
		}
	}
	return rows
}

// chatIndent is the small left margin every transcript row is drawn with: a
// line of text sitting flush against the panel's border reads as cramped, and
// a couple of border columns of air between them is what a page margin is
// for. The wrap width shrinks by the same amount, so nothing gains a row.
const chatIndent = 2

func (m *uiModel) renderHistory() string {
	w := m.contentWidth() - chatIndent
	if !m.historyDirty && m.cachedWidth == w && m.cachedHistory != "" {
		return m.cachedHistory
	}
	// A replacement in applyAgentText can shrink the slice; the cache must not
	// keep entries for lines that no longer exist.
	if len(m.cachedRows) > len(m.history) {
		m.cachedRows = m.cachedRows[:len(m.history)]
	}
	var b strings.Builder
	for i := range m.history {
		l := &m.history[i]
		row := m.rowStyle(l.kind)
		var rows []string
		hit := false
		if i < len(m.cachedRows) {
			c := &m.cachedRows[i]
			if c.kind == l.kind && c.text == l.text && c.width == w {
				rows, hit = c.rows, true
			}
		}
		if !hit {
			for _, r := range rowRows(l.text, w, row) {
				// A markdown row arrives already styled by the renderer, and a
				// prestyled one already carries its own colours; wrapping either
				// again would nest the escapes and break the width accounting.
				if row.markdown || row.prestyled {
					rows = append(rows, r)
					continue
				}
				rows = append(rows, row.style.Render(r))
			}
			entry := cachedLine{kind: l.kind, text: l.text, width: w, rows: rows}
			if i < len(m.cachedRows) {
				m.cachedRows[i] = entry
			} else if i == len(m.cachedRows) {
				m.cachedRows = append(m.cachedRows, entry)
			} else {
				for len(m.cachedRows) < i {
					m.cachedRows = append(m.cachedRows, cachedLine{})
				}
				m.cachedRows = append(m.cachedRows, entry)
			}
		}
		// An image preview brings its own left margin, painted in the picture's
		// background: adding chatIndent on top of it would put a stripe of the
		// terminal's own background down the side of the picture, which reads as a
		// column of the image that is out of step with the rest.
		margin := strings.Repeat(" ", chatIndent)
		for _, r := range rows {
			if row.prestyled {
				b.WriteString(r + "\n")
				continue
			}
			b.WriteString(margin + r + "\n")
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
// showProxy reports the proxy in effect and, when a provider has failed through
// it, says so — a proxy that is silently dropping every request looks exactly
// like a provider that is down, and the two have opposite fixes.
//
// It is a report rather than a prompt because the wizard already has a way to ask
// for a value, and reusing it here would mean /proxy behaved differently from
// every other command that needs input.
// applyProxy handles "/proxy" with or without an argument.
//
// With no argument it reports the current setting. With one it sets the proxy,
// and the change is written to .env as well as the live environment, because a
// proxy that survives only until the program exits is not really configured: the
// next start would go direct and the failure would look like the provider's.
//
// The set-then-check order matters. A malformed URL is rejected before anything
// moves, so a typo cannot leave the session half configured, and the request
// that follows is what turns "the proxy is set" into "the proxy works".
func (m *uiModel) applyProxy(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return m.showProxy()
	}

	fail := func(err error) tea.Cmd {
		m.history = append(m.history, line{kindErr, "proxy: " + err.Error()})
		m.historyDirty = true
		m.followVP()
		return nil
	}

	// "no" sets the bypass list rather than the proxy, so a user with a proxy
	// already in the environment can exempt their own services without turning
	// the proxy off entirely.
	if rest, ok := strings.CutPrefix(arg, "no "); ok {
		if err := config.SetNoProxy(strings.TrimSpace(rest)); err != nil {
			return fail(err)
		}
	} else if err := config.SetProxy(arg); err != nil {
		return fail(err)
	}

	s := config.CurrentProxy()
	if err := m.persistProxy(s); err != nil {
		return fail(err)
	}
	if s.Active {
		m.history = append(m.history, line{kindSys, "proxy: " + s.HTTPS})
	} else {
		m.history = append(m.history, line{kindSys, i18n.T("proxy: cleared, connecting directly")})
	}
	m.historyDirty = true
	m.followVP()
	return m.showProxy()
}

// persistProxy brings .env in line with the environment.
//
// MergeDotEnv only adds and replaces, so a proxy that has been turned off has to
// be removed from the file explicitly: left there it would be read back on the
// next start and the session would silently keep going through a proxy the user
// has switched off.
func (m *uiModel) persistProxy(s config.ProxySettings) error {
	lines, err := config.ReadDotEnv()
	if err != nil {
		return err
	}
	// Start from the file as it is, drop every proxy variable, then write the
	// current ones back. In that order, so a variable that no longer has a value
	// cannot survive, which is the whole point of "off".
	merged, _ := config.MergeDotEnv(lines, nil)
	for _, k := range append(config.ProxyEnvKeys(), config.EnvNoProxy) {
		merged = dropEnvKey(merged, k)
	}
	if s.Active {
		vars := map[string]string{}
		if s.HTTP != "" {
			vars[config.EnvHTTPProxy] = s.HTTP
		}
		if s.HTTPS != "" {
			vars[config.EnvHTTPSProxy] = s.HTTPS
		}
		if s.NoProxy != "" {
			vars[config.EnvNoProxy] = s.NoProxy
		}
		merged, _ = config.MergeDotEnv(merged, vars)
	}
	var sb strings.Builder
	for _, l := range merged {
		sb.WriteString(l + "\n")
	}
	return os.WriteFile(".env", []byte(sb.String()), 0o600)
}

// dropEnvKey removes every line assigning key, leaving comments and other
// variables alone.
func dropEnvKey(lines []string, key string) []string {
	out := lines[:0]
	for _, l := range lines {
		if k, _, ok := config.DotEnvPair(l); ok && k == key {
			continue
		}
		out = append(out, l)
	}
	return out
}

// openProxy opens the /proxy dialog. The menu is the entry point a user who
// does not remember the command syntax can still use; the typed forms
// ("/proxy <url>", "/proxy off", "/proxy no <list>") stay beside it.
func (m *uiModel) openProxy() tea.Cmd {
	m.proxy.reset()
	m.proxy.open = true
	m.statusText = "/proxy"
	return nil
}

// proxyKey walks the /proxy dialog. Escape backs out of any stage without
// touching the live setting, and a value that does not validate keeps the
// field on screen with the reason in the status bar — a closed dialog and an
// error line in the transcript would leave the user retyping from scratch.
func (m *uiModel) proxyKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.proxy.reset()
		m.statusText = i18n.T("cancelled")
		return m, nil
	case "ctrl+c":
		return m, m.quitCmd()
	}

	switch m.proxy.stage {
	case proxyPick:
		switch msg.String() {
		case "up":
			if m.proxy.selected > 0 {
				m.proxy.selected--
			}
			return m, nil
		case "down":
			if m.proxy.selected < proxyActionCount-1 {
				m.proxy.selected++
			}
			return m, nil
		case "enter":
			switch m.proxy.selected {
			case 0:
				m.proxy.stage = proxyURL
				m.proxy.buf = ""
			case 1:
				m.proxy.stage = proxyNo
				m.proxy.buf = ""
			case 2:
				m.proxy.reset()
				return m, m.applyProxy("off")
			default:
				m.proxy.reset()
				return m, m.showProxy()
			}
			return m, nil
		}

	case proxyURL:
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(m.proxy.buf)
			if val == "" {
				m.statusText = i18n.T("no proxy address entered")
				return m, nil
			}
			if err := config.ValidateProxy(val); err != nil {
				m.statusText = err.Error()
				return m, nil
			}
			m.proxy.reset()
			return m, m.applyProxy(val)
		case "backspace":
			if r := []rune(m.proxy.buf); len(r) > 0 {
				m.proxy.buf = string(r[:len(r)-1])
			}
			return m, nil
		}

	case proxyNo:
		switch msg.String() {
		case "enter":
			// An empty field is meaningful here: it clears the bypass list.
			val := strings.TrimSpace(m.proxy.buf)
			m.proxy.reset()
			return m, m.applyProxy("no " + val)
		case "backspace":
			if r := []rune(m.proxy.buf); len(r) > 0 {
				m.proxy.buf = string(r[:len(r)-1])
			}
			return m, nil
		}
	}

	if len(msg.Text) > 0 && m.proxy.stage != proxyPick {
		m.proxy.buf += msg.Text
	}
	return m, nil
}

// proxyActionCount is the number of rows the /proxy menu offers. It lives in
// one place because the key handler and the renderer must agree on it.
const proxyActionCount = 4

func (m *uiModel) proxyBox() string {
	inner := m.floatingWidth() - panelBorder

	// The current state rides in the title: a user reopening the dialog should
	// not have to remember whether a proxy is already on, or which one.
	now := i18n.T("direct connection")
	if s := config.CurrentProxy(); s.Active {
		now = s.HTTPS
		if now == "" {
			now = s.HTTP
		}
	}
	title := i18n.T("? proxy — now: ") + truncate(now, max(inner-ansi.StringWidth(i18n.T("? proxy — now: ")), 1))

	switch m.proxy.stage {
	case proxyURL:
		rows := [][]string{
			{styleHint.Render(i18n.T("proxy address, e.g. 127.0.0.1:8080 or socks5://host:1080"))},
			{styleHint.Render(i18n.T("enter — apply, esc — cancel"))},
			{m.proxy.buf + "█"},
		}
		return m.floatingPanel(title, "", rows, 0)

	case proxyNo:
		rows := [][]string{
			{styleHint.Render(i18n.T("hosts that skip the proxy, comma-separated — e.g. localhost,10.0.0.0/8"))},
			{styleHint.Render(i18n.T("an empty field clears the list · enter — apply · esc — cancel"))},
			{m.proxy.buf + "█"},
		}
		return m.floatingPanel(title, "", rows, 0)
	}

	entries := make([][]string, 0, proxyActionCount)
	for i, label := range []string{
		i18n.T("turn the proxy on or change it"),
		i18n.T("edit the bypass list (NO_PROXY)"),
		i18n.T("turn the proxy off"),
		i18n.T("show the current settings and test them"),
	} {
		marker, style := "   ", lipgloss.NewStyle()
		if i == m.proxy.selected {
			marker, style = " ? ", styleTool
		}
		rows := wrapIndent(label, inner, marker, "     ")
		if i != m.proxy.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(title+"  (enter — select, esc — cancel)", "", entries, m.proxy.selected)
}

func (m *uiModel) showProxy() tea.Cmd {
	s := config.CurrentProxy()
	add := func(s string) {
		m.history = append(m.history, line{kindSys, s})
	}
	if !s.Active {
		add(i18n.T("proxy: none (direct connection)"))
	} else {
		// http and https are shown separately because they are separate
		// variables; a user who set one on the command line and not the other
		// would otherwise have no way to see which is which.
		for _, p := range []struct{ label, val string }{
			{"https", s.HTTPS}, {"http", s.HTTP},
		} {
			if p.val != "" {
				add("proxy " + p.label + ": " + p.val)
			}
		}
		if s.NoProxy != "" {
			add("proxy bypass: " + s.NoProxy)
		}
	}
	add(i18n.T("set one with: /proxy <url> · clear with: /proxy off · bypass with: /proxy no <list>"))

	// The probe is what turns "the proxy is set" into "the proxy works", which
	// is the thing a user behind one actually needs to know. A failure here is
	// reported as inconclusive rather than as a dead provider, because the
	// request may never have left the machine.
	m.statusText = i18n.T("checking the proxy…")
	return func() tea.Msg {
		_, err := config.ListModels(m.prov)
		return proxyCheckMsg{err: err}
	}
}

type proxyCheckMsg struct{ err error }

// handleProxyCheck reports whether a request through the current proxy reaches
// the provider. It deliberately does not change the running provider on a
// failure: a proxy problem is a reason to fix the proxy, not to silently switch
// the user onto a different endpoint and hide the reason their setup is broken.
func (m *uiModel) handleProxyCheck(msg proxyCheckMsg) {
	m.statusText = ""
	kind, text := kindSys, i18n.T("proxy: the provider answered through it")
	if msg.err != nil {
		kind, text = kindErr, i18n.T("proxy: the request failed — ")
		text += msg.err.Error()
	}
	m.history = append(m.history, line{kind, text})
	m.historyDirty = true
	m.followVP()
}

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
	// The GGUF browser has no text field either; a paste aimed at it must not
	// silently land in the path prompt underneath.
	case m.setup.open && m.setup.gguf.open:
		return nil
	case m.setup.open && m.setup.stage != setupPick:
		return &m.setup.buf
	case m.proxy.open && m.proxy.stage != proxyPick:
		return &m.proxy.buf
	case m.picker.open:
		return &m.picker.query
	case m.palette.open:
		return &m.palette.query
	case m.sessionsList.open:
		return &m.sessionsList.query
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
	// The file browser owns the keyboard first: its esc must close only the
	// dialog and hand the user back to the path prompt, not tear down /setup.
	if m.setup.gguf.open {
		return m.ggufPickKey(msg)
	}
	switch msg.String() {
	case "esc":
		m.setup.reset()
		m.statusText = i18n.T("setup cancelled")
		return m, nil
	case "ctrl+c":
		return m, m.quitCmd()
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
			case m.setup.opt.GGUF:
				m.setup.stage = setupGGUF
				m.setup.buf = ""
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

	case setupGGUF:
		switch msg.String() {
		case "ctrl+o":
			m.openGGUFPick()
			return m, nil
		case "enter":
			if strings.TrimSpace(m.setup.buf) == "" {
				// An empty path is the common case: the disk is where the models
				// live, so open the browser instead of demanding a typed path.
				m.openGGUFPick()
				return m, nil
			}
			m.setup.opt.GGUFPath = strings.TrimSpace(m.setup.buf)
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

// openGGUFPick brings up the file browser at a sensible starting point: the
// typed path when it points at something real, the process directory otherwise.
func (m *uiModel) openGGUFPick() {
	start, err := os.Getwd()
	if err != nil || start == "" {
		start = "."
	}
	if b := strings.TrimSpace(m.setup.buf); b != "" {
		if st, serr := os.Stat(b); serr == nil {
			if st.IsDir() {
				start = b
			} else {
				start = filepath.Dir(b)
			}
		}
	}
	m.setup.gguf = ggufPickState{open: true}
	m.loadGGUFDir(start)
}

// loadGGUFDir reads dir into the browser. A failed read keeps the old listing
// on screen and reports the reason, so a permission problem does not blank out
// the dialog the user is mid-navigation in.
func (m *uiModel) loadGGUFDir(dir string) {
	entries, err := readGGUFDir(dir)
	if err != nil {
		m.statusText = i18n.T("could not read the directory: ") + err.Error()
		return
	}
	m.setup.gguf.dir = dir
	m.setup.gguf.entries = entries
	m.setup.gguf.selected = 0
}

// readGGUFDir lists what the browser shows: the parent first, then
// subdirectories, then the .gguf files of this directory. Everything else —
// dotfiles, stray weights, READMEs — is noise for this one purpose.
func readGGUFDir(dir string) ([]ggufEntry, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sep := string(filepath.Separator)
	var dirs, files []ggufEntry
	for _, e := range dirEntries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if e.IsDir() {
			dirs = append(dirs, ggufEntry{label: name + sep, path: path, dir: true})
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".gguf") {
			files = append(files, ggufEntry{label: name, path: path})
		}
	}
	slices.SortFunc(dirs, func(a, b ggufEntry) int { return strings.Compare(a.label, b.label) })
	slices.SortFunc(files, func(a, b ggufEntry) int { return strings.Compare(a.label, b.label) })
	var out []ggufEntry
	if parent := filepath.Dir(dir); parent != dir {
		out = append(out, ggufEntry{label: ".." + sep, path: parent, dir: true})
	}
	return append(append(out, dirs...), files...), nil
}

// ggufPickKey drives the file browser. enter descends into a directory or
// picks a .gguf file — the pick flows straight into applySetup, the same path
// a hand-typed path takes. esc backs out to the path prompt with the typed
// text intact.
func (m *uiModel) ggufPickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.setup.gguf
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
		if e.dir {
			m.loadGGUFDir(e.path)
			return m, nil
		}
		p.open = false
		m.setup.opt.GGUFPath = e.path
		m.setup.buf = e.path
		return m, m.applySetup()
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

	// A GGUF file is saved straight away and the server starts in the
	// background: loading a model takes minutes, and the event loop must not
	// wait on it. The provider arrives later, with ggufReadyMsg.
	if opt.GGUF {
		return m.startGGUFSetup(vars)
	}

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

// persistSetup writes the provider to .env and exports it for this process.
// The status it returns is already translated; err is the underlying cause.
func (m *uiModel) persistSetup(vars map[string]string) (string, error) {
	// Merge into any existing .env rather than replacing it, so a key the user
	// set for another provider is not silently dropped.
	lines, err := config.ReadDotEnv()
	if err != nil {
		return i18n.T("error reading .env"), err
	}
	merged, _ := config.MergeDotEnv(lines, vars)

	var sb strings.Builder
	for _, l := range merged {
		sb.WriteString(l + "\n")
	}
	if werr := os.WriteFile(".env", []byte(sb.String()), 0o600); werr != nil {
		return i18n.T("could not write .env"), werr
	}
	for _, k := range config.SortedKeys(vars) {
		os.Setenv(k, vars[k])
	}
	return "", nil
}

// rebuildRunnerFor points the session at p: a new agent on the same session
// service, then the background tool-calling check the new provider deserves.
func (m *uiModel) rebuildRunnerFor(p config.Provider) tea.Cmd {
	ctx := context.Background()
	a, berr := dmagent.BuildAgent(ctx, p, m.tools, m.mcpToolsets...)
	if berr != nil {
		m.history = append(m.history, line{kindErr, "setup: " + berr.Error()})
		m.historyDirty = true
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
		m.historyDirty = true
		return nil
	}
	m.runner = r
	return m.toolCheckCmd()
}

// startGGUFSetup saves the GGUF configuration and kicks off llama-server in
// the background. The provider is not known until the model has loaded, so
// nothing is committed to m.prov here — ggufReadyMsg finishes the job.
func (m *uiModel) startGGUFSetup(vars map[string]string) tea.Cmd {
	status, err := m.persistSetup(vars)
	if err != nil {
		m.statusText = status
		m.history = append(m.history, line{kindErr, "setup: " + err.Error()})
		m.setup.reset()
		m.historyDirty = true
		m.followVP()
		return nil
	}
	m.setup.reset()
	m.statusText = i18n.T("starting the GGUF model — llama-server is loading it…")
	m.history = append(m.history,
		line{kindSys, i18n.T("starting llama-server on ") + vars["DMCODE_GGUF"]},
		line{kindSys, i18n.T("this can take a while — the model loads before the first reply")})
	m.historyDirty = true
	m.followVP()
	return func() tea.Msg {
		p, lerr := launchGGUF()
		return ggufReadyMsg{prov: p, err: lerr}
	}
}

// handleGGUFReady completes the GGUF setup: on success the provider goes live
// without a restart; on failure the saved configuration stays in .env so the
// user can fix the path or the binary and restart.
func (m *uiModel) handleGGUFReady(msg ggufReadyMsg) tea.Cmd {
	if msg.err != nil {
		m.history = append(m.history,
			line{kindErr, i18n.T("llama-server did not start: ") + msg.err.Error()},
			line{kindSys, i18n.T(".env keeps DMCODE_GGUF — fix DMCODE_LLAMA_SERVER or the path, then restart dmcode.")})
		m.historyDirty = true
		m.statusText = i18n.T("failed")
		m.followVP()
		return nil
	}
	m.prov = msg.prov
	m.statusText = i18n.T("provider: ") + msg.prov.Label
	m.history = append(m.history,
		line{kindSys, i18n.T("GGUF model is up: ") + msg.prov.Model + " — " + msg.prov.BaseURL})
	m.historyDirty = true
	m.followVP()
	return m.rebuildRunnerFor(msg.prov)
}

// commitSetup writes the validated provider to .env and rebuilds the agent.
func (m *uiModel) commitSetup(opt config.SetupOption, vars map[string]string) tea.Cmd {
	status, err := m.persistSetup(vars)
	if err != nil {
		m.statusText = status
		m.history = append(m.history, line{kindErr, "setup: " + err.Error()})
		m.setup.reset()
		m.historyDirty = true
		return nil
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

	return m.rebuildRunnerFor(p)
}

// setupLabel names a wizard option for the sidebar, where the provider is shown.
func setupLabel(opt config.SetupOption) string {
	if opt.GGUF {
		return i18n.T("llama.cpp GGUF")
	}
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
		a, err := dmagent.BuildPooledAgent(ctx, pool, ts, m.switchNotifier(), m.retryNotifier(), m.mode.agentMode(), m.mcpToolsets...)
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

// formatWait renders a pause in the coarsest unit that still says something
// useful: "800ms", "3s", "1m 20s". A backoff shown as "1.734s" reads as noise.
func formatWait(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
}

// retryNotifier tells the event loop that an endpoint is about to be asked
// again. It is nil-safe for the same reason switchNotifier is: the program is
// wired in after the agent is built, and nothing can retry before the first
// turn runs.
func (m *uiModel) retryNotifier() func(llm.RetryEvent) {
	prog := m.prog
	return func(ev llm.RetryEvent) {
		if prog == nil {
			return
		}
		prog.Send(retryMsg{ev})
	}
}

// startTurn sends one prompt to the model.
//
// The prompt is built as parts rather than as text because a turn may carry
// pictures: genai.NewContentFromText can only ever hold a string, so a turn with
// an image attached would have had to drop it. With no images the result is the
// same single text part that was always sent, which is what keeps every existing
// conversation byte-identical on the wire.
func (m *uiModel) startTurn(text string, imgs []imgprev.Attachment) tea.Cmd {
	p := m.prog
	r, userID, sessionID := m.runner, "user", m.sessionID
	turnCtx, cancel := context.WithCancel(context.Background())
	m.cancelTurn = cancel
	m.turnCount++
	m.statusText = i18n.T("generating a reply…")
	return func() tea.Msg {
		userMsg := userContent(text, imgs)
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

// userContent assembles the user turn: the text the user typed, then the pictures
// they attached.
//
// Text comes first and the images follow, which is the order the vision APIs
// document. It also decides what a picture-only turn says: the images alone would
// leave the model nothing to tie them to, and the transcript's own caption is
// already stored separately, so this is the only place a bare attachment gets a
// word of context.
func userContent(text string, imgs []imgprev.Attachment) *genai.Content {
	if len(imgs) == 0 {
		return genai.NewContentFromText(text, genai.RoleUser)
	}
	parts := make([]*genai.Part, 0, len(imgs)+1)
	if t := strings.TrimSpace(text); t != "" {
		parts = append(parts, genai.NewPartFromText(t))
	} else if len(imgs) == 1 {
		parts = append(parts, genai.NewPartFromText(
			i18n.T("Here is the image I want you to look at.")))
	} else {
		parts = append(parts, genai.NewPartFromText(
			i18n.T("Here are the images I want you to look at.")))
	}
	for _, a := range imgs {
		parts = append(parts, &genai.Part{InlineData: &genai.Blob{
			Data:     a.Data,
			MIMEType: a.MIME,
			// The file name travels with the blob so a session read back from
			// disk still says which picture a part was, rather than presenting
			// an anonymous blob.
			DisplayName: a.Name,
		}})
	}
	return genai.NewContentFromParts(parts, genai.RoleUser)
}

// mouseCell is one point of a selection, in terminal cells.
type mouseCell struct{ x, y int }

// selectionCells returns the selection in reading order, whatever direction the
// drag went.
//
// Normalising here rather than at capture time is what makes a backwards drag
// copy the text the user highlighted instead of the text between the last point
// and the first in the order the mouse visited it.
func (m *uiModel) selectionCells() (from, to mouseCell, ok bool) {
	if !m.selMoved {
		return from, to, false
	}
	a, b := mouseCell{m.selAnchorX, m.selAnchorY}, mouseCell{m.selFocusX, m.selFocusY}
	if a.y > b.y || (a.y == b.y && a.x > b.x) {
		a, b = b, a
	}
	return a, b, true
}

// selectedText is what the current selection amounts to: the plain text under it,
// with the styling taken out and the trailing padding of each row trimmed.
//
// The frame is the source rather than the history because that is what the user
// pointed at. Reading m.history instead would hand back unwrapped lines — a row
// the user saw as three wrapped lines would arrive as one long one — and would
// include the gutter markers the render owns.
func (m *uiModel) selectedText() string {
	from, to, ok := m.selectionCells()
	if !ok {
		return ""
	}
	rows := strings.Split(m.frame, "\n")
	var out []string
	for y := from.y; y <= to.y && y < len(rows); y++ {
		if y < 0 {
			continue
		}
		row := rows[y]
		width := ansi.StringWidth(row)
		start, end := 0, width
		if y == from.y {
			start = from.x
		}
		if y == to.y {
			end = to.x
		}
		// A row narrower than the pointer — the user dragged past the end of a
		// short line — contributes nothing rather than an error.
		if start > width {
			continue
		}
		if end > width {
			end = width
		}
		if end <= start {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(row, start, end)), " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// selReverseOn and selReverseOff are the SGR pair for reverse video. They are
// applied per escape sequence rather than wrapped around the selected slice,
// because a styled transcript row carries SGR resets of its own and a wrapper
// applied once at the front would be cancelled by the first colour change — the
// selection would visibly stop halfway along a line.
const (
	selReverseOn  = "\x1b[7m"
	selReverseOff = "\x1b[27m"
)

// highlightCells draws the cells [from,to) of a styled row in reverse video.
//
// It walks the string rather than slicing it, because the bytes of a styled row
// are not its cells: a column is not a byte offset, and the escape sequences
// between them belong to the renderer, not to the text.
func highlightCells(row string, from, to int) string {
	if from >= to {
		return row
	}
	var (
		b      strings.Builder
		col    int
		inSel  bool
		opened bool
	)
	flush := func() {
		if opened {
			b.WriteString(selReverseOff)
			opened = false
		}
	}
	for i := 0; i < len(row); {
		// An escape sequence: copy it whole, then re-arm the highlight if we are
		// inside the selection, since a full reset inside the row turns it off.
		if row[i] == 0x1b {
			j := i + 1
			if j < len(row) && row[j] == '[' {
				j++
				for j < len(row) && !isANSIFinal(row[j]) {
					j++
				}
				if j < len(row) {
					j++
				}
			} else if j < len(row) {
				j++
			}
			seq := row[i:j]
			b.WriteString(seq)
			if j <= len(row) && row[j-1] == 'm' && inSel {
				b.WriteString(selReverseOn)
				opened = true
			}
			i = j
			continue
		}
		// A rune: decode it whole and measure it, so a double-width character
		// advances two columns and the selection ends where the eye says it does
		// rather than one cell early.
		r, size := utf8.DecodeRuneInString(row[i:])
		if size == 0 {
			break
		}
		w := ansi.StringWidth(string(r))
		next := col + w
		inside := next > from && col < to
		if inside && !inSel {
			b.WriteString(selReverseOn)
			opened = true
			inSel = true
		} else if !inside && inSel {
			flush()
			inSel = false
		}
		b.WriteString(row[i : i+size])
		col = next
		i += size
	}
	flush()
	return b.String()
}

// isANSIFinal reports whether b ends an escape sequence.
func isANSIFinal(b byte) bool { return b >= 0x40 && b <= 0x7e }

// renderToolResponse renders a tool result for the transcript.
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
		// maxToolRows caps the tool list's share of the panel. MCP can double
		// the count, and a list that takes the panel over leaves the plan and
		// the hotkeys below it trimmed away — the cap is what keeps the order
		// of sections meaning anything on a short terminal.
		maxToolRows = 12
	)

	// flow writes a wrapped multi-row entry: the first row starts with its
	// marker, and every continuation row lines up under the first name rather
	// than under the marker's left edge — a list that resumes one column to
	// the left of where it started reads as broken, which is what the single
	// space used to do.
	flow := func(style lipgloss.Style, marker, text string) []string {
		indent := strings.Repeat(" ", ansi.StringWidth(marker))
		var out []string
		for _, r := range wrapIndent(text, sbInner, marker, indent) {
			out = append(out, style.Render(r))
		}
		return out
	}

	// build renders the whole body. hotkeys is the one collapsible part: the
	// keys are listed by /help, so they are the first thing to go when the
	// terminal is too short for everything.
	build := func(hotkeys bool) string {
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

		// The art leads, at half the size it wears at startup: the same
		// letterforms folded into quadrant glyphs. With the panel open the
		// header is hidden — the sidebar is where the session facts live — so
		// the panel is also where the brand belongs.
		for _, l := range strings.Split(miniLogo, "\n") {
			row(styleLogo, l)
		}
		// The build version right under the name, in the brand's color without
		// the weight: a caption, not a second name. "dev" is shown as it is —
		// "vdev" would be a version of nothing. It is pushed to the panel's
		// right edge, under the tail of the art rather than its head, and cut
		// when a version string outruns the panel.
		v := config.Version
		if v != "dev" {
			v = "v" + v
		}
		v = truncate(v, sbInner)
		row(styleVersion, strings.Repeat(" ", sbInner-ansi.StringWidth(v))+v)
		b.WriteString("\n")

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
		// What the agent actually changed. The counts come from the tools, which are
		// the only place that knows a file's before and after, so this is the real
		// diff of the session rather than a sum of the write calls that produced it.
		// It stays hidden until something has changed: a column of zeroes on a fresh
		// session is noise, and its absence is the information.
		if cs := dmtools.Changes(); cs.Files > 0 {
			row(styleHint, truncate(fmt.Sprintf(i18n.T(" files: %d"), cs.Files), sbInner-1))
			row(styleHint, " "+styleAdd.Render(fmt.Sprintf("+%d ", cs.Added))+styleDel.Render(fmt.Sprintf("-%d", cs.Removed)))
		}
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
		rows := flow(styleHint, " ", strings.Join(m.activeToolNames(), ", "))
		if len(rows) > maxToolRows {
			rows = append(rows[:maxToolRows], " …")
		}
		for _, r := range rows {
			b.WriteString(r + "\n")
		}
		b.WriteString("\n")

		// The plan sits between the tools and the hotkeys: it is state the turn is
		// producing, where the tool list is fixed and the hotkeys never change.
		m.planSidebar(row, value)
		b.WriteString("\n")

		if hotkeys {
			row(styleSidebarLabel, i18n.T("HOTKEYS"))
			row(styleHint, i18n.T(" ctrl+e  editor"))
			row(styleHint, i18n.T(" ctrl+p  commands"))
			row(styleHint, i18n.T(" ctrl+b  hide panel"))
			row(styleHint, i18n.T(" ctrl+y  copy reply"))
			row(styleHint, i18n.T(" ctrl+z  undo last message"))
			row(styleHint, i18n.T(" tab     plan/act"))
			row(styleHint, i18n.T("shift+tab yolo"))
			row(styleHint, i18n.T(" esc     stop turn"))
			row(styleHint, i18n.T(" pgup/dn scroll"))
		}

		// Measuring at sbInner wraps and pads every row to exactly the width the
		// framed box will have available, so the split below counts real rows.
		return lipgloss.NewStyle().Width(sbInner).Render(strings.TrimRight(b.String(), "\n"))
	}

	if height <= 0 {
		return styleSidebar.Width(sidebarBoxWidth).Render(build(true))
	}

	budget := max(height-2*sbEdge, 1)
	lines := strings.Split(build(true), "\n")
	if len(lines) > budget {
		// The full body does not fit. The hotkeys go first — they are the most
		// replaceable rows on the panel — and only what still overflows after
		// that is trimmed.
		lines = strings.Split(build(false), "\n")
		if len(lines) > budget {
			lines = lines[:budget]
			// Mark the cut so a trimmed panel is not read as a complete one.
			lines[budget-1] = styleHint.Render("…")
		}
	}

	return styleSidebar.Width(sidebarBoxWidth).Height(height).Render(strings.Join(lines, "\n"))
}

// progressNote is the live progress text: what the agent is doing right now,
// or the generic running note when the step has not named itself. The spinner
// travels with it — the bar no longer carries the note while a turn runs, so
// the motion cue goes where the text went.
func (m *uiModel) progressNote() string {
	if m.statusText != "" {
		return m.spin.View() + " " + m.statusText
	}
	return m.spin.View() + " " + i18n.T("running…")
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

	// While a turn runs the note lives in the chat panel's progress row, next
	// to the transcript it describes; carrying it here too would be two places
	// saying the same thing, and the bar's one row is the scarcer of the two.
	statusDesc := m.statusText
	switch {
	case m.busy:
		statusDesc = ""
	case statusDesc == "":
		statusDesc = i18n.T("waiting for a task")
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

// pendingStrip is the preview of what the next message will carry, framed like the
// input box above which it sits. It is empty when nothing is attached.
//
// The same art the transcript shows is reused rather than redrawn, so what waits to
// be sent and what was sent cannot disagree — a second renderer for the same picture
// would eventually give two answers to "what am I about to attach".
//
// Width is the terminal's, not the panel's: the strip is not inside the chat panel,
// so a sidebar beside it does not narrow it. That is why the art is drawn at the
// preview's fixed width and simply has room, rather than being re-sampled to fit.
func (m *uiModel) pendingStrip() string {
	if len(m.pending) == 0 {
		return ""
	}
	rows := 0
	for _, a := range m.pending {
		rows += a.Rows + 1
	}
	// Each row is written at its own width and lipgloss pads the rest, because a
	// row of half-blocks carries its own background only as far as the glyphs go.
	body := make([]string, 0, rows)
	for i, a := range m.pending {
		body = append(body, strings.Split(a.Art, "\n")...)
		body = append(body, truncate(a.Caption(i+1, len(m.pending), a.Name), max(m.width, 8)))
	}
	return stylePanel.Width(max(m.width, 16)).Height(rows + pendingPanelBorder).
		Render(strings.Join(body, "\n"))
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
		if i == m.setup.gguf.selected {
			marker = " ▸ "
		} else if e.dir {
			style = styleHint
		}
		rows := wrapIndent(e.label, inner, marker, "     ")
		if i != m.setup.gguf.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(i18n.T("? gguf file — enter opens, esc back"), m.setup.gguf.dir, entries, m.setup.gguf.selected)
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
		return m.floatingPanel(i18n.T("? provider key"), "", rows, 0)

	case setupURL:
		rows := [][]string{{styleHint.Render(i18n.T("base URL, e.g. http://localhost:1234/v1"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? custom endpoint"), "", rows, 0)

	case setupModel:
		rows := [][]string{{styleHint.Render(i18n.T("model id on this endpoint"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? model"), "", rows, 0)

	case setupGGUF:
		rows := [][]string{{styleHint.Render(i18n.T("path to the .gguf file — ctrl+o or enter on empty browses the disk"))},
			{m.setup.buf + "█"}}
		return m.floatingPanel(i18n.T("? gguf file"), "", rows, 0)
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
	// The workspace chrome (tree, git panel, terminal) is the screen once the
	// editor exists; the main area is its tabs or, in chat mode, the chat.
	// The editor's own altscreen flag and mouse mode ride on the View it
	// returns.
	if m.ed != nil {
		return m.ed.View()
	}
	if m.width == 0 {
		return tea.NewView(i18n.T("dmcode is starting…"))
	}
	v := tea.NewView(m.buildFrame())
	v.AltScreen = true
	// All-motion reporting is what a selection needs: a drag only produces
	// MouseMotionMsg when the terminal is told to report motion, so cell motion
	// — which was enough for the wheel — never reaches the handler that does the
	// selecting. The wheel still scrolls; /mouse turns all of it off for anyone
	// who wants the terminal's own drag-select and paste menu back.
	if m.mouseEnabled {
		v.MouseMode = tea.MouseModeAllMotion
	} else {
		v.MouseMode = tea.MouseModeNone
	}
	return v
}

// buildFrame assembles the chat frame at the model's current width and height:
// the whole window when the editor has never been opened, the workspace main
// area in chat mode (chatFrame sets the sizes to the region's then).
func (m *uiModel) buildFrame() string {
	if m.picker.open || m.palette.open || m.setup.open || m.lang.open || m.proxy.open || m.sessionsList.open || m.ask.open {
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
		case m.ask.open:
			box = m.askBox()
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
	parts = append(parts, middle, m.statusBarView())
	// The strip goes between the status bar and the input box rather than into
	// either: it is about what the *next* message will carry, which is the input's
	// business, and putting it above the bar would read as part of the transcript.
	if strip := m.pendingStrip(); strip != "" {
		parts = append(parts, strip)
	}
	parts = append(parts, stylePanel.Width(m.width).Render(m.input.View()))

	frame := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if rows := m.suggestHeight(); rows > 0 {
		frame = overlaySuggest(frame, m, rows)
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

// AskPrompt wires the broker to the event loop, so a question raised by the
// agent reaches the overlay and the answer travels back.
//
// The reply channel is buffered: the tool's goroutine may have been released
// already — by a cancelled turn, or by the timer on the broker's side — and a
// send that waited for a reader that had gone would block the event loop rather
// than the turn it belonged to.
func (m *uiModel) AskPrompt() func(ask.Request) <-chan ask.Answer {
	prog := m.prog
	return func(req ask.Request) <-chan ask.Answer {
		if prog == nil {
			return nil
		}
		reply := make(chan ask.Answer, 1)
		wait := m.askWait(req)
		prog.Send(askRequestMsg{req: req, reply: reply, wait: wait})
		return reply
	}
}

// askWait is how long this question gets: the request's own timeout if it named
// one, otherwise the session's setting.
func (m *uiModel) askWait(req ask.Request) time.Duration {
	if req.TimeoutSeconds > 0 {
		return time.Duration(req.TimeoutSeconds) * time.Second
	}
	return m.askTimeout
}

// bindSub hands RunTUI a way to attach a sender to the delegation tool, which is
// built in main before the program exists. RunTUI calls it with a function that
// reaches the event loop; main stores it inside the tool's notifier.
//
// This is a binder rather than a sender for one reason: main has no *tea.Program
// and cannot get one, so it cannot send anything itself. A plain
// func(SubEvent) parameter could only be called by main, which would be the
// wrong direction.
func RunTUI(ctx context.Context, p config.Provider, pool []config.Provider, agentTools, readOnlyTools []tool.Tool, toolNames []string, mcpToolsets []tool.Toolset, mcpNotes []string, broker *ask.Broker, bindSub func(func(dmagent.SubEvent)), yoloTools []tool.Tool) error {
	if len(pool) == 0 {
		pool = []config.Provider{p}
	}
	// The notifier needs the program, which needs the model, which needs the
	// agent: the program is wired in afterwards, and a failover cannot happen
	// before the first turn anyway.
	m := InitialModel(nil, nil, p, agentTools, readOnlyTools, toolNames, yoloTools)
	m.pool = pool
	m.ctx = ctx
	m.mcpToolsets = mcpToolsets
	m.askTimeout = config.AskTimeout()
	// A server the eager listing could not ask is a real problem the user has
	// to see: its tools will not exist until it comes up.
	if len(mcpNotes) > 0 {
		m.statusText = strings.Join(mcpNotes, "; ")
	}

	prog := tea.NewProgram(m)
	m.prog = prog
	if broker != nil {
		broker.SetPromptFunc(m.AskPrompt())
	}
	if bindSub != nil {
		bindSub(func(ev dmagent.SubEvent) { prog.Send(subAgentMsg{ev}) })
	}

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
	// The mode switch is added here, for plan mode only, rather than in the
	// tool set main builds: it needs this model's hook and it needs to know the
	// current mode, and neither exists where those sets are assembled. In act
	// mode it is simply absent, so the agent cannot ask for the mode it is
	// already in — which is also what stops plan↔act oscillation.
	if mode == modePlan {
		switchTool, err := dmagent.SwitchModeTool(dmagent.ModePlan, m.setModeRequest)
		if err != nil {
			return nil, err
		}
		ts = append(slices.Clone(ts), switchTool)
	}
	a, err := dmagent.BuildPooledAgent(m.ctx, pool, ts, m.switchNotifier(), m.retryNotifier(), mode.agentMode(), m.mcpToolsets...)
	if err != nil {
		return nil, err
	}
	if m.svc == nil {
		m.sessions = memsession.NewPersistent(config.SessionsDir())
		m.sessions.SetIdentity("dmcode", "user")
		// Registering what earlier runs left behind is what makes /sessions
		// able to offer them: only the header of each file is read, so this
		// costs a short read per saved conversation, not its events.
		if n := m.sessions.Discover(m.ctx, "dmcode", "user"); n > 0 {
			m.recoveredSessions = n
		}
		if id, err := m.sessions.Ensure(m.ctx, "dmcode", "user", m.sessionID); err == nil {
			m.sessionID = id
		}
		m.svc = m.sessions
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
