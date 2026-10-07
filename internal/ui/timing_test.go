package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/memsession"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// mustGet reads back a session the service already holds, so an event can be
// appended to it the way the runner would.
func mustGet(t *testing.T, svc *memsession.Service, id string) session.Session {
	t.Helper()
	resp, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "dmcode", UserID: "user", SessionID: id,
	})
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return resp.Session
}

// A turn has to be measurable at all: the timer is started before the send and
// stopped when the turn ends, so a clock that never ran is the first thing these
// pin.
func TestATurnReportsHowLongItTook(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.busy = true
	m.turnStart = time.Now().Add(-7 * time.Second)

	// The live counter rides on the progress note, so a user watching a slow
	// turn can tell it from a hung one without waiting for the report.
	if !strings.Contains(m.progressNote(), "7s") {
		t.Errorf("progress note %q does not show the elapsed time", m.progressNote())
	}

	m.Update(turnDoneMsg{timing: turnTiming{Elapsed: 7 * time.Second, FirstToken: 900 * time.Millisecond}})
	if m.timing.Elapsed != 7*time.Second {
		t.Fatalf("the finished turn's timing was not kept: %+v", m.timing)
	}
	// The clock must not keep running against the wall clock after the turn is
	// over — that is what would make the next frame report a turn still going.
	if !m.turnStart.IsZero() {
		t.Error("the timer is still running after the turn ended")
	}
	// The report is a transcript row, not a badge: it belongs to that turn.
	// The last row is the blank spacer turnDoneMsg always appends, so the
	// report is searched for rather than taken from the tail.
	var report string
	for _, l := range m.history {
		if strings.Contains(l.text, "first token") {
			report = l.text
		}
	}
	if report == "" || !strings.Contains(report, "900ms") {
		t.Errorf("the turn report does not carry the timings: %q", report)
	}
}

// A turn that produced no text has no first token. Reporting 0ms would claim a
// speed the turn never achieved.
func TestATurnWithNoProseReportsNoFirstToken(t *testing.T) {
	line := timingLine(turnTiming{Elapsed: 4 * time.Second, Calls: 3, Prompt: 900, Completion: 0}, 128_000)
	if strings.Contains(line, "first token") {
		t.Errorf("%q reports a first token for a turn that produced no text", line)
	}
	if !strings.Contains(line, "calls: 3") {
		t.Errorf("%q does not count the calls a tool loop made", line)
	}
}

// The context reading is only meaningful against the window it was measured in.
func TestTheContextIsAFractionOfTheWindow(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	m.timing.Prompt = 32_000
	// A quarter full must read as a quarter: this is the number that tells a
	// user compaction is coming, so a wrong fraction is worse than none.
	if got := m.timing.percentUsed(m.window); got != 25 {
		t.Fatalf("percentUsed = %d, want 25", got)
	}
	lines := m.contextLines()
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, " "), "25%") {
		t.Fatalf("sidebar context lines do not show the percentage: %q", lines)
	}
	// A model this build knows gives a real denominator, so nothing on the row
	// is a guess.
	if m.windowGuess {
		t.Error("a recognised model was marked as a guessed window")
	}
	if strings.Contains(strings.Join(lines, " "), "~") {
		t.Errorf("%q marks a known window as a guess", lines)
	}
}

// The meter must never lie about being full: a count above the window fills the
// bar and stops, rather than drawing past the panel edge.
func TestTheContextBarCannotOverflow(t *testing.T) {
	bar := usageBar(500_000, 128_000, 10)
	if n := len([]rune(bar)); n != 10 {
		t.Errorf("bar is %d cells wide, want 10: %q", n, bar)
	}
	if strings.Contains(bar, "█") == false {
		t.Error("a context over the window did not fill the bar")
	}
}

// A provider that reports no usage must still show a meter. The original
// version drew nothing at all in that case, so on every OpenAI-compatible
// server that omits usage from a streamed turn the meter was simply absent —
// which is what "I can't see the context being used" turned out to mean.
func TestTheMeterFallsBackToAnEstimate(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	m.timing.Prompt = 0 // the endpoint reported nothing

	svc := memsession.NewMemory()
	m.sessions = svc
	m.sessionID = "sess-approx"
	if _, err := svc.Ensure(context.Background(), "dmcode", "user", m.sessionID); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// A conversation with real prose in it must produce a reading, and the
	// reading is marked as the guess it is.
	if err := svc.AppendEvent(context.Background(), mustGet(t, svc, m.sessionID), &session.Event{
		Author: "user", Timestamp: time.Now(),
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText(strings.Repeat("a conversational sentence here ", 200), genai.RoleUser),
		},
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	lines := m.contextLines()
	if len(lines) == 0 {
		t.Fatal("no context meter for an endpoint that reports no usage")
	}
	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "~") {
		t.Errorf("%q does not mark the estimate as approximate", joined)
	}
	if !strings.Contains(joined, "%") {
		t.Errorf("%q shows no percentage to compare against the window", joined)
	}
}

// The endpoint's own count wins over the estimate, and loses the mark: a number
// the model reported is not a guess.
func TestTheReportedCountWinsOverTheEstimate(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	m.timing.Prompt = 32_000
	joined := strings.Join(m.contextLines(), " ")
	if strings.Contains(joined, "~") {
		t.Errorf("%q marks a reported count as an estimate", joined)
	}
}

// Compaction is the feature that keeps a long session alive, so a model whose
// window is known must get a threshold and one that is unknown must not be
// given an invented one.
func TestCompactionFollowsTheWindow(t *testing.T) {
	m := &uiModel{}
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	if c := m.compactionConfig(); c == nil {
		t.Fatal("a known window gets no compaction")
	} else if c.TokenThreshold >= m.exactWindow {
		t.Errorf("threshold %d leaves no room for the answer that triggers it", c.TokenThreshold)
	}
	// The other half of the split: an unrecognised model may draw a guessed
	// meter, but must never rewrite the conversation on the strength of it.
	n := &uiModel{}
	n.applyWindow(config.Provider{Model: "some-finetune-abc"})
	if n.window == 0 {
		t.Error("the meter lost its denominator for an unrecognised model")
	}
	if c := n.compactionConfig(); c != nil {
		t.Error("an unknown window was given a guessed compaction threshold")
	}
}

// A meter without a denominator answers nothing: "32 000 tokens" leaves the
// reader to guess the window. An unrecognised model used to produce exactly
// that, which is what "out of how much?" was about.
func TestTheMeterAlwaysHasADenominator(t *testing.T) {
	m := framedModel(120, 40, "madeup/unknown-model-42b") // not in the table
	m.applyWindow(m.prov)
	m.timing.Prompt = 32_000

	if m.window <= 0 {
		t.Fatal("an unrecognised model leaves the meter with no denominator at all")
	}
	joined := strings.Join(m.contextLines(), " ")
	if !strings.Contains(joined, "%") {
		t.Errorf("%q shows no share of a window", joined)
	}
	if !strings.Contains(joined, "~") {
		t.Errorf("%q presents an assumed denominator as a fact: %q", joined, joined)
	}
}

// An assumed denominator may be drawn; it may not decide when to rewrite the
// user's conversation. Compaction therefore reads the exact figure, which is
// zero for the same model whose meter is showing a guess.
func TestAnAssumedWindowNeverTriggersCompaction(t *testing.T) {
	m := &uiModel{}
	m.applyWindow(config.Provider{Model: "madeup/unknown-model-42b"})
	if m.window == 0 {
		t.Fatal("the display window was left empty")
	}
	if m.exactWindow != 0 {
		t.Fatalf("an unrecognised model was given an exact window: %d", m.exactWindow)
	}
	if c := m.compactionConfig(); c != nil {
		t.Error("compaction was scheduled from an assumed window")
	}
}

// The session total is the number that survives past the last turn: without it
// the timings a user watched scroll past are gone the moment a new one arrives.
func TestTheSessionTotalAccumulatesAcrossTurns(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	for _, d := range []time.Duration{4 * time.Second, 6 * time.Second} {
		m.Update(turnDoneMsg{timing: turnTiming{Elapsed: d, FirstToken: time.Second, Calls: 1}})
	}
	if m.total.Turns != 2 {
		t.Fatalf("total.Turns = %d, want 2", m.total.Turns)
	}
	if m.total.Elapsed != 10*time.Second {
		t.Errorf("total.Elapsed = %v, want 10s", m.total.Elapsed)
	}
	if got := m.total.Average(2); got != 5*time.Second {
		t.Errorf("Average = %v, want 5s", got)
	}
	out := m.statsLine()
	if !strings.Contains(out, "10s") {
		t.Errorf("/stats does not report the session's total time:\n%s", out)
	}
}

// A cancelled turn still cost the user the wait, so it counts. Excluding it
// would make the total the one figure that cannot be checked against a clock.
func TestAStoppedTurnStillCountsTowardsTheTotal(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.Update(turnDoneMsg{err: context.Canceled, timing: turnTiming{Elapsed: 30 * time.Second, Calls: 1}})
	if m.total.Elapsed != 30*time.Second {
		t.Errorf("a cancelled turn was not counted: %v", m.total.Elapsed)
	}
}

// Every user-facing string built with a format must actually consume its
// arguments, in every language. The bug this pins was a sentence split into two
// translated halves with the number applied to the second: in English that half
// is "more turns:", which has no verb, so fmt printed "%!(EXTRA int=99)" beside
// a label already ending in a colon. The Russian catalog had the verb, so the
// breakage was English-only and no existing test noticed.
//
// The check is mechanical rather than visual: a rendered row that still carries
// fmt's own diagnostics is a broken row, whichever language produced it.
func TestNoUserFacingStringCarriesAFormatError(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	m.turnCount = 8
	m.timing = turnTiming{Elapsed: 9 * time.Second, FirstToken: time.Second,
		Completion: 800, Prompt: 32_000, Calls: 1}
	m.total.record(m.timing)
	m.total.record(turnTiming{Elapsed: 5 * time.Second, Calls: 1})
	m.compacted = true

	rendered := strings.Join(append(append([]string{}, m.contextLines()...),
		strings.Split(m.statsLine(), "\n")...), " ")
	if bad := strings.Index(rendered, "%!"); bad >= 0 {
		t.Fatalf("a rendered string carries a format error at %d:\n%s", bad, rendered)
	}
}

// The sentence has to read as a sentence in both languages, not merely not
// crash: a stray "%d" left in a translation is the same defect wearing a
// different mask.
func TestTheRoomLeftIsOneReadableSentence(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Russian} {
		i18n.Set(lang)
		defer i18n.Set(i18n.English)
		got := fmt.Sprintf(i18n.T("room for about %d more turns"), 24)
		if strings.Contains(got, "%d") || strings.Contains(got, "%!") {
			t.Errorf("%s: the sentence still shows its placeholder: %q", lang, got)
		}
		if !strings.Contains(got, "24") {
			t.Errorf("%s: the count is missing from %q", lang, got)
		}
	}
}

// Every count in the UI passes through formatCount, so its edges are worth
// pinning. The grouping is the point: "12 345" can be read at a glance, where
// the "12.4k" it replaced had to be decoded first.
func TestFormatCount(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0"}, {842, "842"}, {1000, "1 000"}, {12_345, "12 345"},
		{128_000, "128 000"}, {999, "999"}, {-5, "0"},
	} {
		if got := formatCount(tc.in); got != tc.want {
			t.Errorf("formatCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The question a context meter is actually asked is "how much is left", and a
// bare token count does not answer it. The meter must say both how full the
// window is and roughly how many more turns fit.
func TestTheContextMeterSaysHowMuchIsLeft(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	m.turnCount = 8
	m.timing.Prompt = 32_000 // a quarter of the window, over 8 turns

	joined := strings.Join(m.contextLines(), " ")
	if !strings.Contains(joined, "25%") {
		t.Errorf("%q does not say how full the window is", joined)
	}
	// 128k window, 32k used, 4k per turn: about 24 turns remain.
	if got := m.contextRoom(32_000); got != 24 {
		t.Errorf("contextRoom = %d, want 24", got)
	}
	if !strings.Contains(joined, "24") {
		t.Errorf("%q does not say how many more turns fit", joined)
	}
	// The unit must be named: a number with no unit is the complaint this
	// whole change is fixing.
	if !strings.Contains(joined, "tokens") {
		t.Errorf("%q gives counts without saying what is counted", joined)
	}
}

// A full window must not promise room that is not there.
func TestAFullWindowPromisesNothing(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow(config.Provider{Model: "gpt-4o"})
	m.turnCount = 8
	m.timing.Prompt = 128_000
	if got := m.contextRoom(128_000); got != 0 {
		t.Errorf("contextRoom = %d on a full window, want 0", got)
	}
	if strings.Contains(strings.Join(m.contextLines(), " "), "more turns") {
		t.Error("a full window still claims room for more turns")
	}
}
