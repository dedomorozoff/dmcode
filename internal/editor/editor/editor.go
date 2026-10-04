package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/hinshun/vt10x"

	"github.com/dedomorozoff/dmcode/internal/editor/buffer"
	"github.com/dedomorozoff/dmcode/internal/editor/config"
	"github.com/dedomorozoff/dmcode/internal/editor/events"
	"github.com/dedomorozoff/dmcode/internal/editor/i18n"
	"github.com/dedomorozoff/dmcode/internal/editor/lsp"
	"github.com/dedomorozoff/dmcode/internal/editor/plugin"
	"github.com/dedomorozoff/dmcode/internal/editor/ptyterm"
	"github.com/dedomorozoff/dmcode/internal/editor/session"
	"github.com/dedomorozoff/dmcode/internal/editor/syntax"
	"github.com/dedomorozoff/dmcode/internal/editor/vcs"
	"github.com/dedomorozoff/dmcode/internal/editor/watcher"
)

type tab struct {
	buf          *buffer.Buffer
	path         string
	syntaxCached []syntax.HighlightedLine
	syntaxText   string
	diffCached   vcs.FileDiff
	diffText     string
	blame        []vcs.BlameLine // git blame of HEAD lines; nil = not computed
	lineEnding   string          // "lf" or "crlf"
	encoding     string          // "utf-8", "utf-16le", "utf-16be", "latin-1"
	wrapSegs     []wrapSeg
	wrapW        int
	wrapTabW     int
	wrapText     string
}

// wrapSeg describes one screen row of a wrapped (or plain) buffer line: the
// line number and the tab-expanded column range it covers. In non-wrap mode
// every buffer line yields exactly one segment spanning the whole line.
type wrapSeg struct {
	line     int
	expStart int
	expEnd   int
}

func (t *tab) name(base string) string {
	if t.path == "" {
		return "[untitled]"
	}
	return shortenPath(base, t.path)
}

// detectFileInfo analyzes raw bytes to determine line endings and encoding.
func detectFileInfo(data []byte) (lineEnding, encoding string) {
	lineEnding = "lf"
	encoding = "utf-8"

	// Detect BOM
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		encoding = "utf-8"
		data = data[3:]
	} else if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		encoding = "utf-16le"
		data = data[2:]
	} else if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		encoding = "utf-16be"
		data = data[2:]
	}

	// Detect CRLF
	for _, b := range data {
		if b == '\r' {
			lineEnding = "crlf"
			break
		}
	}

	// Check for non-ASCII bytes that aren't valid UTF-8
	if encoding == "utf-8" {
		for i := 0; i < len(data); {
			b := data[i]
			if b < 0x80 {
				i++
			} else if b < 0xC0 {
				encoding = "latin-1"
				break
			} else if b < 0xE0 {
				if i+1 >= len(data) || data[i+1]&0xC0 != 0x80 {
					encoding = "latin-1"
					break
				}
				i += 2
			} else if b < 0xF0 {
				if i+2 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 {
					encoding = "latin-1"
					break
				}
				i += 3
			} else if b < 0xF8 {
				if i+3 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 || data[i+3]&0xC0 != 0x80 {
					encoding = "latin-1"
					break
				}
				i += 4
			} else {
				encoding = "latin-1"
				break
			}
		}
	}

	return
}

// getSyntaxLines returns the highlighted lines for the tab, cached against the
// buffer text. h comes from the model so the theme is owned by the editor
// rather than by package-level state in internal/syntax.
func (t *tab) getSyntaxLines(h *syntax.Highlighter) []syntax.HighlightedLine {
	text := t.buf.Text()
	if t.syntaxCached != nil && t.syntaxText == text {
		return t.syntaxCached
	}
	t.syntaxText = text
	t.syntaxCached = h.HighlightBuffer(t.path, text)
	return t.syntaxCached
}

// tabWrap returns the wrap segments for the tab at the given content width:
// one segment per screen row. In non-wrap mode this is one full-line segment
// per buffer line; callers only enable segmentation when the pane wraps.
// Results are cached and invalidated automatically when the text, content
// width, or tab width changes.
func (t *tab) tabWrap(contentW, tabW int) []wrapSeg {
	text := t.buf.Text()
	if t.wrapSegs != nil && t.wrapW == contentW && t.wrapTabW == tabW && t.wrapText == text {
		return t.wrapSegs
	}
	var segs []wrapSeg
	for ln := 0; ln < t.buf.LineCount(); ln++ {
		line := t.buf.LineAt(ln)
		var exp []rune
		for _, r := range line {
			if r == '\t' {
				for k := 0; k < tabW; k++ {
					exp = append(exp, ' ')
				}
			} else {
				exp = append(exp, r)
			}
		}
		segs = append(segs, wrapLine(ln, exp, contentW)...)
	}
	t.wrapSegs = segs
	t.wrapW = contentW
	t.wrapTabW = tabW
	t.wrapText = text
	return segs
}

// wrapLine splits one tab-expanded line into width-sized segments, breaking at
// word boundaries when possible.
func wrapLine(ln int, exp []rune, w int) []wrapSeg {
	if w < 1 {
		w = 1
	}
	if len(exp) <= w {
		return []wrapSeg{{line: ln, expStart: 0, expEnd: len(exp)}}
	}
	var segs []wrapSeg
	start := 0
	for start < len(exp) {
		end := start + w
		if end > len(exp) {
			end = len(exp)
		}
		if end < len(exp) {
			// Prefer breaking right after the last space inside the window.
			brk := -1
			for i := end - 1; i > start; i-- {
				if exp[i] == ' ' {
					brk = i + 1
					break
				}
			}
			if brk > start {
				end = brk
			}
		}
		if end <= start {
			end = start + 1
			if end > len(exp) {
				end = len(exp)
			}
		}
		segs = append(segs, wrapSeg{line: ln, expStart: start, expEnd: end})
		if end == len(exp) {
			break
		}
		start = end
	}
	return segs
}

// segRowForCol finds the screen row (index into tabWrap's result) that shows
// the given tab-expanded column of a buffer line, returning its segment start.
func segRowForCol(segs []wrapSeg, line, expCol int) (row, expStart int) {
	for i := range segs {
		s := segs[i]
		if s.line == line && expCol >= s.expStart && expCol < s.expEnd {
			return i, s.expStart
		}
	}
	// Fall back to the last segment of the line (column past end of line).
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].line == line {
			return i, segs[i].expStart
		}
	}
	return 0, 0
}

func (t *tab) getDiff(repo *vcs.Repo) vcs.FileDiff {
	if t.path == "" {
		return vcs.FileDiff{}
	}
	r := repo
	if r == nil || !strings.HasPrefix(t.path, r.Root) {
		if found, err := vcs.Open(filepath.Dir(t.path)); err == nil {
			r = found
		}
	}
	if r == nil {
		return vcs.FileDiff{}
	}
	text := t.buf.Text()
	if t.diffCached.Lines != nil && t.diffText == text {
		return t.diffCached
	}
	t.diffText = text
	t.diffCached = r.DiffBuffer(t.path, text)
	return t.diffCached
}

type Model struct {
	root string
	cfg  config.Config
	// Embed marks a model running inside another Bubble Tea program (dmcode's
	// TUI). The editor then never returns tea.Quit: its exit paths produce
	// CloseEditorMsg, and the parent closes it without ending the process.
	Embed bool
	// Chat puts the workspace into chat mode: the chrome — tab bar, tree
	// sidebar, git panel, terminal, status bar — stays on screen, but the
	// main area and every key and click no panel owns belong to the host
	// (dmcode's chat transcript and its input line).
	Chat bool
	// Host carries the callbacks chat mode calls back into. It is set once at
	// embed time and never touched by the editor itself.
	Host *Host
	g    glyphSet // active render glyphs (unicode or ASCII fallback)
	// syn is the syntax highlighter owned by the model. It was a package-level
	// variable in internal/syntax, written from Update on config hot-reload
	// and read while rendering; keeping it here removes that global mutable.
	syn        *syntax.Highlighter
	tr         i18n.Translator
	plugins    *plugin.Manager
	lspClient  *lsp.Client
	diagCh     chan lspDiagMsg
	diags      map[string][]lsp.Diagnostic
	tabs       []tab
	panes      []pane
	layout     splitLayout
	activePane int
	width      int
	height     int
	msg        string

	promptOpen      bool
	promptIn        []rune
	promptSave      bool
	promptSaveIn    []rune
	promptNewFile   bool
	promptNewFolder bool
	promptRename    bool
	promptRenameRel string

	finderOpen  bool
	finderQ     []rune
	finderFiles []string
	finderHits  []string
	finderSel   int

	// Folder browser: a native TUI picker so "File: Open Folder..." works the
	// same on every platform (no zenity/kdialog/PowerShell dependency).
	folderOpen    bool
	folderPath    string // current directory being browsed
	folderEntries []folderEntry
	folderSel     int
	folderOffset  int

	helpOpen   bool
	helpScroll int // help panel scroll offset (the list overflows small screens)

	treeVisible    bool
	treeFocus      bool
	treeRows       []treeEntry
	treeSel        int
	treeOffset     int
	expanded       map[string]bool
	treeConfirm    string // "" | "delete" | "trash" — pending file action confirmation
	treeConfirmRel string // path (relative to baseDir) the confirmation applies to

	// Search/replace
	searchOpen         bool
	searchQuery        []rune
	searchMatchIdx     int
	searchTotalMatches int
	replaceOpen        bool
	replaceWith        []rune
	replaceFocusFind   bool

	// Go to line
	gotoOpen bool
	gotoIn   []rune

	// Events, watcher, Git
	bus                *events.Bus
	watcher            *watcher.Watcher
	fileEvents         chan string
	repo               *vcs.Repo
	conflictOpen       bool
	conflictPath       string
	conflictRows       []vcs.DiffRow
	conflictLeftLines  []string
	conflictRightLines []string
	conflictOffY       int
	conflictOffX       int
	gitOpen            bool
	gitFocus           bool // keys route to the git panel; open+unfocused shows it in the sidebar
	gitMode            gitPanelMode
	gitFiles           []vcs.FileStatus
	gitSel             int
	gitOffset          int
	gitCommitIn        []rune
	gitDiffFocused     bool // true when diff preview area has focus (scrollable)

	// Git log
	gitLogEntries []vcs.LogEntry
	gitLogSel     int
	gitLogOffset  int

	// Inline git blame annotations (Alt+B)
	blameOn bool

	// Git branch management
	gitBranchIn     []rune
	gitBranchList   []string
	gitBranchSel    int
	gitBranchOffset int
	gitBranchNew    bool // true when creating a new branch, false when switching

	// Side-by-side diff view (opened from the Git panel)
	diffViewOpen    bool
	diffPath        string
	diffRows        []vcs.DiffRow
	diffHeadLines   []string
	diffRightLines  []string
	diffHeadSyntax  []syntax.HighlightedLine
	diffRightSyntax []syntax.HighlightedLine
	diffOffsetY     int
	diffOffsetX     int

	// Bottom terminal panel (real PTY + ANSI screen state)
	termOpen     bool
	termRows     []terminalRow
	termCursorX  int
	termCursorY  int
	termCursorOK bool
	termSession  *ptyterm.Terminal
	termVT       vt10x.Terminal
	termCh       chan terminalOutputMsg
	termExitCh   chan terminalExitMsg
	termGen      int

	// Bookmarks are session-local navigation marks; they share the gutter's
	// marker column with diagnostics and git changes.
	bookmarks map[string]map[int]bool // abs path → line (1-based) → true

	// Command palette & Clipboard
	paletteOpen   bool
	paletteQ      []rune
	paletteSel    int
	paletteOffset int
	clipboard     string

	// Language chooser
	langChooserOpen bool
	langChooserSel  int

	// Plugin store (install/uninstall bundled plugins)
	pluginStoreOpen       bool
	pluginStoreSel        int
	pendingPluginRemovals map[string]bool
	storeItems            []storeItem
	storeLoading          bool
	storeErr              string
	pendingStoreInstall   string

	// Autocompletion popup
	complOpen   bool
	complItems  []string
	complSel    int
	complOffset int
	complLine   int
	complStart  int

	quitConfirm bool
	quitTab     bool // true if confirming close of a single tab (not quit)
	pendingQuit bool

	agentReviewOffX int

	// Mouse state
	mouseDown  bool
	hoverIcon  statusAction // status-bar icon under the cursor (actNone if none)
	hoverSplit statusAction // top-right split icon under the cursor

	// Panel text selection: drag with the mouse (with Shift held) over the
	// terminal output; on release the selected text lands in the clipboard.
	termSelActive bool
	termSelAnchor selPos
	termSelEnd    selPos
	dragTerm      bool // motion events extend the terminal selection
	termFocus     bool // the terminal panel owns keyboard input

	// Double-click detection: last click position/time plus a validity flag so
	// a third quick click starts a fresh pair instead of chaining.
	lastClickX     int
	lastClickY     int
	lastClickTime  time.Time
	lastClickValid bool

	// Double-Shift detection (JetBrains-style "search everywhere"): the time of
	// the previous bare Shift press so two rapid taps open the palette. Only
	// terminals with the Kitty protocol / Windows Console API report bare
	// modifier presses, so this degrades gracefully elsewhere.
	lastShiftTime time.Time
}

var debugKeys = os.Getenv("DMCODE_DEBUG_KEYS") != "" || os.Getenv("DMED_DEBUG_KEYS") != ""

// Russian ЙЦУКЕН → English QWERTY mapping for layout-independent keybindings.
var ruToEn = map[rune]rune{
	'й': 'q', 'ц': 'w', 'у': 'e', 'к': 'r', 'е': 't', 'н': 'y',
	'г': 'u', 'ш': 'i', 'щ': 'o', 'з': 'p', 'х': '[', 'ъ': ']',
	'ф': 'a', 'ы': 's', 'в': 'd', 'а': 'f', 'п': 'g', 'р': 'h',
	'о': 'j', 'л': 'k', 'д': 'l', 'ж': ';', 'э': '\'',
	'я': 'z', 'ч': 'x', 'с': 'c', 'м': 'v', 'и': 'b', 'т': 'n',
	'ь': 'm', 'б': ',', 'ю': '.',
	'ё': '`',
}

func normalizeKey(r rune) rune {
	if en, ok := ruToEn[r]; ok {
		return unicode.ToLower(en)
	}
	if en, ok := ruToEn[unicode.ToLower(r)]; ok {
		return unicode.ToLower(en)
	}
	return r
}

// c0Special reports whether a C0 control code is really one of the control
// keys that ultraviolet delivers as a special key code (Tab, Enter, Escape)
// rather than a Ctrl+letter chord that needs re-encoding.
func c0Special(r rune) bool {
	return r == '\t' || r == tea.KeyEnter || r == tea.KeyEsc
}

// controlByteKey converts a raw control byte (0x01–0x1f, NUL = Ctrl+Space)
// into the matching ctrl+key message, preserving any other modifiers reported
// by the terminal (notably Alt for Ctrl+Alt combos sent as ESC + a control
// byte on some terminals).
func controlByteKey(r rune, mod tea.KeyMod) tea.KeyPressMsg {
	if r == 0 {
		return tea.KeyPressMsg{Code: tea.KeySpace, Mod: mod | tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: r + 96, Mod: mod | tea.ModCtrl}
}

// New builds the standalone editor: on start it restores the previous
// session's tabs for the project root, and on exit it saves them.
func New(paths ...string) Model { return newModel(false, paths...) }

// NewEmbedded builds the editor for a host program — dmcode's workspace. The
// editor's own session file does not apply: a fresh dmcode start opens on a
// bare editor rather than the last run's tabs, and which files are open is
// the host's business (OpenChangedTabs on entering the editor, CloseAllTabs
// when the conversation changes).
func NewEmbedded(paths ...string) Model { return newModel(true, paths...) }

func newModel(embed bool, paths ...string) Model {
	fe := make(chan string, 16)
	m := Model{
		g:                     unicodeGlyphs,
		Embed:                 embed,
		width:                 80,
		height:                24,
		expanded:              map[string]bool{},
		fileEvents:            fe,
		bus:                   events.New(),
		diagCh:                make(chan lspDiagMsg, 64),
		diags:                 map[string][]lsp.Diagnostic{},
		pendingPluginRemovals: map[string]bool{},
		bookmarks:             map[string]map[int]bool{},
	}
	if w, err := watcher.New(func(p string) {
		select {
		case fe <- p:
		default:
		}
	}); err == nil {
		m.watcher = w
	}

	var files []string
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if m.root == "" {
				m.root = normalizePath(".", p)
				m.msg = m.t("msg.project", filepath.Base(m.root))
			}
			continue
		}
		files = append(files, p)
	}

	// Restore the previous session only when the user did not open specific
	// files and a project root is known — an explicit file list takes
	// precedence over the session. An embedded editor never restores: the
	// host decides what is open.
	restoreActiveTab := -1
	if !embed && len(files) == 0 && m.root != "" {
		sessPath := session.DefaultPath(m.root)
		if sessPath != "" {
			if _, err := os.Stat(sessPath); err != nil {
				sessPath = session.LegacyPath(m.root)
			}
		}
		if sess, err := session.Load(sessPath); err == nil && len(sess.Files) > 0 {
			for _, f := range sess.Files {
				m.openPath(f)
			}
			m.restoreCursors(sess.Cursors)
			restoreActiveTab = sess.ActiveTab
		}
	} else {
		for _, p := range files {
			m.openPath(p)
		}
	}
	if len(m.tabs) == 0 {
		m.tabs = append(m.tabs, tab{buf: buffer.New()})
	}
	m.cfg = config.Load(m.root)
	m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
	m.syn = syntax.New(m.cfg.Editor.SyntaxTheme)
	m.initPanes()
	if restoreActiveTab >= 0 && restoreActiveTab < len(m.tabs) {
		m.setActiveTab(restoreActiveTab)
	}
	if m.root != "" {
		m.treeVisible = true
		m.rebuildTree()
	}
	if repo, err := vcs.Open(m.baseDir()); err == nil {
		m.repo = repo
	}
	m.loadPlugins()
	m.plugins.Emit(&m, "ready")
	// Watch root directory for tree updates
	if m.root != "" && m.watcher != nil {
		m.watcher.Watch(m.root)
	}
	// Watch config files for hot-reload
	if p := config.ConfigPath(); m.watcher != nil {
		m.watcher.Watch(p)
	}
	if p := config.ProjectConfigPath(m.root); p != "" && m.watcher != nil {
		m.watcher.Watch(p)
	}
	return m
}

func (m Model) baseDir() string {
	if m.root != "" {
		return m.root
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func (m Model) activeTab() *tab { return &m.tabs[m.activeTabIndex()] }

func (m *Model) cur() *tab {
	if len(m.tabs) == 0 {
		return nil
	}
	return &m.tabs[m.activeTabIndex()]
}

func (m *Model) openPath(rawPath string) {
	path := normalizePath(m.baseDir(), rawPath)
	data, err := os.ReadFile(path)
	t := tab{path: path, buf: buffer.New(), lineEnding: "lf", encoding: "utf-8"}
	if err != nil {
		if os.IsNotExist(err) {
			m.msg = m.t("msg.new_file", path)
		} else {
			m.msg = m.t("msg.open_failed", err.Error())
		}
	} else {
		le, enc := detectFileInfo(data)
		t.lineEnding = le
		t.encoding = enc
		t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
	}
	if m.watcher != nil && path != "" {
		if err := m.watcher.Watch(path); err != nil {
			// Watching is best-effort, but say so: a silent failure here looks
			// exactly like "external changes are not detected".
			m.msg = m.t("msg.watch_failed", path, err.Error())
		}
	}
	m.tabs = append(m.tabs, t)
	// Only set active tab if panes are already initialized
	if len(m.panes) > 0 {
		m.setActiveTab(len(m.tabs) - 1)
	}
	if m.plugins != nil {
		m.plugins.Emit(m, "file_open")
	}
	// Hint when this file's language needs an LSP server that isn't installed.
	if err == nil {
		if hint := m.lspMissingHintFor(t.path); hint != "" {
			m.msg = m.t("msg.lsp_missing", hint)
		}
	}
}

func (m *Model) switchTab(d int) {
	n := len(m.tabs)
	if n == 0 {
		return
	}
	idx := m.activeTabIndex()
	m.setActiveTab(((idx+d)%n + n) % n)
}

func (m *Model) jumpTab(n int) {
	m.setActiveTab(n)
}

func (m *Model) closeTab() tea.Cmd {
	return m.closeTabAt(m.activeTabIndex())
}

// closeTabAt closes the tab at the given index (used by middle-click on the
// tab bar, which targets a specific tab rather than the active one).
func (m *Model) closeTabAt(idx int) tea.Cmd {
	if len(m.tabs) == 1 {
		return m.requestQuit()
	}
	if idx < 0 || idx >= len(m.tabs) {
		return nil
	}
	if m.layout != splitNone {
		// Closing a tab in a split also collapses the split.
		other := 1 - m.activePane
		m.panes = []pane{m.panes[other]}
		m.activePane = 0
		m.layout = splitNone
	}
	m.tabs = append(m.tabs[:idx], m.tabs[idx+1:]...)
	m.fixPaneTabsAfterClose(idx)
	return nil
}

func (m *Model) startPrompt() {
	m.promptOpen = true
	m.promptIn = nil
}

func (m *Model) startNewFilePrompt() {
	m.promptOpen = true
	m.promptNewFile = true
	m.promptRenameRel = ""
	m.promptIn = nil
}

func (m *Model) startNewFolderPrompt() {
	m.promptOpen = true
	m.promptNewFolder = true
	m.promptRenameRel = ""
	m.promptIn = nil
}

func (m *Model) startTreeNewFilePrompt() {
	m.promptOpen = true
	m.promptNewFile = true
	m.promptRenameRel = ""
	m.promptIn = []rune(m.treeTargetDir())
}

func (m *Model) startTreeNewFolderPrompt() {
	m.promptOpen = true
	m.promptNewFolder = true
	m.promptRenameRel = ""
	m.promptIn = []rune(m.treeTargetDir())
}

func (m *Model) startTreeRenamePrompt(rel string) {
	m.promptOpen = true
	m.promptRename = true
	m.promptRenameRel = rel
	m.promptIn = []rune(relName(rel))
}

func (m *Model) startSavePrompt() {
	m.promptSave = true
	m.promptSaveIn = nil
}

func (m *Model) startFinder() {
	m.finderOpen = true
	m.finderQ = nil
	m.finderSel = 0
	m.finderFiles = collectFiles(m.baseDir(), m.cfg.Editor.SkippedDirs)
	m.finderHits = searchFiles(m.finderFiles, "")
}

// openConfigFile opens the .dmcode.conf file in a new tab for editing.
// Creates the file with defaults if it doesn't exist.
func (m *Model) openConfigFile() {
	path := config.ConfigPath()
	if m.root != "" {
		path = config.ProjectConfigPath(m.root)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create with defaults
		content := "# dmcode editor configuration\n" +
			"# Uncomment settings to override defaults.\n\n" +
			"[editor]\n" +
			"# tab_width = 4\n" +
			"# syntax_theme = monokai\n" +
			"# line_numbers = true\n" +
			"# skipped_dirs = .git,node_modules\n\n" +
			"[ui]\n" +
			"# tree_width = 25\n\n" +
			"[plugins]\n" +
			"# repo = dedomorozoff/dmed  # GitHub store for the plugin store\n" +
			"# dir = plugins\n" +
			"# branch = main\n"
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(content), 0o644)
	}
	m.openPath(path)
	m.msg = m.t("msg.edit_config")
}

func (m *Model) refind() {
	m.finderHits = searchFiles(m.finderFiles, string(m.finderQ))
	if m.finderSel >= len(m.finderHits) {
		m.finderSel = len(m.finderHits) - 1
	}
	if m.finderSel < 0 {
		m.finderSel = 0
	}
}

// normalizePaste converts CRLF / lone CR line endings in pasted text to LF.
// Windows terminals and the system clipboard deliver "\r\n", and a literal
// "\r" inside buffer content would both garble the terminal rendering (the
// terminal treats it as a carriage return) and pollute the saved file.
func normalizePaste(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// pasteInput inserts pasted text into the active field, mirroring where typed
// keys land (see handleKey routing), and falls back to the editor buffer.
func (m *Model) pasteInput(text string) {
	text = normalizePaste(text)
	switch {
	case m.promptSave:
		m.promptSaveIn = append(m.promptSaveIn, []rune(text)...)
		return
	case m.promptOpen:
		m.promptIn = append(m.promptIn, []rune(text)...)
		return
	case m.searchOpen:
		if m.replaceOpen && !m.replaceFocusFind {
			m.replaceWith = append(m.replaceWith, []rune(text)...)
		} else {
			m.searchQuery = append(m.searchQuery, []rune(text)...)
			m.updateSearchMatches(false)
		}
		return
	case m.finderOpen:
		m.finderQ = append(m.finderQ, []rune(text)...)
		m.refind()
		return
	case m.paletteOpen:
		m.paletteQ = append(m.paletteQ, []rune(text)...)
		return
	}
	if m.cur().buf.HasMultipleCursors() {
		m.cur().buf.MultiInsertText(text)
	} else {
		m.cur().buf.InsertText(text)
	}
	m.msg = m.t("msg.pasted")
}

func (m *Model) handleFinder(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.finderOpen = false
	case "enter":
		if n := len(m.finderHits); n > 0 {
			path := m.finderHits[m.finderSel]
			m.finderOpen = false
			m.focusOrOpen(path)
		} else {
			m.finderOpen = false
		}
	case "up":
		if n := len(m.finderHits); n > 0 {
			m.finderSel = (m.finderSel - 1 + n) % n
		}
	case "down":
		if n := len(m.finderHits); n > 0 {
			m.finderSel = (m.finderSel + 1) % n
		}
	case "backspace":
		if n := len(m.finderQ); n > 0 {
			m.finderQ = m.finderQ[:n-1]
			m.refind()
		}
	default:
		if len(msg.Text) > 0 {
			m.finderQ = append(m.finderQ, []rune(msg.Text)...)
			m.refind()
		}
	}
	return nil
}

func (m *Model) handleHelp(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "f1", "ctrl+e", "q":
		m.helpOpen = false
	case "j", "down", "pgdown":
		m.scrollHelp(1)
	case "k", "up", "pgup":
		m.scrollHelp(-1)
	case "g", "home":
		m.helpScroll = 0
	case "G", "end":
		m.helpScroll = m.helpMaxScroll()
	}
	return nil
}

// scrollHelp moves the help panel by d rows (negative = up) and clamps it.
func (m *Model) scrollHelp(d int) {
	m.helpScroll += d
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}
	if max := m.helpMaxScroll(); m.helpScroll > max {
		m.helpScroll = max
	}
}

// helpMaxScroll is the maximum help scroll offset so the last row stays
// visible; 0 when everything already fits.
func (m Model) helpMaxScroll() int {
	n := len(helpEntries) + 1 // title row + entries
	if h := m.viewHeight(); n > h {
		return n - h
	}
	return 0
}

func (m *Model) focusOrOpen(rawPath string) {
	path := normalizePath(m.baseDir(), rawPath)
	for i := range m.tabs {
		if m.tabs[i].path == path {
			m.setActiveTab(i)
			return
		}
	}
	m.openPath(path)
}

func (m *Model) handlePrompt(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.promptOpen = false
		m.promptNewFile = false
		m.promptNewFolder = false
		m.promptRename = false
		m.promptRenameRel = ""
	case "enter":
		path := strings.TrimSpace(string(m.promptIn))
		newFolder := m.promptNewFolder
		renameRel := m.promptRenameRel
		m.promptOpen = false
		m.promptNewFile = false
		m.promptNewFolder = false
		m.promptRename = false
		m.promptRenameRel = ""
		if path == "" {
			break
		}
		switch {
		case renameRel != "":
			m.renameTreeEntry(renameRel, path)
		case newFolder:
			full := normalizePath(m.baseDir(), path)
			if err := os.MkdirAll(full, 0o755); err != nil {
				m.msg = m.t("msg.create_folder_fail", err.Error())
			} else {
				m.msg = m.t("msg.created_folder", filepath.Base(full))
			}
			m.rebuildTree()
			m.refreshGitFiles()
		default:
			m.openPath(path)
			m.rebuildTree()
			m.refreshGitFiles()
		}
	case "backspace":
		if n := len(m.promptIn); n > 0 {
			m.promptIn = m.promptIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.promptIn = append(m.promptIn, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m *Model) handleSavePrompt(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.promptSave = false
		m.pendingQuit = false
	case "enter":
		raw := strings.TrimSpace(string(m.promptSaveIn))
		m.promptSave = false
		if raw != "" {
			path := normalizePath(m.baseDir(), raw)
			t := m.cur()
			t.path = path
			if m.watcher != nil {
				if err := m.watcher.Watch(path); err != nil {
					m.msg = m.t("msg.watch_failed", path, err.Error())
				}
			}
			text := t.buf.Text()
			if t.lineEnding == "crlf" {
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			if err := os.WriteFile(t.path, []byte(text), 0o644); err != nil {
				m.msg = m.t("msg.save_failed", err.Error())
				return nil
			}
			t.buf.MarkSaved()
			m.msg = m.t("msg.saved")
			if m.pendingQuit {
				m.pendingQuit = false
				return m.quit()
			}
		}
	case "backspace":
		if n := len(m.promptSaveIn); n > 0 {
			m.promptSaveIn = m.promptSaveIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.promptSaveIn = append(m.promptSaveIn, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m Model) hasDirty() bool {
	for _, t := range m.tabs {
		if t.buf.Dirty() {
			return true
		}
	}
	return false
}

func (m *Model) handleQuitConfirm(msg tea.KeyPressMsg) tea.Cmd {
	// gitKeyName maps the physical Y/N keys to y/n in any keyboard layout, so
	// the confirmation works in Cyrillic too (н/т are the Y/N keys there).
	switch gitKeyName(msg) {
	case "esc":
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
	case "y", "Y":
		t := m.cur()
		if t.path == "" {
			m.pendingQuit = true
			m.quitConfirm = false
			m.startSavePrompt()
			return nil
		}
		m.saveActive()
		if t.buf.Dirty() {
			m.quitConfirm = false
			m.pendingQuit = false
			m.quitTab = false
			m.msg = m.t("msg.save_failed_gen")
			if m.quitTab {
				m.quitTab = false
			}
			return nil
		}
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
		if m.quitTab {
			// Just close the tab after saving
			cmd := m.closeTab()
			if cmd == nil {
				return nil
			}
			m.shutdown()
			return cmd
		}
		return m.quit()
	case "n", "N":
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
		if m.quitTab {
			// Close the tab without saving
			cmd := m.closeTab()
			if cmd == nil {
				return nil
			}
			m.shutdown()
			return cmd
		}
		return m.quit()
	}
	return nil
}

type FileChangedMsg struct {
	Path string
}

func waitForFileEvent(ch <-chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return FileChangedMsg{Path: p}
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForFileEvent(m.fileEvents), waitForTermOutput(m.termCh), waitForLSPDiag(m.diagCh))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case FileChangedMsg:
		path := msg.Path
		// Hot-reload config when the editor conf changes
		if strings.HasSuffix(path, ".dmcode.conf") || strings.HasSuffix(path, "editor.conf") || strings.HasSuffix(path, ".dmed.conf") {
			m.cfg = config.Load(m.root)
			m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
			// A new highlighter invalidates the per-tab cached lines.
			m.syn = syntax.New(m.cfg.Editor.SyntaxTheme)
			for i := range m.tabs {
				m.tabs[i].syntaxCached = nil
			}
			m.msg = m.t("msg.config_reloaded")
			return m, waitForFileEvent(m.fileEvents)
		}
		// Hot-reload a changed plugin without restarting the editor.
		if m.plugins != nil && m.isPluginPath(path) {
			// Skip events for plugins the store just removed itself.
			if m.pendingPluginRemovals[path] {
				delete(m.pendingPluginRemovals, path)
				return m, waitForFileEvent(m.fileEvents)
			}
			m.reloadPlugin(path)
			return m, waitForFileEvent(m.fileEvents)
		}
		for i := range m.tabs {
			t := &m.tabs[i]
			if t.path == "" {
				continue
			}
			absT, _ := filepath.Abs(t.path)
			if absT == path || t.path == path {
				if !t.buf.Dirty() {
					if data, err := os.ReadFile(t.path); err == nil {
						t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
						t.syntaxCached = nil
						t.diffText = ""
						m.msg = m.t("msg.reloaded_name", t.name(m.baseDir()))
					}
				} else {
					if data, err := os.ReadFile(t.path); err == nil {
						diskText := strings.ReplaceAll(string(data), "\r\n", "\n")
						bufText := t.buf.Text()
						m.conflictRows = vcs.SideBySide(bufText, diskText)
						m.conflictLeftLines = strings.Split(strings.TrimRight(bufText, "\n"), "\n")
						m.conflictRightLines = strings.Split(strings.TrimRight(diskText, "\n"), "\n")
						m.conflictOffY = 0
						m.conflictOffX = 0
					}
					m.conflictOpen = true
					m.conflictPath = path
					m.msg = m.t("msg.external")
				}
				break
			}
		}
		// Refresh Git panel if open to show updated file status
		if m.gitOpen {
			m.refreshGitFiles()
		}
		// Rebuild tree if the change is in the project root
		if m.treeVisible && m.root != "" {
			absRoot, _ := filepath.Abs(m.root)
			absPath, _ := filepath.Abs(path)
			if strings.HasPrefix(absPath, absRoot) {
				m.rebuildTree()
			}
		}
		return m, waitForFileEvent(m.fileEvents)
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.resizeTerminal()
	case terminalOutputMsg:
		if msg.gen != m.termGen {
			return m, nil
		}
		m.termRows = msg.rows
		if m.termVT != nil {
			m.termVT.Lock()
			cur := m.termVT.Cursor()
			m.termCursorX, m.termCursorY = cur.X, cur.Y
			m.termCursorOK = m.termVT.CursorVisible()
			m.termVT.Unlock()
		}
		if msg.exited {
			m.msg = "terminal: process exited"
		}
		return m, waitForTermOutput(m.termCh)
	case terminalExitMsg:
		if msg.gen != m.termGen {
			return m, nil
		}
		m.termSession = nil
		if msg.err != nil && !strings.Contains(strings.ToLower(msg.err.Error()), "killed") {
			m.msg = "terminal: " + msg.err.Error()
		}
	case gitTransferMsg:
		if msg.err != "" {
			m.msg = m.t("git.transfer_error", m.t("git.op_"+msg.op), msg.err)
			return m, nil
		}
		m.msg = m.t("git.transfer_done", m.t("git.op_"+msg.op))
		if m.gitOpen {
			m.refreshGitFiles()
		}
	case gitBlameMsg:
		if msg.err != "" {
			m.msg = m.t("git.blame_error", msg.err)
			m.blameOn = false
			return m, nil
		}
		for i := range m.tabs {
			t := &m.tabs[i]
			abs, _ := filepath.Abs(t.path)
			if abs == msg.path {
				t.blame = msg.lines
				if len(msg.lines) == 0 {
					m.msg = m.t("msg.blame_none")
				} else {
					m.msg = m.t("msg.blame_on", len(msg.lines))
				}
				break
			}
		}
	case tea.MouseClickMsg:
		cmd := m.handleMouseClick(msg)
		return m, cmd
	case tea.MouseWheelMsg:
		cmd := m.handleMouseWheel(msg)
		return m, cmd
	case tea.MouseReleaseMsg:
		if m.Chat {
			if m.Host != nil && m.Host.Release != nil {
				return m, m.Host.Release(msg)
			}
			return m, nil
		}
		m.mouseDown = false
		switch {
		case m.dragTerm:
			m.dragTerm = false
			m.copyToClipboard(m.termSelectionText())
		case m.termOpen && msg.Y >= m.termStartRow() && msg.Y < m.termStartRow()+m.termPanelHeight():
			button := 0
			if msg.Button == tea.MouseMiddle {
				button = 1
			} else if msg.Button == tea.MouseRight {
				button = 2
			}
			m.forwardTerminalMouseEvent(button, msg.X, msg.Y-m.termStartRow(), 'm')
		}
	case tea.MouseMotionMsg:
		if m.Chat {
			// The chat owns the main area, but the chrome rows are still the
			// editor's to hover: a callout left over from the editor mode —
			// or drawn over the strip itself — must clear on motion, or it
			// sits over the transcript forever. Chat mode draws none of its
			// own, so a pointer over the strip belongs to the editor and is
			// not handed to the host.
			m.updateStatusHover(msg)
			if m.hoverIcon != actNone || m.hoverSplit != actNone {
				return m, nil
			}
			if m.Host != nil && m.Host.Motion != nil {
				return m, m.Host.Motion(msg)
			}
			return m, nil
		}
		if m.mouseDown {
			cmd := m.handleMouseMotion(msg)
			return m, cmd
		}
		m.updateStatusHover(msg)
	case tea.PasteMsg:
		if m.Chat {
			if m.Host != nil && m.Host.Paste != nil {
				return m, m.Host.Paste(msg.String())
			}
			return m, nil
		}
		if text := msg.String(); text != "" {
			if m.termOpen && m.termSession != nil {
				_, _ = m.termSession.Write([]byte(strings.ReplaceAll(text, "\r\n", "\r")))
			} else {
				m.pasteInput(text)
			}
		}
	case tea.KeyPressMsg:
		if debugKeys {
			fmt.Fprintf(os.Stderr, "dmed: key %q\n", msg.String())
		}
		cmd := m.handleKey(msg)
		if debugKeys {
			m.msg = "k:" + msg.String()
		}
		if cmd != nil {
			return m, cmd
		}
	case lspCompletionMsg:
		if m.complOpen && msg.path == m.cur().path {
			if msg.err != nil {
				m.msg = "lsp: " + msg.err.Error()
				if len(m.complItems) == 0 {
					m.closeCompletion()
				}
			} else {
				m.mergeLSPCompletion(msg.items)
				if len(m.complItems) == 0 {
					m.closeCompletion()
				}
			}
		}
	case lspDiagMsg:
		abs, _ := filepath.Abs(msg.path)
		m.diags[abs] = msg.diags
		return m, waitForLSPDiag(m.diagCh)
	case lspDefinitionMsg:
		if msg.err != nil {
			m.msg = "goto def: " + msg.err.Error()
		} else if msg.loc == nil {
			m.msg = m.t("msg.no_definition")
		} else {
			m.focusOrOpen(msg.loc.Path)
			if t := m.cur(); t != nil {
				t.buf.SetCursor(msg.loc.Line, msg.loc.Col)
				t.buf.Deselect()
			}
			m.clampScroll()
		}
	case pluginStoreMsg:
		m.storeLoading = false
		if msg.err != nil {
			m.storeErr = msg.err.Error()
		} else {
			for _, it := range msg.items {
				exists := false
				for _, e := range m.storeItems {
					if e.File == it.File {
						exists = true
						break
					}
				}
				if !exists {
					m.storeItems = append(m.storeItems, it)
				}
			}
		}
	case pluginSourceMsg:
		m.pendingStoreInstall = ""
		if msg.err != nil {
			m.msg = "plugin download error: " + msg.err.Error()
		} else if m.plugins != nil {
			m.installFromSource(msg.file, msg.src)
		}
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	// Some terminal stacks send bare control bytes (Ctrl+O as 0x0f, Ctrl+C as
	// 0x03, Ctrl+Space as NUL, bare Ctrl as NUL). Normalize them into proper
	// ctrl+key messages so keybindings work and stray control bytes never reach
	// the buffer. Keep any modifiers already reported (e.g. Ctrl+Alt+letter
	// arrives as ESC + a control byte on some terminals) instead of dropping
	// them as before.
	if len(msg.Text) == 1 {
		if r := rune(msg.Text[0]); r < 32 && !c0Special(r) {
			msg = controlByteKey(r, msg.Mod)
		}
	} else if msg.Text == "" && msg.Code < 32 && !c0Special(msg.Code) {
		msg = controlByteKey(msg.Code, msg.Mod)
	}
	// Normalize Russian ЙЦУКЕН → English QWERTY for layout-independent keys.
	// Use []rune so multi-byte UTF-8 (Cyrillic) inputs don't leave trailing NULs.
	origText := msg.Text
	if src := []rune(msg.Text); len(src) > 0 {
		nr := make([]rune, len(src))
		for i, r := range src {
			nr[i] = normalizeKey(r)
		}
		msg = tea.KeyPressMsg{Code: nr[0], Text: string(nr), Mod: msg.Mod}
	} else if msg.Code != 0 {
		// Key combos (Ctrl/Alt+letter) arrive with empty Text on many
		// terminals; map their Cyrillic code to the physical QWERTY key too so
		// "ctrl+в" (Russian layout) still matches "ctrl+d".
		msg.Code = normalizeKey(msg.Code)
	}
	s := msg.String()
	// Some terminal stacks bundle the letter as Text even for Ctrl/Alt chords
	// (notably the Windows Console API on non-US keyboard layouts, where a
	// pressed Ctrl+<physical key> arrives with the Cyrillic Code/Text). String()
	// returns that Text verbatim and drops the modifier, so such a chord would
	// match the plain letter — and fall through to the buffer, typing "g" after
	// Ctrl+G. Rebuild the key name from the modifiers instead (uv's Keystroke)
	// whenever Ctrl or Alt is involved; Shift-only events keep their Text form
	// so the bare "G" ⇄ "g" top/bottom distinction is preserved.
	if (msg.Mod&(tea.ModCtrl|tea.ModAlt)) != 0 && msg.Text != "" {
		s = msg.Keystroke()
	}
	// Restore original text so text-input handlers (chat, search, prompt,
	// etc.) receive the actual typed characters instead of the normalized
	// English equivalents used only for keybinding matching.
	msg.Text = origText

	// The PTY owns input while the panel is focused. Alt+T closes the panel
	// from anywhere; clicking the editor takes focus back, so the editor keeps
	// working while the terminal stays open.
	if m.termOpen {
		if s == "alt+t" {
			m.termOpen = false
			return nil
		}
		if m.termFocus {
			return m.handleTerm(msg)
		}
	}

	// Chat mode: the host owns the main area. The panel toggles stay live so
	// the workspace chrome is driven the same way in both modes; a focused
	// panel keeps its keys; everything else — typing, enter, esc — belongs
	// to the chat.
	if m.Chat {
		switch s {
		case "ctrl+b":
			m.toggleTree()
			return nil
		case "alt+t":
			return m.toggleTerminal()
		case "ctrl+g":
			if m.gitOpen {
				m.gitOpen = false
				m.gitFocus = false
				m.msg = ""
			} else {
				m.openGitPanel()
			}
			return nil
		}
		if m.treeFocus {
			return m.handleTree(msg)
		}
		if m.gitOpen && m.gitFocus {
			return m.handleGit(msg)
		}
		if m.Host != nil && m.Host.Key != nil {
			return m.Host.Key(msg)
		}
		return nil
	}

	// JetBrains-style double Shift opens the palette ("search everywhere").
	// Bare modifier presses are only reported by terminals with the Kitty
	// keyboard protocol / Windows Console API; elsewhere this is a no-op.
	if !msg.IsRepeat && (msg.Code == tea.KeyLeftShift || msg.Code == tea.KeyRightShift) {
		now := time.Now()
		if now.Sub(m.lastShiftTime) <= doubleShiftInterval && !m.lastShiftTime.IsZero() {
			m.lastShiftTime = time.Time{}
			m.startPalette()
			return nil
		}
		m.lastShiftTime = now
		return nil
	}

	// While the completion popup is open, navigation keys control it.
	if m.complOpen && m.handleCompletionKey(s) {
		return nil
	}

	switch s {
	case "ctrl+q":
		return m.requestQuit()
	case "ctrl+c":
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			writeClipboardText(m.clipboard)
			m.msg = m.t("msg.copied")
			return nil
		}
		return m.requestQuit()
	case "ctrl+w":
		return m.closeActiveTab()
	case "ctrl+x":
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			writeClipboardText(m.clipboard)
			m.cur().buf.DeleteSelection()
			m.msg = m.t("msg.cut")
			return nil
		}
		return m.closeActiveTab()
	case "ctrl+p", "ctrl+shift+p", "f2":
		m.startPalette()
		return nil
	case "ctrl+t":
		m.startPrompt()
		return nil
	case "alt+t":
		if cmd := m.toggleTerminal(); cmd != nil {
			return cmd
		}
		return nil
	case "ctrl+o":
		m.startFinder()
		return nil
	case "f3":
		m.updateSearchMatches(true)
		return nil
	case "shift+f3":
		m.findPrev()
		return nil
	case "ctrl+f":
		m.startSearch()
		return nil
	case "ctrl+h":
		m.startReplace()
		return nil
	case "ctrl+g":
		if m.gitOpen {
			m.gitOpen = false
			m.gitFocus = false
			m.msg = ""
		} else {
			m.openGitPanel()
		}
		return nil
	case "f1":
		m.helpOpen = !m.helpOpen
		if m.helpOpen {
			m.helpScroll = 0
		}
		return nil
	case "ctrl+e":
		// In the embedded workspace ctrl+e is the toggle: the key that opened
		// the editor sends it back to the chat, and help left open behind the
		// transcript would resurface on the next visit — so it folds on the
		// way out. Standalone, the key keeps its help binding next to F1.
		if m.Embed {
			m.helpOpen = false
			return func() tea.Msg { return ToggleEditorMsg{} }
		}
		m.helpOpen = !m.helpOpen
		if m.helpOpen {
			m.helpScroll = 0
		}
		return nil
	case "ctrl+b", "f9":
		m.toggleTree()
		return nil
	case "f12":
		return m.gotoDefinition()
	case "alt+m":
		m.toggleBookmarkAt(m.cur().buf.CurLine())
		return nil
	case "alt+n":
		m.jumpBookmark(1)
		return nil
	case "alt+shift+n":
		m.jumpBookmark(-1)
		return nil
	}
	if m.conflictOpen {
		switch s {
		case "r", "R":
			for i := range m.tabs {
				t := &m.tabs[i]
				absT, _ := filepath.Abs(t.path)
				if absT == m.conflictPath || t.path == m.conflictPath {
					if data, err := os.ReadFile(t.path); err == nil {
						t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
						t.syntaxCached = nil
						t.diffText = ""
						m.msg = m.t("msg.reloaded")
					}
					break
				}
			}
			m.conflictOpen = false
			m.conflictRows = nil
			return nil
		case "i", "I", "esc":
			m.conflictOpen = false
			m.conflictRows = nil
			m.msg = m.t("msg.kept")
			return nil
		case "up", "k":
			if m.conflictOffY > 0 {
				m.conflictOffY--
			}
		case "down", "j":
			if m.conflictOffY < len(m.conflictRows)-1 {
				m.conflictOffY++
			}
		case "pgup":
			m.conflictOffY -= m.paneViewHeight(m.activePane) / 2
			if m.conflictOffY < 0 {
				m.conflictOffY = 0
			}
		case "pgdown":
			m.conflictOffY += m.paneViewHeight(m.activePane) / 2
			maxOff := len(m.conflictRows) - 1
			if m.conflictOffY > maxOff {
				m.conflictOffY = maxOff
			}
		case "home", "g":
			m.conflictOffY = 0
		case "end", "G":
			m.conflictOffY = len(m.conflictRows) - 1
			if m.conflictOffY < 0 {
				m.conflictOffY = 0
			}
		case "left", "h":
			m.conflictOffX -= 8
			if m.conflictOffX < 0 {
				m.conflictOffX = 0
			}
		case "right", "l":
			m.conflictOffX += 8
		}
		return nil
	}
	if m.diffViewOpen {
		return m.handleDiffView(msg)
	}
	if m.gitOpen && m.gitFocus {
		return m.handleGit(msg)
	}
	if m.paletteOpen {
		return m.handlePalette(msg)
	}
	if m.folderOpen {
		return m.handleFolderBrowser(msg)
	}
	if m.helpOpen {
		return m.handleHelp(msg)
	}
	if m.langChooserOpen {
		return m.handleLangChooser(msg)
	}
	if m.pluginStoreOpen {
		return m.handlePluginStore(msg)
	}
	if m.promptOpen {
		return m.handlePrompt(msg)
	}
	if m.promptSave {
		return m.handleSavePrompt(msg)
	}
	if m.finderOpen {
		return m.handleFinder(msg)
	}
	if m.treeFocus {
		return m.handleTree(msg)
	}
	if m.searchOpen {
		if m.replaceOpen {
			return m.handleReplace(msg)
		}
		return m.handleSearch(msg)
	}
	if m.gotoOpen {
		return m.handleGoto(msg)
	}
	if m.quitConfirm {
		return m.handleQuitConfirm(msg)
	}
	if len(s) == 5 && strings.HasPrefix(s, "alt+") && s[4] >= '1' && s[4] <= '9' {
		m.jumpTab(int(s[4] - '1'))
		return nil
	}
	// Plugins get first crack at unhandled keys so they can override built-ins.
	if m.plugins != nil && m.plugins.HasBinding(s) {
		if m.plugins.RunBinding(m, s) {
			return nil
		}
	}
	switch s {
	case "ctrl+space":
		return m.triggerCompletion(true)
	case "alt+d":
		b := m.cur().buf
		if !b.AddNextOccurrence() {
			m.msg = m.t("msg.no_more_occurrences")
		} else if !b.HasMultipleCursors() && b.HasSelection() {
			m.msg = m.t("msg.selected")
		} else {
			m.msg = ""
		}
	case "esc":
		m.cur().buf.ClearCursors()
		m.msg = ""
	case "ctrl+s":
		t := m.cur()
		if t.path == "" {
			m.startSavePrompt()
		} else {
			m.saveActive()
		}
	case "ctrl+l":
		m.startGotoPrompt()
	case "ctrl+/", "ctrl+_":
		m.toggleComment()
	case "alt+z":
		m.toggleWordWrap()
	case "alt+b":
		return m.toggleBlame()
	case "ctrl+z":
		if m.cur().buf.Undo() {
			m.msg = ""
		}
	case "ctrl+y":
		m.cur().buf.DeleteLine()
		m.msg = ""
	case "ctrl+d":
		m.cur().buf.DuplicateLine()
		m.msg = ""
	case "ctrl+u":
		m.uppercaseActive()
		return nil
	case "ctrl+r":
		if m.cur().buf.Redo() {
			m.msg = ""
		}
	case "alt+[":
		m.jumpHunk(-1)
	case "alt+]":
		m.jumpHunk(1)
	case "ctrl+\\", "f6":
		m.toggleSplitVert()
	case "ctrl+alt+h", "f7":
		m.toggleSplitHoriz()
	case "ctrl+alt+p", "f8":
		m.focusOtherPane()
	case "ctrl+alt+w":
		m.closePane()
	case "ctrl+v":
		if sysClip, err := readClipboardText(); err == nil && sysClip != "" {
			m.clipboard = normalizePaste(sysClip)
		}
		if m.clipboard != "" {
			if m.cur().buf.HasMultipleCursors() {
				m.cur().buf.MultiInsertText(m.clipboard)
			} else {
				m.cur().buf.InsertText(m.clipboard)
			}
			m.msg = ""
		}
		return nil
	case "alt+left":
		m.switchTab(-1)
	case "alt+right":
		m.switchTab(1)
	case "alt+up":
		m.cur().buf.MoveLineUp()
	case "alt+down":
		m.cur().buf.MoveLineDown()
	case "alt+shift+down":
		m.cur().buf.DuplicateLine()
	case "alt+shift+up":
		m.cur().buf.DuplicateLineUp()
	case "shift+up":
		m.cur().buf.MoveUpWithSelect()
	case "shift+down":
		m.cur().buf.MoveDownWithSelect()
	case "shift+left":
		m.cur().buf.MoveLeftWithSelect()
	case "shift+right":
		m.cur().buf.MoveRightWithSelect()
	case "shift+home":
		m.cur().buf.LineStartWithSelect()
	case "shift+end":
		m.cur().buf.LineEndWithSelect()
	case "up":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllUp()
		} else {
			m.cur().buf.MoveUp()
		}
	case "down":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllDown()
		} else {
			m.cur().buf.MoveDown()
		}
	case "left":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllLeft()
		} else {
			m.cur().buf.MoveLeft()
		}
	case "right":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllRight()
		} else {
			m.cur().buf.MoveRight()
		}
	case "home":
		m.cur().buf.LineStart()
	case "end":
		m.cur().buf.LineEnd()
	case "pgup":
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() > 0; i++ {
			t.buf.MoveUp()
		}
	case "pgdown":
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() < t.buf.LineCount()-1; i++ {
			t.buf.MoveDown()
		}
	case "enter":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiNewline()
		} else {
			m.cur().buf.InsertNewline()
		}
		m.msg = ""
	case "backspace":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiBackspace()
		} else {
			m.cur().buf.Backspace()
		}
		m.msg = ""
		return m.triggerCompletion(false)
	case "delete":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiDelete()
		} else {
			m.cur().buf.Delete()
		}
		m.msg = ""
	case "tab":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiInsertRune('\t')
		} else {
			m.cur().buf.Insert('\t')
		}
		m.msg = ""
	default:
		if len(msg.Text) > 0 {
			if m.cur().buf.HasMultipleCursors() {
				m.cur().buf.MultiInsertText(msg.Text)
			} else {
				for _, r := range msg.Text {
					m.cur().buf.Insert(r)
				}
			}
			m.msg = ""
			return m.triggerCompletion(false)
		} else {
			return nil
		}
	}
	return nil
}

func (m *Model) requestQuit() tea.Cmd {
	if m.hasDirty() {
		m.quitConfirm = true
		return nil
	}
	return m.quit()
}

// CloseEditorMsg is what an embedded editor produces on exit instead of
// tea.Quit — the parent program decides what "closed" means.
type CloseEditorMsg struct{}

// ToggleEditorMsg asks the host to switch the workspace between the chat and
// the editor. The editor cannot do it itself: the mode lives on the host side
// (Chat is the flag that decides what the main area renders), so the icon
// reports the request and the host answers it.
//
// It is one message for both directions on purpose — the icon is a toggle, and
// making each direction its own type would have the host answer a question it
// can already read off Chat.
type ToggleEditorMsg struct{}

// Host is the chat side of an embedded editor. In chat mode the workspace
// chrome renders around a main area the host owns: these callbacks are what
// the host's transcript, input line and overlays are reached through.
type Host struct {
	// Key gets every key no panel owns: typing, enter, esc, the chat's own
	// hotkeys and mode toggles.
	Key func(msg tea.KeyPressMsg) tea.Cmd
	// Click, Wheel, Motion and Release get the mouse events that land in the
	// main area, untranslated — the host maps them onto its own layout.
	Click   func(msg tea.MouseClickMsg) tea.Cmd
	Wheel   func(msg tea.MouseWheelMsg) tea.Cmd
	Motion  func(msg tea.MouseMotionMsg) tea.Cmd
	Release func(msg tea.MouseReleaseMsg) tea.Cmd
	// Paste gets the text a bracketed paste delivered while the chat input
	// had the focus.
	Paste func(text string) tea.Cmd
	// View renders the main area at the editor's geometry: main-area width
	// and height, without the chrome.
	View func(width, height int) []string
	// ChangedFiles lists the files the agent edited, so entering the editor
	// opens a tab for each one the workspace does not show yet.
	ChangedFiles func() []string
}

// OwnsMsg reports whether the message is one of the editor's own lifecycle
// events — terminal output, file watches, LSP diagnostics, git transfers —
// that the embedded editor consumes no matter which mode is on screen. The
// host routes these to the editor even in chat mode, or the chains that keep
// them coming (waitForTermOutput and friends) die and the panels freeze.
func OwnsMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case terminalOutputMsg, terminalExitMsg, FileChangedMsg, lspDiagMsg,
		lspCompletionMsg, lspDefinitionMsg, gitTransferMsg, gitBlameMsg,
		pluginStoreMsg, pluginSourceMsg:
		return true
	}
	return false
}

// MainArea returns the screen origin and size of the main area: the region
// the host renders into and maps its mouse coordinates against. The origin is
// the top-left cell of the main area on the physical screen.
func (m Model) MainArea() (x, y, w, h int) {
	return m.leftRailWidth(), 1, m.editorAreaWidth(), m.viewHeight()
}

// OpenChangedTabs opens a tab for every file the host reports the agent as
// changed, focusing the one already open. It is called on entering the editor
// from the chat, so the workspace shows what the last turns touched.
func (m *Model) OpenChangedTabs() {
	if m.Host == nil || m.Host.ChangedFiles == nil {
		return
	}
	for _, p := range m.Host.ChangedFiles() {
		if p != "" {
			m.focusOrOpen(p)
		}
	}
}

// OpenAt focuses rawPath and puts its cursor on the given 1-based line,
// opening a tab for it when the workspace does not already show it. It reports
// whether there was a file to go to.
//
// The path is relative to the workspace root, which is the form the chat's change
// blocks carry: a header names the file the way the agent spelled it to the
// tool, not as an absolute path.
//
// Two decisions worth keeping. A path that is not there is refused rather than
// opened, because a missing file would otherwise leave an empty tab and a
// "new file" message for something the user asked to look at, not to create. And
// a line past the end of the file is clamped to the last one rather than
// refused: the numbers in a change block come from the text a write produced,
// and a file edited by hand since is shorter than the block claims — landing on
// the end beats refusing to move at all.
func (m *Model) OpenAt(rawPath string, line int) bool {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return false
	}
	path := normalizePath(m.baseDir(), rawPath)
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return false
	}
	m.focusOrOpen(path)
	t := m.cur()
	if t == nil {
		return false
	}
	if line > 0 {
		if n := t.buf.LineCount(); n > 0 && line > n {
			line = n
		}
		t.buf.SetCursor(line-1, 0)
	}
	m.clampScroll()
	return true
}

// CloseAllTabs drops every tab whose buffer is clean — the tabs a session of
// agent edits accumulated, which a new conversation has no use for. A dirty
// buffer is the user's own unsaved typing, and starting another session is
// not a reason to lose it: those tabs stay open. A split collapses, because
// the panes' tab indexes do not survive the renumbering.
func (m *Model) CloseAllTabs() {
	if len(m.tabs) == 0 {
		return
	}
	kept := make([]tab, 0, len(m.tabs))
	dropped := false
	for i := range m.tabs {
		if m.tabs[i].buf.Dirty() {
			kept = append(kept, m.tabs[i])
		} else {
			dropped = true
		}
	}
	if !dropped {
		return
	}
	if len(kept) == 0 {
		// Nothing survived: back to the bare editor New starts from.
		kept = append(kept, tab{buf: buffer.New()})
	}
	m.tabs = kept
	m.initPanes()
	m.clampScroll()
}

// DropPanelFocus clears tree/git/terminal focus so the keys go back to the
// main area — what the host needs when the workspace switches to chat mode
// with a panel still focused from the last editor visit.
func (m *Model) DropPanelFocus() {
	m.treeFocus = false
	m.gitFocus = false
	m.termFocus = false
}

// ClosePanels hides every open panel — what an embedded startup does: the
// workspace exists from the first frame, but the first screen is the host's.
// The terminal's shell, not yet started, costs nothing.
func (m *Model) ClosePanels() {
	m.treeVisible = false
	m.treeFocus = false
	m.termOpen = false
	m.termFocus = false
	m.gitOpen = false
	m.gitFocus = false
	m.gitDiffFocused = false
}

// PanelState reports the workspace chrome's flags: which panels are open and
// whether the terminal owns the keys. Embedded startup and the tests read it;
// nothing writes through it.
func (m Model) PanelState() (termOpen, treeVisible, termFocus bool) {
	return m.termOpen, m.treeVisible, m.termFocus
}

// Highlighter exposes the model's syntax highlighter for a host that renders
// code of its own — dmcode's transcript colours its diff blocks with the same
// theme the editor uses. Nil before New ran.
func (m *Model) Highlighter() *syntax.Highlighter { return m.syn }

// Shutdown stops the editor's background processes (the terminal's shell
// above all) and persists the session, without ending anything else. The
// host program calls it when the whole session ends.
func (m *Model) Shutdown() {
	m.shutdown()
}

// quit tears the editor down and either ends the program (standalone run) or
// asks the parent to close it (embedded run).
func (m *Model) quit() tea.Cmd {
	m.shutdown()
	if m.Embed {
		return func() tea.Msg { return CloseEditorMsg{} }
	}
	return tea.Quit
}

// shutdown stops background processes and persists the session.
func (m *Model) shutdown() {
	m.killTerminal()
	if m.watcher != nil {
		_ = m.watcher.Close()
	}
	if m.lspClient != nil {
		m.lspClient.Close()
	}
	m.saveSession()
}

// closeActiveTab closes the tab of the active pane; closing the last tab
// quits (with a save confirmation when the buffer is dirty).
func (m *Model) closeActiveTab() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}

	// If this is the last tab, quit (with save prompt if dirty)
	if len(m.tabs) == 1 {
		if t.buf.Dirty() {
			m.quitConfirm = true
			m.pendingQuit = true
			m.quitTab = false // This is a quit, not a tab close
			return nil
		}
		return m.quit()
	}

	// Multiple tabs: check if the tab being closed is dirty
	if t.buf.Dirty() {
		m.quitTab = true
		m.pendingQuit = true
		m.quitConfirm = true
		return nil
	}

	// Tab is clean, just close it
	cmd := m.closeTab()
	if cmd == nil {
		return nil
	}
	m.shutdown()
	return cmd
}

func (m *Model) saveActive() {
	t := m.cur()
	if t.path == "" {
		m.msg = m.t("msg.cannot_save")
		return
	}
	text := t.buf.Text()
	if t.lineEnding == "crlf" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if err := os.WriteFile(t.path, []byte(text), 0o644); err != nil {
		m.msg = m.t("msg.save_failed", err.Error())
		return
	}
	t.buf.MarkSaved()
	m.msg = m.t("msg.saved")
	if m.plugins != nil {
		m.plugins.Emit(m, "save")
	}
}

func (m *Model) clampScroll() {
	if len(m.tabs) == 0 {
		return
	}
	p := m.curPane()
	t := m.cur()
	if t == nil {
		return
	}
	h := m.paneViewHeight(m.activePane)
	cur := t.buf.CurLine()
	tabW := m.cfg.Editor.TabWidth
	if p.wordWrap {
		w := m.paneContentWidth(m.activePane)
		if w > 0 && h > 0 {
			segs := t.tabWrap(w, tabW)
			expCol := visCol(t.buf.LineAt(cur), t.buf.Col(), tabW)
			row, _ := segRowForCol(segs, cur, expCol)
			if row < p.offsetY {
				p.offsetY = row
			}
			if row >= p.offsetY+h {
				p.offsetY = row - h + 1
			}
			if maxOff := len(segs) - h; p.offsetY > maxOff {
				p.offsetY = maxOff
			}
			if p.offsetY < 0 {
				p.offsetY = 0
			}
		}
		p.offsetX = 0
		return
	}
	if h > 0 {
		if cur < p.offsetY {
			p.offsetY = cur
		}
		if cur >= p.offsetY+h {
			p.offsetY = cur - h + 1
		}
	}
	w := m.paneContentWidth(m.activePane)
	if w <= 0 {
		return
	}
	x := visCol(t.buf.LineAt(cur), t.buf.Col(), tabW)
	if x < p.offsetX {
		p.offsetX = x
	}
	if x >= p.offsetX+w {
		p.offsetX = x - w + 1
	}
}

func (m *Model) startSearch() {
	m.searchOpen = true
	m.replaceOpen = false
	m.replaceFocusFind = true
	if len(m.searchQuery) > 0 {
		m.updateSearchMatches(false)
	}
}

func (m *Model) startReplace() {
	m.searchOpen = true
	m.replaceOpen = true
	m.replaceFocusFind = false
	if len(m.searchQuery) > 0 {
		m.updateSearchMatches(false)
	}
}

func (m *Model) handleSearch(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.searchOpen = false
		m.replaceOpen = false
		m.msg = ""
	case "enter", "f3", "down", "ctrl+n":
		if len(m.searchQuery) > 0 {
			m.updateSearchMatches(true)
		}
	case "shift+f3", "up", "ctrl+p":
		if len(m.searchQuery) > 0 {
			m.findPrev()
		}
	case "ctrl+h":
		m.startReplace()
	case "backspace":
		if n := len(m.searchQuery); n > 0 {
			m.searchQuery = m.searchQuery[:n-1]
			m.updateSearchMatches(false)
		}
	default:
		if len(msg.Text) > 0 {
			m.searchQuery = append(m.searchQuery, []rune(msg.Text)...)
			m.updateSearchMatches(false)
		}
	}
	return nil
}

func (m *Model) handleReplace(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.searchOpen = false
		m.replaceOpen = false
		m.msg = ""
	case "tab":
		m.replaceFocusFind = !m.replaceFocusFind
	case "ctrl+a":
		m.doReplaceAll()
	case "enter":
		if m.replaceFocusFind {
			m.updateSearchMatches(true)
		} else {
			m.doReplace()
		}
	case "f3", "down", "ctrl+n":
		m.updateSearchMatches(true)
	case "shift+f3", "up", "ctrl+p":
		m.findPrev()
	case "backspace":
		if m.replaceFocusFind {
			if n := len(m.searchQuery); n > 0 {
				m.searchQuery = m.searchQuery[:n-1]
				m.updateSearchMatches(false)
			}
		} else {
			if n := len(m.replaceWith); n > 0 {
				m.replaceWith = m.replaceWith[:n-1]
			}
		}
	default:
		if len(msg.Text) > 0 {
			if m.replaceFocusFind {
				m.searchQuery = append(m.searchQuery, []rune(msg.Text)...)
				m.updateSearchMatches(false)
			} else {
				m.replaceWith = append(m.replaceWith, []rune(msg.Text)...)
			}
		}
	}
	return nil
}

type searchMatch struct {
	line int
	col  int
}

// startGotoPrompt opens the "Go to Line" input (empty; type N, N:C, or +N/-N).
func (m *Model) startGotoPrompt() {
	m.gotoOpen = true
	m.gotoIn = nil
}

// handleGoto handles input in the "Go to Line" prompt. Accepts absolute
// (N, or N:C) and relative (+N/-N, or +N:C/-N:C) line specs.
func (m *Model) handleGoto(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.gotoOpen = false
		m.msg = ""
	case "enter":
		s := strings.TrimSpace(string(m.gotoIn))
		m.gotoOpen = false
		m.msg = ""
		if s != "" {
			m.applyGoto(s)
		}
	case "backspace":
		if n := len(m.gotoIn); n > 0 {
			m.gotoIn = m.gotoIn[:n-1]
		}
	case "ctrl+l":
		m.gotoIn = nil
	default:
		if len(msg.Text) > 0 {
			for _, r := range msg.Text {
				if (r >= '0' && r <= '9') || r == ':' || r == '+' || r == '-' {
					m.gotoIn = append(m.gotoIn, r)
				}
			}
		}
	}
	return nil
}

// applyGoto jumps the active buffer's cursor to the parsed line spec.
func (m *Model) applyGoto(s string) {
	t := m.cur()
	cur := t.buf.CurLine()

	rel := false
	sign := 1
	rest := s
	switch rest[0] {
	case '+':
		rel = true
		rest = rest[1:]
	case '-':
		rel = true
		sign = -1
		rest = rest[1:]
	}

	col := 0
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		col, _ = strconv.Atoi(rest[i+1:])
		if col < 0 {
			col = 0
		}
		rest = rest[:i]
	}
	if len(rest) == 0 {
		return
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return
	}
	line := n - 1
	if rel {
		line = cur + sign*n
	}
	if line < 0 {
		line = 0
	}
	if line >= t.buf.LineCount() {
		line = t.buf.LineCount() - 1
	}
	t.buf.SetCursor(line, 0)
	if col > 0 {
		if col > t.buf.LineLen(line) {
			col = t.buf.LineLen(line)
		}
		t.buf.SetCursor(line, col)
	}
	m.clampScroll()
	m.msg = fmt.Sprintf("line %d", line+1)
}

// toggleComment comments or uncomments the current line(s) using the comment
// syntax of the active file type (from the syntax highlighter's lexer), then
// moves the cursor to the next line.
func (m *Model) toggleComment() {
	t := m.cur()
	prefix, suffix := syntax.CommentTokens(t.path, t.buf.Text())
	if prefix == "" {
		m.msg = m.t("msg.no_comment")
		return
	}
	t.buf.ToggleComment(prefix, suffix)
	m.msg = ""
	t.buf.MoveDown()
	m.clampScroll()
}

func (m *Model) toggleWordWrap() {
	p := m.curPane()
	p.wordWrap = !p.wordWrap
	if p.wordWrap {
		p.offsetX = 0
	}
	m.clampScroll()
	if p.wordWrap {
		m.msg = m.t("msg.word_wrap_on")
	} else {
		m.msg = m.t("msg.word_wrap_off")
	}
}

func findMatchesInRunes(line []rune, query []rune) []int {
	if len(query) == 0 || len(line) < len(query) {
		return nil
	}
	var cols []int
	for i := 0; i <= len(line)-len(query); i++ {
		match := true
		for j := 0; j < len(query); j++ {
			if line[i+j] != query[j] {
				match = false
				break
			}
		}
		if match {
			cols = append(cols, i)
			i += len(query) - 1
		}
	}
	return cols
}

func (m *Model) allMatches() []searchMatch {
	if len(m.searchQuery) == 0 {
		return nil
	}
	t := m.cur()
	var matches []searchMatch
	for ln := 0; ln < t.buf.LineCount(); ln++ {
		cols := findMatchesInRunes(t.buf.LineAt(ln), m.searchQuery)
		for _, col := range cols {
			matches = append(matches, searchMatch{line: ln, col: col})
		}
	}
	return matches
}

func (m *Model) updateSearchMatches(jumpToNext bool) {
	matches := m.allMatches()
	m.searchTotalMatches = len(matches)
	if len(matches) == 0 {
		m.searchMatchIdx = -1
		return
	}
	t := m.cur()
	curLine := t.buf.CurLine()
	curCol := t.buf.Col()

	foundIdx := 0
	for i, mPos := range matches {
		if mPos.line > curLine || (mPos.line == curLine && mPos.col >= curCol) {
			foundIdx = i
			break
		}
	}
	if jumpToNext && m.searchMatchIdx >= 0 {
		foundIdx = (m.searchMatchIdx + 1) % len(matches)
	}
	m.searchMatchIdx = foundIdx
	target := matches[foundIdx]
	t.buf.SetCursor(target.line, target.col)
}

func (m *Model) findPrev() {
	matches := m.allMatches()
	m.searchTotalMatches = len(matches)
	if len(matches) == 0 {
		m.searchMatchIdx = -1
		return
	}
	if m.searchMatchIdx <= 0 {
		m.searchMatchIdx = len(matches) - 1
	} else {
		m.searchMatchIdx--
	}
	target := matches[m.searchMatchIdx]
	m.cur().buf.SetCursor(target.line, target.col)
}

func (m *Model) doReplace() {
	if len(m.searchQuery) == 0 {
		return
	}
	t := m.cur()
	matches := m.allMatches()
	if len(matches) == 0 {
		return
	}
	curLine := t.buf.CurLine()
	curCol := t.buf.Col()
	qLen := len(m.searchQuery)

	onMatch := false
	for _, mPos := range matches {
		if mPos.line == curLine && mPos.col == curCol {
			onMatch = true
			break
		}
	}
	if !onMatch {
		m.updateSearchMatches(false)
		return
	}

	t.buf.ReplaceRange(curLine, curCol, qLen, m.replaceWith)
	m.msg = m.t("msg.replaced_one")
	m.updateSearchMatches(false)
}

func (m *Model) doReplaceAll() {
	if len(m.searchQuery) == 0 {
		return
	}
	t := m.cur()
	count := t.buf.ReplaceAll(string(m.searchQuery), string(m.replaceWith))
	m.msg = fmt.Sprintf("replaced %d occurrence(s)", count)
	m.searchOpen = false
	m.replaceOpen = false
}

func (m *Model) jumpHunk(dir int) {
	t := m.cur()
	if t.path == "" {
		return
	}
	diff := t.getDiff(m.repo)
	if len(diff.Hunks) == 0 {
		m.msg = m.t("msg.no_git_changes")
		return
	}
	curLine := t.buf.CurLine()
	if dir > 0 {
		for _, h := range diff.Hunks {
			if h.StartLine > curLine {
				t.buf.SetCursor(h.StartLine, 0)
				m.msg = fmt.Sprintf("git hunk: lines %d-%d", h.StartLine+1, h.EndLine+1)
				return
			}
		}
		t.buf.SetCursor(diff.Hunks[0].StartLine, 0)
		m.msg = fmt.Sprintf("git hunk: lines %d-%d", diff.Hunks[0].StartLine+1, diff.Hunks[0].EndLine+1)
	} else {
		for i := len(diff.Hunks) - 1; i >= 0; i-- {
			h := diff.Hunks[i]
			if h.StartLine < curLine {
				t.buf.SetCursor(h.StartLine, 0)
				m.msg = fmt.Sprintf("git hunk: lines %d-%d", h.StartLine+1, h.EndLine+1)
				return
			}
		}
		last := diff.Hunks[len(diff.Hunks)-1]
		t.buf.SetCursor(last.StartLine, 0)
		m.msg = fmt.Sprintf("git hunk: lines %d-%d", last.StartLine+1, last.EndLine+1)
	}
}

func (m *Model) startPalette() {
	m.paletteOpen = true
	m.paletteQ = nil
	m.paletteSel = 0
	m.paletteOffset = 0
	// Unfocus all panels so the palette receives keystrokes (the palette
	// is lower priority than git in the mode-guard chain).
	m.gitFocus = false
	m.treeFocus = false
}

// setLang switches the interface language, rebuilding the translator and
// persisting the choice to the config file (project if available, else global).
func (m *Model) setLang(lang string) {
	m.cfg.UI.Lang = lang
	m.tr = i18n.New(i18n.Resolve(lang))
	path := config.ProjectConfigPath(m.root)
	if path == "" {
		path = config.ConfigPath()
	}
	_ = config.WriteLang(path, lang)
	m.msg = m.t("msg.lang_set", lang)
}

// openLangChooser opens the language selection list.
func (m *Model) openLangChooser() {
	m.langChooserOpen = true
	m.langChooserSel = 0
	m.paletteOpen = false
}

// handleLangChooser handles keys while the language chooser is open.
func (m *Model) handleLangChooser(msg tea.KeyPressMsg) tea.Cmd {
	langs := i18n.Supported()
	switch msg.String() {
	case "esc":
		m.langChooserOpen = false
	case "enter":
		if m.langChooserSel >= 0 && m.langChooserSel < len(langs) {
			m.setLang(langs[m.langChooserSel].Code)
		}
		m.langChooserOpen = false
	case "up", "k":
		if len(langs) > 0 {
			m.langChooserSel = (m.langChooserSel - 1 + len(langs)) % len(langs)
		}
	case "down", "j":
		if len(langs) > 0 {
			m.langChooserSel = (m.langChooserSel + 1) % len(langs)
		}
	}
	return nil
}

// restoreCursors applies saved per-file cursor positions to the just-opened
// tabs. Positions are clamped by SetCursor, so stale values are harmless.
func (m *Model) restoreCursors(cursors map[string]session.CursorPos) {
	for _, t := range m.tabs {
		if c, ok := cursors[t.path]; ok {
			t.buf.SetCursor(c.Line, c.Col)
		}
	}
}

func (m *Model) saveSession() {
	if m.Embed {
		// The editor's own session file is a standalone-editor feature. The
		// host decides what is open, and a host that never restores would
		// otherwise leave a stale tab list behind for a standalone run.
		return
	}
	var files []string
	cursors := map[string]session.CursorPos{}
	for _, t := range m.tabs {
		if t.path != "" {
			files = append(files, t.path)
			cursors[t.path] = session.CursorPos{Line: t.buf.CurLine(), Col: t.buf.Col()}
		}
	}
	if len(files) == 0 {
		return
	}
	sess := session.SessionState{
		Root:       m.root,
		Files:      files,
		ActiveTab:  m.activeTabIndex(),
		Layout:     int(m.layout),
		ActivePane: m.activePane,
		Cursors:    cursors,
	}
	_ = session.Save(session.DefaultPath(m.root), sess)
}

func (m *Model) handleMouseMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if !m.mouseDown {
		return nil
	}
	if m.dragTerm {
		row := msg.Y - m.termStartRow() - 1
		if row >= 0 && row < len(m.termRows) {
			m.extendTermSelection(row, msg.X)
		}
		return nil
	}
	y := msg.Y
	x := msg.X

	h := m.viewHeight()
	if y < 1 {
		y = 1
	}
	if y > h {
		y = h
	}

	editorRow := y - 1
	ln, rawCol := m.clickPosToLineCol(m.activePane, editorRow, x)

	m.cur().buf.DragSelect(ln, rawCol)
	return nil
}

// updateStatusHover tracks which status-bar icon the cursor is over so the
// hovered cell can highlight and a callout can be drawn. Called for motion
// events with no button pressed (requires MouseModeAllMotion).
func (m *Model) updateStatusHover(msg tea.MouseMotionMsg) {
	m.updateSplitHover(msg)
	if m.statusIconsVisible() && msg.Y == m.statusBarRow() {
		m.hoverIcon = m.statusIconAt(msg.X)
		return
	}
	m.hoverIcon = actNone
}

// expandedToRawCol converts an expanded (tab-expanded) column back to a raw
// character index, reversing the tab expansion done during rendering.
func expandedToRawCol(line []rune, expCol, tabWidth int) int {
	return expandedToRaw(line, expCol, tabWidth)
}

func expandedToRaw(line []rune, expCol, tabWidth int) int {
	col := 0
	for i, r := range line {
		if r == '\t' {
			next := ((col / tabWidth) + 1) * tabWidth
			if expCol < next {
				return i
			}
			col = next
		} else {
			if expCol <= col {
				return i
			}
			col++
		}
	}
	return len(line)
}

// cursorScreenPos returns the (x, y) position of the editor cursor in
// the terminal content, accounting for the tab bar, left rail, gutter,
// scroll offsets, word wrap, and split layout.
func (m Model) cursorScreenPos() (int, int) {
	p := m.curPane()
	t := &m.tabs[p.tabIdx]
	curLine := t.buf.CurLine()
	curRawCol := t.buf.Col()

	// Expand cursor column for tabs.
	raw := t.buf.LineAt(curLine)
	expCol := 0
	for i := 0; i < curRawCol && i < len(raw); i++ {
		if raw[i] == '\t' {
			expCol += m.cfg.Editor.TabWidth
		} else {
			expCol++
		}
	}

	gw := m.gutterWidthForTab(t)
	leftW := m.leftRailWidth()

	screenRow := curLine
	segStart := 0
	if p.wordWrap {
		w := m.paneContentWidth(m.activePane)
		if w > 0 {
			segs := t.tabWrap(w, m.cfg.Editor.TabWidth)
			row, es := segRowForCol(segs, curLine, expCol)
			screenRow = row
			segStart = es
		}
	}

	// X position within the pane content area.
	paneX := gw + (expCol - (p.offsetX + segStart))

	// Determine the screen X based on which pane we're in.
	var screenX int
	switch m.layout {
	case splitVert:
		if m.activePane == 0 {
			screenX = leftW + paneX
		} else {
			screenX = leftW + m.paneTotalWidth(0) + 1 + paneX
		}
	default:
		screenX = leftW + paneX
	}

	// Y position: tab bar (1 row) + line offset within the pane.
	screenY := 1 + (screenRow - p.offsetY)

	// For horizontal split, the second pane starts lower.
	if m.layout == splitHoriz && m.activePane == 1 {
		screenY += m.paneViewHeight(0) + 1 // +1 for separator
	}

	// Clamp to valid range.
	if screenX < 0 {
		screenX = 0
	}
	if screenY < 0 {
		screenY = 0
	}
	return screenX, screenY
}
