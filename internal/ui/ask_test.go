package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/ask"
)

// askTwoOptions is a question with a recommendation and room for a typed answer.
func askTwoOptions() ask.Request {
	return ask.Request{
		Question:    "что делаем?",
		Options:     []ask.Option{{Label: "да", Recommended: true}, {Label: "нет"}},
		AllowCustom: true,
	}
}

// openQuestion puts the overlay up and returns the channel the answer will
// arrive on, the way a blocked tool would see it.
func openQuestion(t *testing.T, m *uiModel, req ask.Request, wait time.Duration) chan ask.Answer {
	t.Helper()
	reply := make(chan ask.Answer, 1)
	m.openAsk(askRequestMsg{req: req, reply: reply, wait: wait})
	if !m.ask.open {
		t.Fatal("the overlay did not open")
	}
	return reply
}

// receivedAnswer reads the answer the tool would get, or reports that none came.
func receivedAnswer(t *testing.T, reply chan ask.Answer) ask.Answer {
	t.Helper()
	select {
	case a := <-reply:
		return a
	case <-time.After(time.Second):
		t.Fatal("the answer never reached the waiting tool")
		return ask.Answer{}
	}
}

// typed sends one printable rune into whatever field the overlay has focused.
func typed(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// TestConfirmSendsTheHighlightedOption: enter on a single-choice question means
// the row under the cursor, which is the row the user is looking at.
func TestConfirmSendsTheHighlightedOption(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("down")) // move to "нет"
	m.askKey(pressKey("enter"))

	got := receivedAnswer(t, reply)
	if len(got.Selected) != 1 || got.Selected[0] != "нет" {
		t.Errorf("answer = %+v, want the second option", got)
	}
	if m.ask.open {
		t.Error("the overlay stayed open after the answer")
	}
}

// TestMultiSelectTicksSeveralOptions: the point of multi is that "and" is
// available.
func TestMultiSelectTicksSeveralOptions(t *testing.T) {
	m := newSessionModel(t)
	req := askTwoOptions()
	req.Multi = true
	reply := openQuestion(t, m, req, 0)

	m.askKey(pressKey("space")) // tick "да"
	m.askKey(pressKey("down"))
	m.askKey(pressKey("space")) // tick "нет"
	m.askKey(pressKey("enter"))

	got := receivedAnswer(t, reply)
	if len(got.Selected) != 2 {
		t.Fatalf("answer = %+v, want both options", got)
	}
	// In the order the question was asked, not in map order.
	if got.Selected[0] != "да" || got.Selected[1] != "нет" {
		t.Errorf("answer = %v, want them in the order asked", got.Selected)
	}
}

// TestMultiSelectUntick: a tick the user changes their mind about has to come
// off, or the question can only be answered by everything.
func TestMultiSelectUntick(t *testing.T) {
	m := newSessionModel(t)
	req := askTwoOptions()
	req.Multi = true
	reply := openQuestion(t, m, req, 0)

	m.askKey(pressKey("space"))
	m.askKey(pressKey("space")) // the same row again
	m.askKey(pressKey("enter"))

	got := receivedAnswer(t, reply)
	if len(got.Selected) != 1 {
		t.Errorf("answer = %+v, want only the option under the cursor", got)
	}
}

// TestSingleChoiceSpaceReplaces: on a single-choice question space means "this
// one", not "this one as well as whatever was there".
func TestSingleChoiceSpaceReplaces(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("space")) // "да"
	m.askKey(pressKey("down"))
	m.askKey(pressKey("space")) // "нет"
	m.askKey(pressKey("enter"))

	got := receivedAnswer(t, reply)
	if len(got.Selected) != 1 || got.Selected[0] != "нет" {
		t.Errorf("answer = %+v, want only the last ticked option", got)
	}
}

// TestCustomAnswerReachesTheTool: the option the model did not think of is the
// reason the row exists.
func TestCustomAnswerReachesTheTool(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("c"))
	if !m.ask.customOpen {
		t.Fatal("c did not open the text field")
	}
	for _, r := range "свой" {
		m.askKey(typed(r))
	}
	m.askKey(pressKey("enter"))

	got := receivedAnswer(t, reply)
	if got.Custom != "свой" {
		t.Errorf("answer = %+v, want the typed text", got)
	}
}

// TestEmptyCustomFieldConfirmsNothing: answering a question with a blank is
// worse than asking again.
func TestEmptyCustomFieldConfirmsNothing(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("c"))
	m.askKey(pressKey("enter")) // empty field

	if !m.ask.open {
		t.Error("an empty field closed the question")
	}
	select {
	case <-reply:
		t.Error("an empty field sent an answer")
	default:
	}
}

// TestEscapeInTheFieldGoesBackNotOut: a user who opened the text field by
// accident must not lose the whole question.
func TestEscapeInTheFieldGoesBackNotOut(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("c"))
	m.askKey(typed('а'))
	m.askKey(pressKey("esc"))

	if !m.ask.open {
		t.Fatal("escape closed the whole question instead of the field")
	}
	if m.ask.customOpen {
		t.Error("the field stayed open")
	}
	select {
	case <-reply:
		t.Error("escape in the field answered the question")
	default:
	}
}

// TestEscapeSkipsTheQuestion: no answer is a legitimate answer, and the turn
// must be released rather than left waiting.
func TestEscapeSkipsTheQuestion(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("esc"))

	got := receivedAnswer(t, reply)
	if !got.Skipped {
		t.Errorf("answer = %+v, want it skipped", got)
	}
	if m.ask.open {
		t.Error("the overlay stayed open after skipping")
	}
}

// TestTimerClosesTheOverlayAndNamesTheChosenOption: the broker has already
// decided by then, so the screen must not keep asking a question that is
// settled.
func TestTimerClosesTheOverlayAndNamesTheChosenOption(t *testing.T) {
	m := newSessionModel(t)
	reply := openQuestion(t, m, askTwoOptions(), time.Hour)

	// The tick is driven with the deadline already past, which is what the
	// broker's own timer will have seen by the time this runs.
	m.ask.deadline = time.Now().Add(-time.Second)
	m.handleAskTick()

	if m.ask.open {
		t.Error("the overlay stayed open after the timer expired")
	}
	var said bool
	for _, l := range m.history {
		if strings.Contains(l.text, "да") {
			said = true
		}
	}
	if !said {
		t.Error("the transcript did not say which option the timer chose")
	}
	// The broker sends the answer on its own side; nothing here may block.
	select {
	case <-reply:
	default:
	}
}

// TestNoTickCommandWhenTheTimerIsOff: waking the event loop once a second for a
// question with no deadline is work for nothing.
func TestNoTickCommandWhenTheTimerIsOff(t *testing.T) {
	m := newSessionModel(t)
	reply := make(chan ask.Answer, 1)
	if cmd := m.openAsk(askRequestMsg{req: askTwoOptions(), reply: reply, wait: 0}); cmd != nil {
		t.Error("a tick was scheduled with the timer off")
	}
	if m.askRemaining() != 0 {
		t.Errorf("remaining = %s, want none with the timer off", m.askRemaining())
	}
}

// TestNoCustomRowWithoutAllowCustom: a row that cannot be answered is a row
// that wastes a keystroke.
func TestNoCustomRowWithoutAllowCustom(t *testing.T) {
	m := newSessionModel(t)
	req := ask.Request{Question: "вопрос", Options: []ask.Option{{Label: "да"}}}
	openQuestion(t, m, req, 0)

	if m.customRow() != -1 {
		t.Error("the custom row is present although the question did not ask for one")
	}
	m.askKey(pressKey("down")) // must not move past the last option
	if m.ask.cursor != 0 {
		t.Errorf("cursor = %d, want it pinned to the only option", m.ask.cursor)
	}
}

// TestQuestionRendersItsOptionsAndNotes: an overlay that drops a note leaves
// the user choosing between options they cannot tell apart.
func TestQuestionRendersItsOptionsAndNotes(t *testing.T) {
	m := newSessionModel(t)
	req := ask.Request{
		Question: "чем заняться?",
		Options:  []ask.Option{{Label: "правкой", Note: "быстрее"}, {Label: "тестом", Note: "надёжнее"}},
	}
	openQuestion(t, m, req, 0)
	m.width, m.height = 100, 40

	box := m.askBox()
	if box == "" {
		t.Fatal("the overlay rendered nothing")
	}
	for _, want := range []string{"правкой", "быстрее", "тестом", "надёжнее", "чем заняться?"} {
		if !strings.Contains(box, want) {
			t.Errorf("the overlay is missing %q", want)
		}
	}
}

// TestTheQuestionDoesNotCoverTheTranscript: the question is about the reply the
// user is looking at, so that reply has to stay on screen while it is asked.
// This is the difference between a docked panel and the centred overlay it used
// to be, and it is checked on the frame the user actually sees.
func TestTheQuestionDoesNotCoverTheTranscript(t *testing.T) {
	m := newSessionModel(t)
	m.width, m.height = 100, 40
	m.layout()
	m.history = []line{{kindAgent, "the reply the question is about"}}
	m.followVP()
	openQuestion(t, m, askTwoOptions(), 0)

	frame := ansi.Strip(m.View().Content)
	if !strings.Contains(frame, "the reply the question is about") {
		t.Fatal("the question covered the reply it was about")
	}
	if !strings.Contains(frame, "что делаем?") {
		t.Fatal("the question is not on screen")
	}
	if got := len(strings.Split(frame, "\n")); got != m.height {
		t.Errorf("frame is %d rows, want %d", got, m.height)
	}
	reply, question := strings.Index(frame, "the reply the question is about"), strings.Index(frame, "что делаем?")
	if reply > question {
		t.Errorf("the question is above the reply (reply at %d, question at %d)", reply, question)
	}
}

// TestTheQuestionTakesItsRowsFromTheTranscript: docking must not push the frame
// past the terminal, and closing the question must give the rows back. A panel
// appended without resizing the viewport would make the frame taller than the
// screen; one that never gave the rows back would leave the chat short.
func TestTheQuestionTakesItsRowsFromTheTranscript(t *testing.T) {
	m := newSessionModel(t)
	m.width, m.height = 100, 40
	m.layout()
	before := m.vp.Height()

	openQuestion(t, m, askTwoOptions(), 0)
	during := m.vp.Height()
	if during >= before {
		t.Errorf("viewport is %d rows while asking, want fewer than %d", during, before)
	}
	if got := len(strings.Split(ansi.Strip(m.View().Content), "\n")); got != m.height {
		t.Errorf("frame with the question is %d rows, want %d", got, m.height)
	}

	m.askKey(pressKey("enter"))
	if after := m.vp.Height(); after != before {
		t.Errorf("viewport is %d rows after the answer, want %d back", after, before)
	}
}

// TestALongCustomAnswerDoesNotGrowTheFrame: the custom row wraps as it is typed,
// so the question gets taller after it was laid out. The viewport has to be
// re-measured, or the bottom border is pushed off the terminal one wrapped row
// at a time.
func TestALongCustomAnswerDoesNotGrowTheFrame(t *testing.T) {
	m := newSessionModel(t)
	m.width, m.height = 60, 24
	m.layout()
	openQuestion(t, m, askTwoOptions(), 0)

	m.askKey(pressKey("c"))
	for i := 0; i < 200; i++ {
		m.askKey(typed('ж'))
	}

	frame := ansi.Strip(m.View().Content)
	if got := len(strings.Split(frame, "\n")); got != m.height {
		t.Errorf("frame is %d rows, want %d", got, m.height)
	}
	if !strings.Contains(frame, "ж") {
		t.Error("the custom answer left the screen")
	}
}
