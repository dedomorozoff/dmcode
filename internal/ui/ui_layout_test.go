package ui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// lipgloss counts a style's Width/Height as the total block size with borders
// included. Treating them as content sizes is what left the sidebar two rows
// short of the chat panel and every row of the frame a few columns narrower
// than the terminal, so the frame geometry is asserted rather than eyeballed.
func TestFrameFitsTerminalExactly(t *testing.T) {
	sizes := [][2]int{
		{200, 60}, {140, 45}, {120, 40}, {100, 30}, {91, 24}, {90, 24},
		{89, 24}, {80, 24}, {70, 20}, {60, 20}, {50, 15}, {40, 12}, {30, 10}, {20, 9},
	}
	Models := []string{
		"gpt-4o-mini",
		"openai/gpt-oss-120b",
		"meta-llama/Llama-3.3-70B-Instruct:free-qwq",
		"anthropic/claude-sonnet-4-20250514-thinking-extended",
	}

	for _, size := range sizes {
		w, h := size[0], size[1]
		for _, model := range Models {
			m := framedModel(w, h, model)
			lines := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
			// The frame is header + chat panel + status bar + input box, so it
			// cannot be shorter than chromeHeight rows whatever the terminal is.
			want := max(h, chromeHeight)
			if len(lines) != want {
				t.Errorf("%dx%d model %q: frame is %d rows, want %d", w, h, model, len(lines), want)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != w {
					t.Errorf("%dx%d model %q: row %d is %d cells, want %d", w, h, model, i, got, w)
				}
			}
		}
	}
}

// The chat panel and the sidebar are framed side by side, so any difference in
// their outer heights leaves one bottom border floating above the other.
func TestSidebarMatchesPanelHeight(t *testing.T) {
	for _, h := range []int{3, 5, 12, 40} {
		panel := stylePanel.Width(40).Height(h).Render("x")
		sidebar := styleSidebar.Width(sidebarBoxWidth).Height(h).Render("y")

		if gotPanel, gotSidebar := lipgloss.Height(panel), lipgloss.Height(sidebar); gotPanel != gotSidebar {
			t.Errorf("outer height %d: panel renders %d lines, sidebar %d", h, gotPanel, gotSidebar)
		}
	}
}

// The sidebar is trimmed to the exact height of the panel beside it, marked
// with an ellipsis so a cut-off panel is not read as a complete one.
func TestSidebarTrimsOverflowWithMarker(t *testing.T) {
	m := &uiModel{
		prov:      config.Provider{Model: "some/very/long/model/name", Label: "Pollinations (без ключа)"},
		sessionID: "sess-0123456789abcdef",
		workDir:   "C:/cygwin64/home/alexl/dmcode",
		toolNames: []string{"read_file", "write_file", "edit_file", "list_dir", "grep", "glob", "run_command"},
		turnCount: 3, toolCallCount: 17, lastTool: "run_command",
	}

	full := m.sidebarView(200)
	if !strings.Contains(full, "HOTKEYS") {
		t.Error("tall sidebar should show every section")
	}
	if got := lipgloss.Width(full); got != sidebarBoxWidth+sidebarGap {
		t.Errorf("sidebar is %d cells wide, want %d", got, sidebarBoxWidth+sidebarGap)
	}

	short := m.sidebarView(6)
	if got, want := lipgloss.Height(short), 6; got != want {
		t.Errorf("trimmed sidebar is %d lines, want %d", got, want)
	}
	if !strings.Contains(short, "…") {
		t.Error("trimmed sidebar should mark the cut with an ellipsis")
	}
	if strings.Contains(short, "esc     stop turn") {
		t.Error("trimmed sidebar must not keep rendering cut-off lines")
	}
}

// Long values must be cut on rune boundaries: a byte cut lands mid-rune and the
// terminal prints replacement characters.
// chdir moves into a temp directory for the duration of the test, so a test that
// writes .env cannot touch the real one in the repository.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestOneLineFlattensPastedText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-abc", "sk-abc"},
		{"sk-abc\n", "sk-abc"},
		{"sk-abc\r\n", "sk-abc"},
		{"\n  sk-abc  \n", "sk-abc"},
		{"line1\nline2", "line1line2"},
		{"key\rmore", "keymore"},
		{"", ""},
	}
	for _, c := range cases {
		if got := oneLine(c.in); got != c.want {
			t.Errorf("oneLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A key pasted into /setup must reach the buffer. It used to be dropped: the
// terminal sends tea.PasteMsg, which is not a KeyPressMsg, and the setup overlay
// only ever saw key presses.
func TestPasteReachesSetupBuffer(t *testing.T) {
	m := &uiModel{}
	m.setup = setupState{open: true, stage: setupKey}

	if got := m.pasteTarget(); got == nil {
		t.Fatal("setup key stage exposes no paste target")
	}
	m.pasteInto("gsk_from_clipboard")
	if m.setup.buf != "gsk_from_clipboard" {
		t.Errorf("pasteTarget/pasteInto left buf = %q", m.setup.buf)
	}

	// A trailing newline from a web copy must not survive: it would be written
	// into .env and break the assignment on the next load.
	m.pasteInto("\r\n")
	if m.setup.buf != "gsk_from_clipboard" {
		t.Errorf("trailing newline leaked into buf: %q", m.setup.buf)
	}
}

func TestPasteTargetPerOverlay(t *testing.T) {
	m := &uiModel{}

	// The provider list is navigated with arrows, not typed into.
	m.setup = setupState{open: true, stage: setupPick}
	if m.pasteTarget() != nil {
		t.Error("the provider list should have no paste target")
	}

	m.palette = paletteState{open: true}
	if m.pasteTarget() != &m.palette.query {
		t.Error("the palette query is not the paste target")
	}
	m.pasteInto("set")
	if m.palette.query != "set" {
		t.Errorf("palette query = %q, want %q", m.palette.query, "set")
	}
	if m.palette.selected != 0 {
		t.Error("paste into the palette must reset the cursor")
	}

	// The language list has no text field.
	m.lang = langState{open: true}
	if m.pasteTarget() != nil {
		t.Error("the language list should have no paste target")
	}

	// Nothing open: the main textinput pastes on its own.
	m = &uiModel{}
	if m.pasteTarget() != nil {
		t.Error("with no overlay open, the main input should handle the paste")
	}
}

// The whole point of the fix: Update must hand a PasteMsg to the open overlay
// instead of dropping it on the floor.
func TestUpdateRoutesPasteToOpenOverlay(t *testing.T) {
	m := &uiModel{}
	m.setup = setupState{open: true, stage: setupKey}

	_, _ = m.Update(tea.PasteMsg{Content: "sk-pasted"})

	if m.setup.buf != "sk-pasted" {
		t.Errorf("Update dropped the paste: buf = %q, want %q", m.setup.buf, "sk-pasted")
	}
}

func TestSetupLabelUsesHostName(t *testing.T) {
	// Unchanged behaviour kept as a regression guard: a custom endpoint shows
	// as "Custom endpoint" rather than an empty host.
	if got := setupLabel(config.SetupOption{}); got == "" {
		t.Error("setupLabel returned an empty label for a custom endpoint")
	}
}

// A rejected key must not reach .env. Writing it there would overwrite the
// working OPENAI_API_KEY and the user would only find out on the next message.
func TestRejectedKeyLeavesDotEnvUntouched(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	// A working configuration is already in place.
	existing := "OPENAI_BASE_URL=https://opencode.ai/zen/v1\nOPENAI_API_KEY=oc_sk_good\n"
	if err := os.WriteFile(".env", []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	opt := config.SetupOption{
		Label:   "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "deepseek/deepseek-chat-v3.1:free",
		EnvKey:  "OPENAI_API_KEY",
		API:     config.APIChat,
		Signup:  "https://openrouter.ai/keys",
	}
	m := &uiModel{}
	m.setup = setupState{open: true, stage: setupKey, opt: opt, buf: "oc_sk_wrong_provider"}

	// The endpoint rejects the key.
	_ = m.handleSetupCheck(setupCheckMsg{
		vars: config.SetupVars(opt, "oc_sk_wrong_provider"),
		opt:  opt,
		err:  errors.New("401 Unauthorized: Missing Authentication header"),
	})

	got, err := os.ReadFile(".env")
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if string(got) != existing {
		t.Errorf(".env was modified by a rejected key:\n got %q\nwant %q", got, existing)
	}

	// The wizard goes back to the key prompt, cleared, and offers a bypass.
	if m.setup.stage != setupKey {
		t.Errorf("stage = %d, want the key prompt (%d)", m.setup.stage, setupKey)
	}
	if m.setup.buf != "" {
		t.Errorf("the rejected key was kept in the buffer: %q", m.setup.buf)
	}
	if !m.setup.force {
		t.Error("a retry after a rejection should be able to save anyway")
	}
}

// The transcript must say the configuration was not written, or the user is
// left believing the provider changed.
func TestRejectedKeyExplainsNothingWasSaved(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	opt := config.SetupOption{Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENAI_API_KEY"}
	m := &uiModel{}
	m.setup = setupState{open: true, stage: setupKey, opt: opt, buf: "bad"}

	_ = m.handleSetupCheck(setupCheckMsg{
		vars: config.SetupVars(opt, "bad"),
		opt:  opt,
		err:  errors.New("401 Unauthorized"),
	})

	joined := ""
	for _, l := range m.history {
		joined += l.text + "\n"
	}
	if !strings.Contains(joined, "rejected the key") {
		t.Errorf("no mention of the rejection in:\n%s", joined)
	}
	if !strings.Contains(joined, "unchanged") {
		t.Errorf("no mention that .env was left alone in:\n%s", joined)
	}
	// The signup URL is the whole point of a failed check: it is where the
	// correct key comes from.
	if !strings.Contains(joined, opt.Signup) && opt.Signup != "" {
		t.Errorf("the signup URL was not shown:\n%s", joined)
	}
}

func TestTruncateCountsCellsNotBytes(t *testing.T) {
	got := truncate("Pollinations (без ключа)", 20)
	if n := ansi.StringWidth(got); n > 20 {
		t.Errorf("truncate returned %d cells, want at most 20", n)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("truncate split a rune: %q", got)
		}
	}
	if got := truncate("abcdef", 0); got != "" {
		t.Errorf("truncate to 0 cells = %q, want empty", got)
	}
}

// The banner is the app's one piece of branding. It must survive intact on a
// roomy terminal, and vanish whole on a narrow one rather than appearing as a
// sliced or smeared fragment.
func TestLogoArtSurvivesIntact(t *testing.T) {
	art := strings.Split(strings.TrimRight(logo, "\n"), "\n")
	widest := 0
	for _, r := range art {
		if n := ansi.StringWidth(r); n > widest {
			widest = n
		}
	}

	wide := rowRows(logo, 200, m0(kindLogo))
	if len(wide) != len(art) {
		t.Fatalf("logo at 200 cells produced %d rows, want %d", len(wide), len(art))
	}
	for i, r := range wide {
		if r != art[i] {
			t.Errorf("logo row %d was altered:\n got %q\nwant %q", i, r, art[i])
		}
	}

	// Art that cannot fit is dropped entirely: no partial banner, no re-wrap.
	for width := 8; width < widest; width++ {
		if got := rowRows(logo, width, m0(kindLogo)); got != nil {
			t.Errorf("logo at %d cells leaked a partial banner: %q", width, got)
		}
	}

	// Exactly wide enough: it must come back, whole.
	if got := rowRows(logo, widest, m0(kindLogo)); len(got) != len(art) {
		t.Errorf("logo at its exact width (%d) produced %d rows, want %d", widest, len(got), len(art))
	}
}

// Every transcript row has to fit the chat panel's inner width, gutter
// included, and only the first row may carry the gutter.
func TestTranscriptRowsFitAndHang(t *testing.T) {
	cases := []struct {
		kind lineKind
		text string
	}{
		{kindUser, "a user message long enough that it has to be broken across rows somewhere in the middle"},
		{kindTool, `read_file({"path":"internal/ui/render.go","start_line":120,"end_line":260})`},
		{kindToolRes, `{"preview":"package ui — содержимое файла с кириллицей"}`},
		{kindAgent, "first paragraph\n\nsecond paragraph that is also long enough to need breaking"},
		{kindErr, "error: the provider refused the connection and the message goes on for a while"},
		{kindSys, i18n.T("— session reset —")},
	}
	for _, width := range []int{12, 20, 34, 56, 73, 120} {
		for _, tc := range cases {
			rows := wrapIndent(tc.text, width, m0(tc.kind).first, m0(tc.kind).rest)
			if len(rows) == 0 {
				t.Fatalf("kind %d width %d: no rows produced", tc.kind, width)
			}
			for i, r := range rows {
				if got := ansi.StringWidth(r); got > width {
					t.Errorf("kind %d width %d row %d is %d cells: %q", tc.kind, width, i, got, r)
				}
				if i == 0 {
					continue
				}
				if !strings.HasPrefix(r, m0(tc.kind).rest) {
					t.Errorf("kind %d width %d row %d lost its hanging indent: %q", tc.kind, width, i, r)
				}
			}
		}
	}
}

func m0(k lineKind) transcriptRow { return (&uiModel{}).rowStyle(k) }

// A word longer than the row is broken rather than allowed to run off the edge.
func TestWrapCellsBreaksOverlongWord(t *testing.T) {
	rows := wrapCells(strings.Repeat("я", 200), 30)
	if len(rows) < 7 {
		t.Fatalf("expected the word to be split into several rows, got %d", len(rows))
	}
	for i, r := range rows {
		if n := ansi.StringWidth(r); n > 30 {
			t.Errorf("row %d is %d cells: %q", i, n, r)
		}
	}
	if got := strings.Join(rows, ""); got != strings.Repeat("я", 200) {
		t.Error("wrapping lost or duplicated content")
	}
}

// The palette and the model picker list unbounded model identifiers, so their
// frames must stay inside the terminal in both directions.
func TestOverlayPanelsFitTerminal(t *testing.T) {
	ids := []string{
		"gpt-4o-mini", "openai/gpt-oss-120b",
		"anthropic/claude-sonnet-4-20250514-thinking-extended",
		"meta-llama/Llama-3.3-70B-Instruct:free-qwq",
		"google/gemini-2.5-pro", "x/y", "z", "a-very-long-provider-name/model-id-with-a-long-suffix",
	}
	for _, size := range [][2]int{{200, 60}, {120, 40}, {90, 24}, {70, 20}, {40, 12}} {
		m := &uiModel{prov: config.Provider{Model: "gpt-4o-mini"}, width: size[0], height: size[1]}
		m.picker = modelPicker{open: true, Models: ids, selected: 3}
		m.palette = paletteState{open: true, query: "модел"}

		for name, box := range map[string]string{"picker": m.modelPickerBox(), "palette": m.paletteBox()} {
			if w := lipgloss.Width(box); w > m.width {
				t.Errorf("%s %dx%d: box is %d cells wide", name, m.width, m.height, w)
			}
			if hh := lipgloss.Height(box); hh > m.height {
				t.Errorf("%s %dx%d: box is %d rows tall", name, m.width, m.height, hh)
			}
		}
	}
}

// framedModel builds a model with content adversarial to the layout: long
// unbreakable words, Cyrillic, multi-row tool output and a busy status.
func framedModel(w, h int, model string) *uiModel {
	m := &uiModel{
		prov:        config.Provider{Model: model, Label: "Pollinations (без ключа)"},
		sessionID:   "sess-1234567890123",
		workDir:     "C:/cygwin64/home/alexl/dmcode",
		toolNames:   []string{"read_file", "write_file", "edit_file", "list_dir", "grep", "glob", "run_command"},
		turnCount:   12,
		lastTool:    "run_command_with_a_really_long_name",
		showSidebar: true,
		busy:        true,
		statusText:  i18n.T("calling: ") + "run_command_with_a_really_long_name",
	}
	m.toolCallCount = 345
	m.history = []line{
		{kindSys, ""},
		{kindLogo, logo},
		{kindUser, strings.Repeat("оченьдлинноенеразбиваемоеслово", 12)},
		{kindUser, "a normal user message that should wrap near the right edge of the chat panel"},
		{kindTool, `read_file({"path":"internal/ui/render_history.go","start_line":1,"end_line":420,"max_bytes":1048576})`},
		{kindToolRes, `{"result":"package ui — содержимое файла с кириллицей и очень длинной строкой"}`},
		{kindAgent, "Here is the answer.\n\nSecond paragraph, also long enough to be wrapped by the renderer.\n\n- пункт\n- второй пункт"},
		{kindErr, "error: something went horribly wrong with a very long error message"},
		{kindSys, i18n.T("— session reset —")},
	}
	m.suggest = []suggestion{
		{"/model " + model, i18n.T("switch to this model")},
		{"/models", i18n.T("list models")},
		{"/new", i18n.T("start a new session")},
		{"/clear", i18n.T("clear the screen")},
		{"/quit", i18n.T("leave")},
	}
	m.width, m.height = w, h
	m.layout()
	m.syncVP()
	m.followVP()
	return m
}

// TestResizeRewrapsTranscript guards the re-wrap on resize. layout() only
// recomputes the geometry; the transcript is re-wrapped by the followVP at the
// tail of Update, so an early return from the WindowSizeMsg case would silently
// leave the transcript broken for the previous terminal width. The assertions
// read the viewport, not renderHistory, because a stale transcript only ever
// lived in the viewport.
func TestResizeRewrapsTranscript(t *testing.T) {
	m := framedModel(120, 30, "gpt-4o-mini")
	m.stick = false

	for _, size := range [][2]int{{60, 20}, {100, 34}, {72, 22}, {200, 50}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		rows := m.vp.GetContent()
		for i, l := range strings.Split(strings.TrimRight(rows, "\n"), "\n") {
			if got := ansi.StringWidth(l); got > m.contentWidth() {
				t.Errorf("%dx%d: viewport row %d is %d cells, limit %d",
					size[0], size[1], i, got, m.contentWidth())
			}
		}
	}
}

// TestResizeKeepsHistory guards against a resize path that would clear or
// truncate the transcript, which is easy to introduce when re-syncing the
// viewport. Narrower widths need more rows, never fewer.
func TestResizeKeepsHistory(t *testing.T) {
	m := framedModel(120, 30, "gpt-4o-mini")
	m.stick = false
	before := m.vp.TotalLineCount()

	m.Update(tea.WindowSizeMsg{Width: 64, Height: 20})
	after := m.vp.TotalLineCount()

	if after == 0 {
		t.Fatal("transcript is empty after a resize")
	}
	if after < before {
		t.Errorf("resize 120->64 shrank the transcript from %d to %d rows", before, after)
	}
	// The last thing in the transcript must survive the resize, otherwise the
	// re-sync is dropping the tail of the conversation.
	if !strings.Contains(m.vp.GetContent(), "session reset") {
		t.Error("tail of the transcript is missing after a resize")
	}
}
