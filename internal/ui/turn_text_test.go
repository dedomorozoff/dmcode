package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// replyAfterTool is the second round's text: a heading and a table, the two
// shapes a doubled reply destroys outright — the second copy lands on the same
// line, so "## Итог" becomes "## Итог## Итог" and the table loses the delimiter
// row that is what makes it a table.
const replyAfterTool = "## Итог\n\n| Файл | Строк |\n|---|---|\n| a.go | 10 |\n"

// newTurnModel builds a model with a transcript sized as the TUI would have it,
// so a test can look at what the viewport actually draws.
func newTurnModel(t *testing.T) *uiModel {
	t.Helper()
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.width, m.height = 120, 45
	m.layout()
	m.history = nil
	return m
}

// play drives a model through one tool-using turn, the way startTurn does: every
// text part goes through turnText, and the tool result is dropped between the
// rounds to stand for the tool line a real turn puts there.
func play(m *uiModel, tt *turnText, toolResult string) {
	for _, ev := range []struct {
		text    string
		partial bool
		tool    bool
	}{
		{"Смотрю.\n", true, true},
		{"Смотрю.\n", false, false},
		{replyAfterTool, true, false},
		{replyAfterTool, false, false},
	} {
		m.applyAgentText(tt.add(ev.text, ev.partial))
		if ev.tool && toolResult != "" {
			m.history = append(m.history, line{kindToolRes, toolResult})
			m.historyDirty = true
		}
	}
}

// transcript joins every line of the transcript, which is what a duplication
// shows up in.
func transcript(m *uiModel) string {
	var b strings.Builder
	for _, l := range m.history {
		b.WriteString(l.text)
	}
	return b.String()
}

// TestTurnTextDoesNotDuplicateAcrossToolRounds is the regression behind "markdown
// works every other time": the accumulator was never reset at the end of a round,
// so the second round's closing text no longer matched what its partials had
// delivered and the whole reply was appended a second time.
func TestTurnTextDoesNotDuplicateAcrossToolRounds(t *testing.T) {
	m := newTurnModel(t)
	play(m, &turnText{}, "")

	want := "Смотрю.\n" + replyAfterTool
	if got := transcript(m); got != want {
		t.Fatalf("transcript got %q, want %q", got, want)
	}
	// The doubled text is the failure in one assertion: a "##" that is not at the
	// start of a line is not a heading any more.
	if strings.Contains(transcript(m), "## Итог## Итог") {
		t.Error("the second round's reply was delivered twice")
	}
}

// TestTurnTextPassesPartialsThrough is the plain streaming case: a delta is new
// text and goes out unchanged, and the closing response that repeats it adds
// nothing at all.
func TestTurnTextPassesPartialsThrough(t *testing.T) {
	var tt turnText
	if s := tt.add("## Итог\n", true); s.text != "## Итог\n" || s.replace {
		t.Errorf("a partial must be passed through as a delta, got %+v", s)
	}
	if s := tt.add("## Итог\n", false); s.text != "" {
		t.Errorf("the closing response repeated the deltas: %+v", s)
	}
}

// TestTurnTextSendsOnlyMissingSuffix covers the provider whose closing response
// carries a little more than its deltas did. Only the difference may be sent;
// the rest would be a second copy of the same words.
func TestTurnTextSendsOnlyMissingSuffix(t *testing.T) {
	var tt turnText
	tt.add("## Итог\n\nтаблица", true)
	if s := tt.add("## Итог\n\nтаблица \n", false); s.text != " \n" || s.replace {
		t.Errorf("only the missing suffix may be sent, got %+v", s)
	}
}

// TestTurnTextReplacesTheRoundWhenTheProviderRewritesIt is the case a suffix
// cannot cover: the closing text is not an extension of the deltas at all, so
// there is nothing to trim. It has to replace the round, because appending it
// would put two different copies of the same reply on screen.
func TestTurnTextReplacesTheRoundWhenTheProviderRewritesIt(t *testing.T) {
	m := newTurnModel(t)
	var tt turnText

	// The tool result belongs to the round before this one, so it sits above the
	// text being corrected and must survive the replacement untouched.
	m.history = append(m.history, line{kindToolRes, `{"ok":true}`})
	m.historyDirty = true
	m.applyAgentText(tt.add("## Итог\n\nчерновик\n", true))
	m.applyAgentText(tt.add("## Итог\n\nисправлено\n", false))

	if len(m.history) != 2 {
		t.Fatalf("the transcript is %d lines, want 2: %q", len(m.history), transcript(m))
	}
	if m.history[0].kind != kindToolRes {
		t.Errorf("line 0 is %s, want the tool result", kindName(m.history[0].kind))
	}
	if got := m.history[1].text; got != "## Итог\n\nисправлено\n" {
		t.Errorf("the round was not replaced, it is %q", got)
	}
}

// TestApplyAgentTextReplaceKeepsSplitDeltas is the shape a tool call inside a
// round produces: the streamed text is split across two agent lines, and the
// closing response has to collapse them back into one correct reply.
func TestApplyAgentTextReplaceKeepsSplitDeltas(t *testing.T) {
	m := newTurnModel(t)
	var tt turnText

	m.applyAgentText(tt.add("## Итог\n", true))
	m.history = append(m.history, line{kindToolRes, `{"ok":true}`})
	m.historyDirty = true
	m.applyAgentText(tt.add("## Итог\n\n| a | b |\n|---|---|\n", true))
	m.applyAgentText(tt.add("## Итог\n\n| a | b |\n|---|---|\n", false))

	// The draft that was streamed before the correction must not survive as a
	// second copy above the answer.
	for _, l := range m.history {
		if strings.Contains(l.text, "черновик") {
			t.Error("a stale copy of the round is still on screen")
		}
	}
	if !strings.Contains(transcript(m), "## Итог") {
		t.Errorf("the reply lost its heading: %q", transcript(m))
	}
}

// TestTurnTextSendsEverythingWhenNothingStreamed keeps the non-streaming path
// working: with no partials to deduplicate against, the closing text is the only
// copy there will ever be.
func TestTurnTextSendsEverythingWhenNothingStreamed(t *testing.T) {
	var tt turnText
	if s := tt.add(replyAfterTool, false); s.text != replyAfterTool || s.replace {
		t.Errorf("an unstreamed reply must be sent whole, got %+v", s)
	}
}

// TestTurnTextEmptyFinalResponseClosesTheRound guards the reset itself: a round
// that produced no text at all (a bare tool call) must still leave the
// accumulator empty, or the next round is compared against the one before it.
func TestTurnTextEmptyFinalResponseClosesTheRound(t *testing.T) {
	var tt turnText
	tt.add("первый раунд\n", true)
	if s := tt.add("", false); s.text != "" {
		t.Errorf("an empty closing response produced %+v", s)
	}
	if s := tt.add("## Второй\n", false); s.text != "## Второй\n" || s.replace {
		t.Errorf("the next round was compared against the previous one: %+v", s)
	}
}

// TestReportReplyThroughFullPipelineAfterToolRound drives the reply through the
// transcript the way a tool-using turn does: the model's first words, the tool
// line, then the answer. Testing the renderer alone would miss the duplication,
// which happens long before the renderer is reached.
func TestReportReplyThroughFullPipelineAfterToolRound(t *testing.T) {
	m := newTurnModel(t)
	play(m, &turnText{}, `{"ok":true}`)

	m.historyDirty = true
	m.syncVP()

	drawn := ansi.Strip(m.vp.View())
	for _, marker := range []struct{ m, what string }{
		{"##", "heading marker"},
		{"|", "table pipe"},
	} {
		if strings.Contains(drawn, marker.m) {
			t.Errorf("%s reached the screen: %q", marker.what, drawn)
		}
	}
	if !strings.Contains(drawn, "Итог") || !strings.Contains(drawn, "a.go") {
		t.Errorf("the reply lost its content: %q", drawn)
	}
}
