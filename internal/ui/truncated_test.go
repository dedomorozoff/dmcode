package ui

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// transcriptJoined is every transcript row as one string, which is what these
// assertions read: a note the user cannot see is a note that was never written,
// and searching a joined string cannot miss one for being on an unexpected row.
func transcriptJoined(m *uiModel) string {
	var b strings.Builder
	for _, l := range m.history {
		b.WriteString(l.text)
		b.WriteString("\n")
	}
	return b.String()
}

// A turn cut by the output limit ends cleanly — no error, a full report, a
// half-sentence answer — and the only number left on screen is the context
// meter. A user reading that has no way to tell a long calculation from a full
// window, which is how a cut answer gets read as an exhausted context.
//
// So the turn has to name the real cause, and it has to offer the way out: the
// half answer was stored as a model event, so it is in the conversation the next
// turn is built from and "continue" resumes rather than restarts.
func TestACutAnswerNamesTheOutputLimitAndHowToContinue(t *testing.T) {
	m := framedModel(120, 40, "gpt-4o-mini")
	m.applyWindow("gpt-4o")
	m.Update(turnDoneMsg{timing: turnTiming{
		Elapsed: 12 * time.Second, Completion: 4096, Prompt: 8000, Calls: 1, Truncated: true,
	}})

	note := ""
	for _, l := range m.history {
		if strings.Contains(l.text, "⚠") {
			note = l.text
		}
	}
	if note == "" {
		t.Fatalf("a cut answer produced no note: %s", transcriptJoined(m))
	}
	if !strings.Contains(note, "output limit") {
		t.Errorf("the note does not name the output limit: %q", note)
	}
	// The number the endpoint actually stopped at is the one thing that tells
	// the user which knob to turn.
	if !strings.Contains(note, "4 096") {
		t.Errorf("the cut is not reported with the size it reached: %q", note)
	}
	if !strings.Contains(note, "continue") {
		t.Errorf("the note does not say how to carry on: %q", note)
	}
	// The wrong diagnosis is the whole failure, and it is the context meter that
	// supplies it: a cut answer is not a full window, so a note that mentions the
	// context is one a user will read as "start a new session" — advice that
	// throws the conversation away and does not even fix the wall.
	if strings.Contains(note, "context") {
		t.Errorf("the note points at the context instead of the output limit: %q", note)
	}
}

// The mark belongs next to the token count it qualifies. On the timing line, a
// separate row would read as a different problem that happened afterwards,
// which is the confusion the whole feature exists to remove.
func TestTheTimingLineMarksTheCutNextToTheAnswer(t *testing.T) {
	line := timingLine(turnTiming{
		Elapsed: 12 * time.Second, Completion: 4096, Prompt: 8000, Calls: 1, Truncated: true,
	}, 128_000)
	if !strings.Contains(line, "cut at the output limit") {
		t.Errorf("the turn report does not mark the cut: %q", line)
	}
	if !strings.Contains(line, "answer: 4 096 tokens · cut at the output limit") {
		t.Errorf("the cut is not attached to the answer's size: %q", line)
	}

	whole := timingLine(turnTiming{
		Elapsed: 4 * time.Second, Completion: 800, Prompt: 8000, Calls: 1,
	}, 128_000)
	if strings.Contains(whole, "cut") {
		t.Errorf("a finished answer is marked as cut: %q", whole)
	}
}

// finish_reason=length is what the adapter reports when the endpoint stopped the
// model at its output ceiling, and reading it is the only thing standing between
// a cut answer and silence. The test drives the real seam with the real event
// shape, including the case that made a single read wrong: a wrapper above the
// adapter following the truncated response with a synthesised "stop".
func TestTheCutIsReadFromTheFinishReasonAndLatched(t *testing.T) {
	tm := turnTiming{}
	tm.observe(model.LLMResponse{Content: genai.NewContentFromText("half an answer", genai.RoleModel)})
	if tm.Truncated {
		t.Fatal("a response with no finish reason claims to be cut")
	}

	cut := model.LLMResponse{
		Content:       genai.NewContentFromText("the rest never came", genai.RoleModel),
		FinishReason:  genai.FinishReasonMaxTokens,
		TurnComplete:  true,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{CandidatesTokenCount: 4096},
	}
	tm.observe(cut)
	if !tm.Truncated {
		t.Fatal("finish_reason=length was not read as a cut")
	}
	// The size it stopped at comes from the same response, so the note can say it.
	if tm.Completion != 4096 {
		t.Errorf("the cut is not paired with the size it reached: %d", tm.Completion)
	}

	tm.observe(model.LLMResponse{
		Content:      genai.NewContentFromText("", genai.RoleModel),
		FinishReason: genai.FinishReasonStop,
		TurnComplete: true,
	})
	if !tm.Truncated {
		t.Error("a later response cleared the mark on an answer that was already cut")
	}
}

// The prompt count is the largest one seen, not the last: a streaming endpoint
// that reports usage only on an intermediate chunk leaves the final one empty,
// and "most recent wins" is how a turn that plainly used tokens reports zero.
func TestUsageIsTakenAsTheLargestReportNotTheLast(t *testing.T) {
	tm := turnTiming{}
	tm.observe(model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 8000}})
	tm.observe(model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 4000}})
	if tm.Prompt != 8000 {
		t.Errorf("Prompt = %d, want the largest report (8000)", tm.Prompt)
	}
	tm.observe(model.LLMResponse{})
	if tm.Prompt != 8000 {
		t.Errorf("an empty report wiped the prompt count: %d", tm.Prompt)
	}
}
