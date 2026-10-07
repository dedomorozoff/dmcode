package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/config"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// pickerModel is the /models box on a terminal of the given size, serving the
// given catalogue through the given endpoint.
func pickerModel(w, h int, baseURL string, ids ...string) *uiModel {
	m := &uiModel{prov: config.Provider{BaseURL: baseURL, Model: ""}, width: w, height: h}
	m.picker = modelPicker{open: true, Models: ids}
	return m
}

// catalogue is a listing long enough that nothing fits on one screen and
// varied enough that a row is identifiable in the frame: "model-042" is the
// forty-third one whatever window the box chose.
func catalogue(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("vendor/model-%03d", i)
	}
	return ids
}

// highlightedRow finds the frame row carrying the cursor, or "" when the cursor
// is not drawn at all. The marker is the box's own glyph rather than a row
// index, because "the row under the cursor" is the thing every one of these
// tests is actually about.
func highlightedRow(box string) string {
	for _, l := range strings.Split(box, "\n") {
		if strings.Contains(l, "▸") {
			return l
		}
	}
	return ""
}

func pgKey(name string) tea.KeyPressMsg {
	switch name {
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "ctrl+f":
		return ctrl('f')
	}
	return pressKey(name)
}

// TestTheModelListScrollsToTheCursor is the bug this whole change is about.
//
// The box used to keep the cursor on screen by growing: it trimmed the tail and
// stopped as soon as the highlighted row was inside, which for a list of three
// hundred on a twenty-four-row terminal produced a panel three hundred rows
// tall. lipgloss.Place then centres the overflow, so the bottom border and the
// panel's own title were both pushed off the screen and the row the user had
// navigated to was in the middle of the part nobody could see.
//
// Both halves matter, and the second is the one a height assertion alone would
// pass: the box has to be short AND the cursor has to be in it.
func TestTheModelListScrollsToTheCursor(t *testing.T) {
	m := pickerModel(100, 24, "https://api.groq.com/openai/v1", catalogue(300)...)

	for i := 0; i < 299; i++ {
		m = press(t, m, pressKey("down"))
	}
	if got, want := m.picker.list.sel, 299; got != want {
		t.Fatalf("the cursor is on %d after 299 presses, want %d", got, want)
	}

	box := m.modelPickerBox()
	if h := lipgloss.Height(box); h > m.height {
		t.Fatalf("the box is %d rows on a %d-row terminal — it grew to reach the cursor", h, m.height)
	}
	row := highlightedRow(box)
	if row == "" {
		t.Fatalf("the cursor is not drawn anywhere in the box:\n%s", box)
	}
	if !strings.Contains(row, "model-299") {
		t.Errorf("the drawn cursor is on %q, want the row the user navigated to:\n%s", row, box)
	}
	if !strings.Contains(box, "⌘ models") {
		t.Errorf("the title scrolled off the top of the frame:\n%s", box)
	}
}

// TestPageKeysMoveTheCursorAndScrollWithIt: a page key is the answer on a long
// list, and it has to leave the cursor on screen — the failure mode is a page
// key that moves the selection into a window nobody scrolled to.
func TestPageKeysMoveTheCursorAndScrollWithIt(t *testing.T) {
	m := pickerModel(100, 30, "https://api.groq.com/openai/v1", catalogue(400)...)

	m = press(t, m, pgKey("end"))
	if m.picker.list.sel != 399 {
		t.Fatalf("end put the cursor on %d, want the last of 400", m.picker.list.sel)
	}
	box := m.modelPickerBox()
	if row := highlightedRow(box); !strings.Contains(row, "model-399") {
		t.Errorf("end scrolled nowhere: the cursor is on %q\n%s", row, box)
	}

	m = press(t, m, pgKey("home"))
	if m.picker.list.sel != 0 {
		t.Fatalf("home put the cursor on %d, want the first", m.picker.list.sel)
	}
	if box := m.modelPickerBox(); !strings.Contains(box, "model-000") {
		t.Errorf("home did not scroll back:\n%s", box)
	}

	before := m.picker.list.sel
	m = press(t, m, pgKey("pgdown"))
	if m.picker.list.sel <= before {
		t.Errorf("pgdown left the cursor on %d", m.picker.list.sel)
	}
	if box := m.modelPickerBox(); highlightedRow(box) == "" {
		t.Errorf("pgdown moved the cursor off the visible list:\n%s", box)
	}
}

// TestTheFilterNarrowsAndTheHeaderSaysSo. The filter existed before this change
// and worked; nobody used it, because nothing on the box said typing filtered
// anything, and a list of three hundred that silently shrinks is indistinguishable
// from a list of three hundred.
func TestTheFilterNarrowsAndTheHeaderSaysSo(t *testing.T) {
	m := pickerModel(100, 30, "https://api.openrouter.ai/api/v1",
		"vendor/alpha:free", "vendor/beta", "vendor/gamma:free")

	box := m.modelPickerBox()
	for _, want := range []string{"type to filter", "ctrl+f"} {
		if !strings.Contains(box, want) {
			t.Errorf("the header does not mention %q, so the filter is undiscoverable:\n%s", want, box)
		}
	}

	m = press(t, m, pressKey("b"))
	if got := m.filteredModels(); len(got) != 1 || got[0] != "vendor/beta" {
		t.Fatalf("typing b left %v, want only vendor/beta", got)
	}
	box = m.modelPickerBox()
	if strings.Contains(box, "type to filter") {
		t.Errorf("the invitation is still showing over a filter that has been typed:\n%s", box)
	}
	if strings.Contains(box, "alpha") {
		t.Errorf("a filtered-out model is still drawn:\n%s", box)
	}
	if !strings.Contains(box, "1/3") {
		t.Errorf("the header does not say how much of the list survived:\n%s", box)
	}
}

// TestEveryWordOfTheFilterHasToMatch: a model id is
// "meta-llama/Llama-3.3-70B-Instruct", and somebody typing "llama 70b" is
// looking for that one. Requiring the whole string as a single substring makes
// the obvious query return nothing.
func TestEveryWordOfTheFilterHasToMatch(t *testing.T) {
	m := pickerModel(100, 30, "https://api.groq.com/openai/v1",
		"meta-llama/Llama-3.3-70B-Instruct", "meta-llama/Llama-3.1-8B-Instruct", "qwen/qwen3-32b")

	for _, k := range []tea.KeyPressMsg{
		{Code: 'l', Text: "l"}, {Code: 'l', Text: "l"}, {Code: 'a', Text: "a"},
		{Code: 'm', Text: "m"}, {Code: 'a', Text: "a"},
		{Code: ' ', Text: " "},
		{Code: '7', Text: "7"}, {Code: '0', Text: "0"}, {Code: 'b', Text: "b"},
	} {
		m = press(t, m, k)
	}
	got := m.filteredModels()
	if len(got) != 1 || got[0] != "meta-llama/Llama-3.3-70B-Instruct" {
		t.Errorf("\"llama 70b\" matched %v, want the 70B model", got)
	}
}

// TestFreeOnlyKeepsWhatTheEndpointServesForFree is the third half of the
// complaint. Two ways a model turns out to be free, and they do not overlap: the
// endpoint's own naming (OpenRouter's ":free") and the endpoint's free tier
// (Groq's, where no id says anything).
func TestFreeOnlyKeepsWhatTheEndpointServesForFree(t *testing.T) {
	ids := []string{"openai/gpt-4o", "deepseek/deepseek-chat:free", "kilo-auto/free"}

	openrouter := pickerModel(100, 30, "https://openrouter.ai/api/v1", ids...)
	openrouter = press(t, openrouter, pgKey("ctrl+f"))
	if !openrouter.picker.onlyFree {
		t.Fatal("ctrl+f did not turn the free filter on")
	}
	got := openrouter.filteredModels()
	if len(got) != 2 || strings.Contains(strings.Join(got, ","), "gpt-4o") {
		t.Errorf("OpenRouter free filter kept %v, want the two ids that say they are free", got)
	}

	// The same ids on a Groq endpoint: nothing in the names, and every one of
	// them free. A filter that read the id alone would offer the user nothing.
	groq := pickerModel(100, 30, "https://api.groq.com/openai/v1", "qwen/qwen3-32b", "llama-3.3-70b-versatile")
	groq = press(t, groq, pgKey("ctrl+f"))
	if n := len(groq.filteredModels()); n != 2 {
		t.Errorf("Groq's free tier left %d rows, want both", n)
	}

	// And it turns back off rather than being a one-way door.
	groq = press(t, groq, pgKey("ctrl+f"))
	if n := len(groq.filteredModels()); n != 2 || groq.picker.onlyFree {
		t.Errorf("ctrl+f again left onlyFree=%v and %d rows", groq.picker.onlyFree, n)
	}
}

// TestFreeRowsAreToldApartWithoutAGlyphOfTheirOwn: the free ones have to be
// findable while scrolling a catalogue nobody has filtered yet, and the way they
// are told apart is weight — a dim row against a plain one — because a third
// marker in the gutter column is a code to learn rather than a fact to read.
// The header's count is the other half of the same answer, and both are asserted
// here because losing either leaves "which of these is free" unanswerable.
func TestFreeRowsAreToldApartWithoutAGlyphOfTheirOwn(t *testing.T) {
	m := pickerModel(100, 30, "https://openrouter.ai/api/v1", "vendor/paid:pro", "vendor/cheap:free")

	box := m.modelPickerBox()
	if !strings.Contains(box, "1 free") {
		t.Errorf("the header does not count the free models:\n%s", box)
	}

	// styleHint paints its foreground, so the dim SGR is the mark.
	const dim = "\x1b[90m"
	freeRow, paidRow := "", ""
	for _, l := range strings.Split(box, "\n") {
		switch {
		case strings.Contains(l, "cheap"):
			freeRow = l
		case strings.Contains(l, "paid"):
			paidRow = l
		}
	}
	if !strings.Contains(freeRow, dim) {
		t.Errorf("the free row is not told apart from a paid one: %q", freeRow)
	}
	if strings.Contains(paidRow, dim) {
		t.Errorf("the paid row is dimmed like a free one: %q", paidRow)
	}
}

// TestTheFreeFilterSurvivesTheFetchThatFollowsIt guards the seam between the
// typed fast path and the network. "/models free" sets the filter before the
// listing exists; the reply must not reset it, or the command answers a
// different question than the user typed and hands back the whole catalogue.
func TestTheFreeFilterSurvivesTheFetchThatFollowsIt(t *testing.T) {
	m := pickerModel(100, 30, "https://openrouter.ai/api/v1")
	m.picker.query, m.picker.onlyFree = "deepseek", true

	next, _ := m.Update(modelsListMsg{Models: []string{"deepseek/deepseek-chat-v3.1:free", "openai/gpt-4o"}})
	m = next.(*uiModel)

	if m.picker.query != "deepseek" || !m.picker.onlyFree {
		t.Errorf("the listing reset the filters: query=%q onlyFree=%v", m.picker.query, m.picker.onlyFree)
	}
	got := m.filteredModels()
	if len(got) != 1 || !strings.Contains(got[0], "deepseek") {
		t.Errorf("the pre-set filter does not apply to the listing that arrived: %v", got)
	}
}

// TestTheWheelScrollsTheListNotTheTranscriptBehindIt. With a box on screen the
// frame IS the box, so a wheel handed to the viewport scrolls a conversation the
// user cannot see while the list under the pointer stands still — the same
// reason the click hit test stops at an overlay.
func TestTheWheelScrollsTheListNotTheTranscriptBehindIt(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "работа, которую видно")
	m.mouseEnabled = true
	m.picker = modelPicker{open: true, Models: catalogue(200)}
	before := m.vp.YOffset()

	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(*uiModel)

	if m.picker.list.sel == 0 {
		t.Error("the wheel did not move the cursor of the open list")
	}
	if m.vp.YOffset() != before {
		t.Errorf("the wheel scrolled the transcript behind the box: offset %d -> %d", before, m.vp.YOffset())
	}
}

// TestCtrlCQuitsTheModelList: the box swallows every key it does not know, and
// ctrl+c carries no text, so without a case for it the one key that always
// means "get me out" is the one key the box keeps.
func TestCtrlCQuitsTheModelList(t *testing.T) {
	m := pickerModel(100, 30, "https://api.groq.com/openai/v1", catalogue(50)...)

	_, cmd := m.pickerKey(ctrl('c'))
	if cmd == nil {
		t.Fatal("ctrl+c in the model list did not produce a command")
	}
	if msg := cmd(); msg == nil {
		t.Error("ctrl+c produced no message; it must quit the program")
	}
}

// TestAFilterThatFindsNothingSaysSo is the difference between "nothing matches"
// and "the box is broken".
func TestAFilterThatFindsNothingSaysSo(t *testing.T) {
	m := pickerModel(100, 30, "https://api.groq.com/openai/v1", catalogue(20)...)
	m.picker.query = "zzz"

	if n := len(m.filteredModels()); n != 0 {
		t.Fatalf("the filter kept %d rows", n)
	}
	box := m.modelPickerBox()
	if !strings.Contains(box, "nothing found") {
		t.Errorf("an empty result says nothing:\n%s", box)
	}
	if lipgloss.Height(box) > m.height {
		t.Errorf("the empty box is %d rows on a %d-row terminal", lipgloss.Height(box), m.height)
	}
}

// TestThePickerSpeaksRussian: the strings that tell a user what they may type
// are the ones a translated interface has to translate, because a user who
// cannot read the hint cannot find the ctrl+f either.
func TestThePickerSpeaksRussian(t *testing.T) {
	restore := i18n.Current()
	t.Cleanup(func() { i18n.Set(restore) })
	i18n.Set(i18n.Russian)

	m := pickerModel(100, 30, "https://openrouter.ai/api/v1", "vendor/paid:pro", "vendor/cheap:free")
	box := m.modelPickerBox()
	for _, want := range []string{"печатай", "ctrl+f", "моделей"} {
		if !strings.Contains(box, want) {
			t.Errorf("the Russian header is missing %q:\n%s", want, box)
		}
	}
	if strings.Contains(box, "type to filter") {
		t.Errorf("the box still shows the English invitation in Russian:\n%s", box)
	}
}
