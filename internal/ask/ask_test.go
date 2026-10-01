package ask

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
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

// declarationSchema is the tool's argument schema as the model will see it: the
// generated declaration, round-tripped through JSON so the assertions are about
// the wire rather than about the Go types behind it.
func declarationSchema(t *testing.T) map[string]any {
	t.Helper()
	tl, err := NewBroker(0).MakeTool()
	if err != nil {
		t.Fatalf("MakeTool: %v", err)
	}
	d, ok := tl.(interface {
		Declaration() *genai.FunctionDeclaration
	})
	if !ok {
		t.Fatal("the tool does not expose a declaration")
	}
	fd := d.Declaration()
	if fd == nil || fd.ParametersJsonSchema == nil {
		t.Fatal("the declaration carries no argument schema")
	}
	raw, err := json.Marshal(fd.ParametersJsonSchema)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func prop(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	p, _ := props[name].(map[string]any)
	if p == nil {
		t.Fatalf("the schema has no property %q", name)
	}
	return p
}

// TestOptionsArePlainStrings is the regression this tool actually had.
//
// It took the options as a list of {label, note, recommended} objects, which the
// generator emits with additionalProperties:false and no description of any
// field. Models wrote ["a", "b"] and were told the items had to be objects;
// they wrote {"Label": …, "value": …} and were told "value" was unexpected. Both
// were hard errors raised before the tool body ran, so the tool did nothing at
// all however it was described. Plain strings are the shape a model reaches for
// first, and the only one with nothing to get wrong.
func TestOptionsArePlainStrings(t *testing.T) {
	schema := declarationSchema(t)

	opts := prop(t, schema, "options")
	items, _ := opts["items"].(map[string]any)
	if items == nil {
		t.Fatal("options has no item schema, so a model cannot tell what an option is")
	}
	if got := items["type"]; got != "string" {
		t.Errorf("an option is described as %v, want a plain string", got)
	}

	// The arguments themselves are an object, and have to be. What must not
	// appear is an object *inside* them: every nested object is a shape the model
	// has to guess, and every wrong guess is a rejection it cannot learn from.
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		if kind, _ := node["type"].(string); kind == "object" {
			t.Errorf("%s is a nested object; that is the shape the model gets wrong", path)
		}
		if props, ok := node["properties"].(map[string]any); ok {
			for name, sub := range props {
				if m, ok := sub.(map[string]any); ok {
					walk(m, path+"."+name)
				}
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, path+"[]")
		}
	}
	props, _ := schema["properties"].(map[string]any)
	for name, raw := range props {
		if m, ok := raw.(map[string]any); ok {
			walk(m, name)
		}
	}
}

// TestEveryArgumentIsDescribed: a field with no `jsonschema` tag reaches the
// model as a bare {"type":"string"}, which tells it nothing about what to put
// there. That is how task/context/report came to be written as answers.
func TestEveryArgumentIsDescribed(t *testing.T) {
	schema := declarationSchema(t)
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		t.Fatal("the schema has no properties")
	}
	for name, raw := range props {
		p, _ := raw.(map[string]any)
		if desc, _ := p["description"].(string); strings.TrimSpace(desc) == "" {
			t.Errorf("argument %q has no description, so the model is guessing", name)
		}
	}
}

// TestRecommendedIsOptionalAndAString: it names one of the options by its text,
// so it must not be required — a model that has no preference omits it.
func TestRecommendedIsOptionalAndAString(t *testing.T) {
	schema := declarationSchema(t)
	if got := prop(t, schema, "recommended")["type"]; got != "string" {
		t.Errorf("recommended is %v, want a string naming one of the options", got)
	}
	required, _ := schema["required"].([]any)
	for _, r := range required {
		if r == "recommended" {
			t.Error("recommended is required, so a model with no preference cannot answer without inventing one")
		}
	}
	// The two fields that carry the question itself must still be required: a
	// question with no options and no text field is not answerable at all.
	var hasQuestion, hasOptions bool
	for _, r := range required {
		switch r {
		case "question":
			hasQuestion = true
		case "options":
			hasOptions = true
		}
	}
	if !hasQuestion || !hasOptions {
		t.Errorf("required = %v, want it to include question and options", required)
	}
}

// TestRequestFromArgsMarksTheRecommendedRow: the model names its preference by
// copying an option's text, and that text is what ends up highlighted.
func TestRequestFromArgsMarksTheRecommendedRow(t *testing.T) {
	req := askArgs{
		Question:    "  что делать?  ",
		Options:     []string{"  починить сборку  ", "добавить тест", "  "},
		Recommended: "добавить тест",
		Multi:       true,
	}.request()

	if req.Question != "что делать?" {
		t.Errorf("question = %q, want it trimmed", req.Question)
	}
	if len(req.Options) != 2 {
		t.Fatalf("got %d options, want the two non-blank ones: %+v", len(req.Options), req.Options)
	}
	if req.Options[0].Label != "починить сборку" {
		t.Errorf("first option = %q, want it trimmed", req.Options[0].Label)
	}
	if req.Options[0].Recommended {
		t.Error("the wrong option was marked recommended")
	}
	if !req.Options[1].Recommended {
		t.Error("the named option was not marked recommended")
	}
	if !req.Multi {
		t.Error("multi did not survive the conversion")
	}
}

// TestRequestFromArgsIgnoresARecommendationThatNamesNothing: a label that is not
// on the list would highlight nothing, and a default the user cannot see is
// worse than none.
func TestRequestFromArgsIgnoresARecommendationThatNamesNothing(t *testing.T) {
	req := askArgs{
		Question:    "вопрос",
		Options:     []string{"первый", "второй"},
		Recommended: "третий",
	}.request()
	for i, o := range req.Options {
		if o.Recommended {
			t.Errorf("option %d (%q) was marked recommended from a label that is not on the list", i, o.Label)
		}
	}
}

// TestRequestFromArgsWithNoUsableOptionsAsksForAFreeTextAnswer: a model that
// sends an empty list has not asked a question, but it has asked something. The
// broker refuses it outright, so the text field is turned on here to give the
// user a way to answer at all.
func TestRequestFromArgsWithNoUsableOptionsAsksForAFreeTextAnswer(t *testing.T) {
	req := askArgs{Question: "вопрос", Options: []string{"", "   "}}.request()
	if len(req.Options) != 0 {
		t.Errorf("got %d options, want none", len(req.Options))
	}
	if req.AllowCustom {
		t.Error("allow_custom was invented; the broker has to be the one to refuse this")
	}
}

// oneCallModel asks for a single tool call with fixed arguments, then answers.
type oneCallModel struct {
	name string
	args map[string]any
	ran  bool
	// results counts the tool results the model was shown, which is how this
	// tells "the call was rejected" from "the tool ran and said no".
	results int
}

func (m *oneCallModel) Name() string { return "oneCall" }

func (m *oneCallModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for _, c := range req.Contents {
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.FunctionResponse != nil {
					m.results++
				}
			}
		}
		if !m.ran {
			m.ran = true
			yield(&model.LLMResponse{
				Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "c1", Name: m.name, Args: m.args}},
				}},
				FinishReason: genai.FinishReasonStop,
			}, nil)
			return
		}
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "готово"}}},
			FinishReason: genai.FinishReasonStop,
		}, nil)
	}
}

// TestTheShapesModelsSendSurviveTheRealDispatch is the end-to-end version of
// TestOptionsArePlainStrings.
//
// The schema assertions above describe what the model is *shown*; this puts a
// shape a model was actually observed to produce through a real turn:
//
//	options: ["Список возможных действий", "Выбор конкретного действия", …]
//	options: [{"Label": "…", "value": "…"}]
//
// The second is not exercised here because it is not the shape this tool
// declares any more — that was the point of the change. Note where the reported
// failure came from, because it decides what a regression looks like: the ADK's
// own argument conversion accepts both of those shapes against the old
// declaration, so the rejection was the *endpoint* validating the tool schema
// dmcode put on the wire. That is why the fix is to the declared schema and not
// to a lenient unmarshaller, and why TestOptionsArePlainStrings is the test that
// has to keep passing.
func TestTheShapesModelsSendSurviveTheRealDispatch(t *testing.T) {
	var seen Request
	b := NewBroker(0)
	// The user picks the option the model marked, and the label comes back
	// verbatim: that round trip is what proves the model's own text survived the
	// schema rather than being normalised or truncated on the way in.
	b.SetPromptFunc(func(req Request) <-chan Answer {
		seen = req
		ch := make(chan Answer, 1)
		for _, o := range req.Options {
			if o.Recommended {
				ch <- Answer{Selected: []string{o.Label}}
				return ch
			}
		}
		ch <- Answer{Selected: []string{"пропущено"}}
		return ch
	})
	tl, err := b.MakeTool()
	if err != nil {
		t.Fatal(err)
	}

	a, err := llmagent.New(llmagent.Config{Name: "dmcode", Model: &oneCallModel{
		name: "ask_user",
		// The shape the model reached for first, verbatim.
		args: map[string]any{
			"question":    "Пожалуйста, уточните, какие варианты рассматривать?",
			"options":     []any{"Список возможных действий", "Выбор конкретного действия", "Не знаю"},
			"recommended": "Выбор конкретного действия",
		},
	}, Instruction: "inst", Tools: []tool.Tool{tl}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := runTurn(t, a)
	if err != nil {
		t.Fatalf("the turn failed, which is what a rejected call looks like: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("the tool was never dispatched: the argument shape was rejected by the schema")
	}
	if len(seen.Options) != 3 {
		t.Errorf("the user was shown %d options, want 3: %+v", len(seen.Options), seen.Options)
	}
	if len(seen.Options) == 3 && !seen.Options[1].Recommended {
		t.Errorf("the recommended option was not the one the model named: %+v", seen.Options)
	}
	if !strings.Contains(results[0], "Выбор конкретного действия") {
		t.Errorf("the tool returned %q, want the option the user chose", results[0])
	}
}

// runTurn drives one turn and returns the tool results the model was shown.
//
// A rejected call never appears among them, which is what makes this the right
// instrument: the failure being guarded against was invisible in the transcript
// except as a question that never reached the user.
func runTurn(t *testing.T, a agent.Agent) ([]string, error) {
	t.Helper()
	r, err := runner.New(runner.Config{
		AppName: "dmcode", Agent: a,
		SessionService: session.InMemoryService(), AutoCreateSession: true,
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for ev, err := range r.Run(context.Background(), "u", "s",
		genai.NewContentFromText("уточни", genai.RoleUser),
		agent.RunConfig{StreamingMode: agent.StreamingModeSSE}) {
		if err != nil {
			return out, err
		}
		if ev.LLMResponse.Content == nil {
			continue
		}
		for _, p := range ev.LLMResponse.Content.Parts {
			if p == nil || p.FunctionResponse == nil {
				continue
			}
			b, mErr := json.Marshal(p.FunctionResponse.Response)
			if mErr != nil {
				t.Fatal(mErr)
			}
			out = append(out, p.FunctionResponse.Name+": "+string(b))
		}
	}
	return out, nil
}
