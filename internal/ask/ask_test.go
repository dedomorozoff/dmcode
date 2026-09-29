package ask

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// answerer returns a PromptFunc that hands back the given answer once the
// request has been received, so a test never races the waiting side.
func answerer(t *testing.T, ans Answer, seen *Request) PromptFunc {
	t.Helper()
	return func(req Request) <-chan Answer {
		if seen != nil {
			*seen = req
		}
		ch := make(chan Answer, 1)
		ch <- ans
		return ch
	}
}

func twoOptions() Request {
	return Request{
		Question: "как поступить?",
		Options: []Option{
			{Label: "первый", Recommended: true},
			{Label: "второй"},
		},
	}
}

// silent is a prompt nobody ever answers, which is what a timer has to cope
// with.
func silent(Request) <-chan Answer { return make(chan Answer) }

// TestAskReturnsTheUsersChoice: the whole point of the tool.
func TestAskReturnsTheUsersChoice(t *testing.T) {
	b := NewBroker(0)
	b.SetPromptFunc(answerer(t, Answer{Selected: []string{"второй"}}, nil))

	got, err := b.Ask(context.Background(), twoOptions())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(got.Selected) != 1 || got.Selected[0] != "второй" {
		t.Errorf("answer = %+v, want the second option", got)
	}
	if got.Auto || got.Skipped {
		t.Errorf("a real answer was marked auto=%v skipped=%v", got.Auto, got.Skipped)
	}
}

// TestAskWithNoPromptSkipsRatherThanHangs: a headless run has nobody to ask.
// Blocking there would hang a turn that has no way to finish.
func TestAskWithNoPromptSkipsRatherThanHangs(t *testing.T) {
	b := NewBroker(0)

	done := make(chan Answer, 1)
	go func() {
		got, _ := b.Ask(context.Background(), twoOptions())
		done <- got
	}()
	select {
	case got := <-done:
		if !got.Skipped {
			t.Errorf("answer = %+v, want it skipped", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask blocked with no interface attached")
	}
}

// TestAskRefusesAQuestionWithNoWayToAnswerIt: a question with neither options
// nor a text field is not a question, and waiting on it is a turn that never
// ends.
func TestAskRefusesAQuestionWithNoWayToAnswerIt(t *testing.T) {
	b := NewBroker(0)
	called := false
	b.SetPromptFunc(func(Request) <-chan Answer {
		called = true
		return make(chan Answer, 1)
	})

	got, err := b.Ask(context.Background(), Request{Question: "что делать?"})
	if err == nil {
		t.Error("Ask accepted a question with no options and no text field")
	}
	if !got.Skipped {
		t.Error("the answer was not marked skipped")
	}
	if called {
		t.Error("the interface was asked a question it could not answer")
	}
}

// TestTimerPicksTheRecommendedOption: the whole reason the timer exists.
func TestTimerPicksTheRecommendedOption(t *testing.T) {
	b := NewBroker(10 * time.Millisecond)
	b.SetPromptFunc(silent)

	got, err := b.Ask(context.Background(), twoOptions())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(got.Selected) != 1 || got.Selected[0] != "первый" {
		t.Errorf("answer = %+v, want the recommended option", got)
	}
	if !got.Auto {
		t.Error("a timer-chosen answer was not marked as a default, so the model will claim the user chose it")
	}
}

// TestTimerFallsBackToTheFirstOption: a question with no recommendation still
// has to resolve to something definite rather than to nothing.
func TestTimerFallsBackToTheFirstOption(t *testing.T) {
	b := NewBroker(10 * time.Millisecond)
	b.SetPromptFunc(silent)

	got, _ := b.Ask(context.Background(), Request{
		Question: "вопрос",
		Options:  []Option{{Label: "первый"}, {Label: "второй"}},
	})
	if len(got.Selected) != 1 || got.Selected[0] != "первый" {
		t.Errorf("answer = %+v, want the first option", got)
	}
}

// TestTimerIsOffByDefault: the user asked for it to be off unless configured,
// and a question that waits indefinitely is the documented behaviour.
func TestTimerIsOffByDefault(t *testing.T) {
	b := NewBroker(0)
	if b.Timeout() != 0 {
		t.Errorf("the default wait is %s, want it disabled", b.Timeout())
	}
	reply := make(chan Answer)
	b.SetPromptFunc(func(Request) <-chan Answer { return reply })

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = b.Ask(context.Background(), twoOptions())
	}()
	select {
	case <-done:
		t.Fatal("Ask returned without an answer and with the timer off")
	case <-time.After(50 * time.Millisecond):
	}
	reply <- Answer{Selected: []string{"поздно"}}
	<-done
}

// TestCancellationEndsTheWait: Esc has to reach a turn that is blocked on a
// question, or the user cannot stop anything the moment it asks something.
func TestCancellationEndsTheWait(t *testing.T) {
	b := NewBroker(0)
	b.SetPromptFunc(silent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := b.Ask(ctx, twoOptions())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if !got.Skipped {
		t.Error("a cancelled question was not marked skipped")
	}
}

// TestPerRequestTimeoutOverridesTheDefault: the model sometimes knows it is
// asking something the user will want time for.
func TestPerRequestTimeoutOverridesTheDefault(t *testing.T) {
	b := NewBroker(time.Hour)
	b.SetPromptFunc(silent)

	req := twoOptions()
	req.TimeoutSeconds = 1

	start := time.Now()
	got, _ := b.Ask(context.Background(), req)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the request's own timeout was ignored, waited %s", elapsed)
	}
	if !got.Auto {
		t.Error("the timeout did not produce a default answer")
	}
}

// TestCustomAnswerReachesTheModel: the option the model did not think of is
// the reason allow_custom exists.
func TestCustomAnswerReachesTheModel(t *testing.T) {
	b := NewBroker(0)
	b.SetPromptFunc(answerer(t, Answer{Custom: "свой вариант"}, nil))

	got, _ := b.Ask(context.Background(), Request{
		Question:    "вопрос",
		Options:     []Option{{Label: "первый"}},
		AllowCustom: true,
	})
	if got.Custom != "свой вариант" {
		t.Errorf("answer = %+v, want the typed variant", got)
	}
	if s := got.String(); !strings.Contains(s, "свой вариант") {
		t.Errorf("String() = %q, want it to carry the typed answer", s)
	}
}

// TestRequestReachesTheInterfaceIntact: the overlay renders exactly these
// fields, so a dropped one is a question the user cannot answer properly.
func TestRequestReachesTheInterfaceIntact(t *testing.T) {
	b := NewBroker(0)
	var seen Request
	b.SetPromptFunc(answerer(t, Answer{Selected: []string{"да"}}, &seen))

	req := Request{
		Question:    "вопрос",
		Options:     []Option{{Label: "да", Note: "поехали", Recommended: true}, {Label: "нет"}},
		Multi:       true,
		AllowCustom: true,
	}
	if _, err := b.Ask(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if seen.Question != req.Question || len(seen.Options) != 2 || seen.Options[0].Note != "поехали" {
		t.Errorf("the interface got %+v, want %+v", seen, req)
	}
	if !seen.Multi || !seen.AllowCustom {
		t.Error("multi/allow_custom did not reach the interface")
	}
}

// TestClosedChannelIsNotAnAnswer: a shutdown must not be read as a reply.
func TestClosedChannelIsNotAnAnswer(t *testing.T) {
	b := NewBroker(0)
	b.SetPromptFunc(func(Request) <-chan Answer {
		ch := make(chan Answer)
		close(ch)
		return ch
	})

	got, err := b.Ask(context.Background(), twoOptions())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !got.Skipped {
		t.Error("a closed channel was read as an answer")
	}
}

// TestStringTellsTheModelWhenNobodyAnswered: a decision the model did not get to
// make must not reach it looking like the user's.
func TestStringTellsTheModelWhenNobodyAnswered(t *testing.T) {
	if s := (Answer{Auto: true, Selected: []string{"да"}}).String(); !strings.Contains(s, "not confirm") {
		t.Errorf("String() = %q, want it to say the user did not confirm", s)
	}
	if s := (Answer{Skipped: true}).String(); !strings.Contains(s, "assumed") {
		t.Errorf("String() = %q, want it to say the agent should carry on", s)
	}
	if s := (Answer{}).String(); !strings.Contains(s, "ask again") {
		t.Errorf("String() = %q, want it to ask again on an empty answer", s)
	}
}

// TestMakeToolIsNamed: the sidebar lists what the agent can reach, so a tool
// built under the wrong name is a tool the user cannot see being offered.
func TestMakeToolIsNamed(t *testing.T) {
	tl, err := NewBroker(0).MakeTool()
	if err != nil {
		t.Fatalf("MakeTool: %v", err)
	}
	if tl.Name() != Name {
		t.Errorf("tool name = %q, want %q", tl.Name(), Name)
	}
}
