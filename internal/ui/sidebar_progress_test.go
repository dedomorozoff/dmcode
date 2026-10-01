package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The progress note describes the turn in flight, so it is shown in the
// transcript flow — right under the last message, scrolling with it — and the
// status bar keeps only the badges while the turn runs.
func TestProgressShowsUnderTheLastMessage(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.busy = true
	m.syncVP()
	// The viewport pads short content to its height, so the tail is read from
	// the content with the padding trimmed off.
	tail := strings.Split(strings.TrimRight(ansi.Strip(m.vp.View()), " \n"), "\n")
	tailRow := strings.TrimRight(tail[len(tail)-1], " ")

	// The note is the tail of the content: the last row names the call in
	// flight, and the row above it is the last transcript row, not padding.
	if !strings.Contains(tailRow, "run_command_with_a_really_long_name") {
		t.Errorf("the note is not the last row of the transcript: %q", tailRow)
	}
	if !strings.Contains(tail[len(tail)-2], "session reset") {
		t.Errorf("the note does not sit under the last message: %q", tail[len(tail)-2])
	}
	if got := ansi.Strip(m.statusBarView()); strings.Contains(got, "run_command") {
		t.Errorf("status bar %q repeats the progress note; the transcript already shows it", got)
	}

	m.busy = false
	m.syncVP()
	if strings.Contains(ansi.Strip(m.vp.View()), "run_command_with_a_really_long_name") {
		t.Error("the note outlived the turn it described")
	}
}

// The header and the sidebar say the same things, so only one of them is on
// screen at a time.
func TestHeaderYieldsToTheSidebar(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini") // showSidebar: true, wide enough
	if m.sidebarVisible() {
		if m.headerVisible() {
			t.Error("the header is up next to the sidebar that shows the same facts")
		}
		// The header is the top two rows of the frame when it is shown, so
		// with it gone those rows belong to the panel and the sidebar. The
		// logo in the transcript also says "dmcode", so the frame as a whole
		// proves nothing — the top rows do.
		for i, l := range strings.Split(m.View().Content, "\n")[:2] {
			if strings.Contains(ansi.Strip(l), m.sessionID) || strings.Contains(ansi.Strip(l), m.prov.Model) {
				t.Errorf("top row %d carries header facts the sidebar already shows: %q", i, ansi.Strip(l))
			}
		}
	}

	m.showSidebar = false
	if !m.headerVisible() {
		t.Error("with the sidebar hidden the header is the only place the model and session are shown")
	}
}

// A sidebar too tall for its box loses the hotkeys first and keeps the plan.
func TestSidebarDropsHotkeysBeforeThePlan(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	// A height that fits nothing like the whole body: deep in the trimmed case.
	tight := m.sidebarView(8)
	plain := ansi.Strip(tight)
	if strings.Contains(plain, "HOTKEYS") {
		t.Error("a tight sidebar still spends rows on the hotkeys")
	}
}

// The tool list wraps under its own marker instead of being cut to one row:
// the joined list of names is far wider than the panel, and the old one-name-
// per-row layout both spilled past the frame and ate a row per tool.
func TestSidebarToolsWrapInsteadOfTruncating(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.toolNames = []string{"read_file", "write_file", "mcp_fetch_docs", "edit_file", "grep", "glob"}
	plain := ansi.Strip(m.sidebarView(40))
	for _, name := range m.toolNames {
		if !strings.Contains(plain, name) {
			t.Errorf("tool %q is not in the sidebar; the list was cut rather than wrapped", name)
		}
	}
}

// The sidebar's brand is the startup art at half size, and half size must
// actually fit the panel it lives in.
func TestMiniLogoFitsTheSidebar(t *testing.T) {
	lines := strings.Split(miniLogo, "\n")
	if len(lines) != 3 {
		t.Fatalf("mini logo has %d rows, want 3", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 27 {
			t.Errorf("mini logo row %d is %d cells wide, does not fit the panel's inner width", i, w)
		}
	}
	if !strings.ContainsAny(miniLogo, "█▀▄") {
		t.Errorf("mini logo lost the block glyphs: %q", miniLogo)
	}
}
