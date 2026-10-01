package todo

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"google.golang.org/genai"
)

func write(t *testing.T, s *Store, steps ...Item) error {
	t.Helper()
	return s.Replace(steps)
}

// TestReplaceNumbersStepsFromOne: the id is what the model uses in todo_set, so
// it has to be predictable — 1-based, in the order given.
func TestReplaceNumbersStepsFromOne(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "первый"}, Item{Content: "второй"}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got := s.Snapshot()
	if len(got) != 2 {
		t.Fatalf("got %d steps, want 2", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("ids = %d, %d; want 1, 2", got[0].ID, got[1].ID)
	}
}

// TestReplaceDropsWhatIsGone: a step the model left out is a step it no longer
// intends to take, which is the whole reason the list is rewritten rather than
// merged.
func TestReplaceDropsWhatIsGone(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "раз"}, Item{Content: "два"}); err != nil {
		t.Fatal(err)
	}
	if err := write(t, s, Item{Content: "только это"}); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot()
	if len(got) != 1 || got[0].Content != "только это" {
		t.Errorf("plan = %+v, want only the surviving step", got)
	}
}

// TestReplaceKeepsTheOldPlanOnError: a rejected plan must leave the previous one
// standing. Losing the plan because one step was empty would be a worse outcome
// than the empty step.
func TestReplaceKeepsTheOldPlanOnError(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "рабочий план"}, Item{Content: "ещё"}); err != nil {
		t.Fatal(err)
	}
	if err := write(t, s, Item{Content: "новый"}, Item{Content: "   "}); err == nil {
		t.Fatal("Replace accepted an empty step")
	}
	if got := s.Snapshot(); len(got) != 2 || got[0].Content != "рабочий план" {
		t.Errorf("plan = %+v, want the previous one untouched", got)
	}
}

// TestReplaceBoundsThePlan: a plan without a ceiling is not a plan, and every
// step of every attempt would ride along in the model's context forever.
func TestReplaceBoundsThePlan(t *testing.T) {
	s := New()
	many := make([]Item, maxItems+1)
	for i := range many {
		many[i] = Item{Content: "шаг"}
	}
	if err := write(t, s, many...); err == nil {
		t.Errorf("Replace accepted %d steps, want a refusal past %d", len(many), maxItems)
	}
}

// TestReplaceBoundsOneStep: a paragraph per step pushes everything else out of
// the context window.
func TestReplaceBoundsOneStep(t *testing.T) {
	s := New()
	long := Item{Content: strings.Repeat("я", maxItemRunes+1)}
	if err := write(t, s, long); err == nil {
		t.Error("Replace accepted an over-long step")
	}
}

// TestSetStatusMovesOneStep: the call a long task makes dozens of times, and
// the one that must not disturb the rest of the list.
func TestSetStatusMovesOneStep(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "раз"}, Item{Content: "два"}, Item{Content: "три"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(2, Done); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got := s.Snapshot()
	if got[1].Status != Done {
		t.Errorf("step 2 = %q, want done", got[1].Status)
	}
	if got[0].Status != Pending || got[2].Status != Pending {
		t.Error("SetStatus moved a step it was not asked about")
	}
}

// TestSetStatusRejectsUnknownStepAndStatus: a typo in an id must not mark a
// different step done, and an unknown status is not a status.
func TestSetStatusRejectsUnknownStepAndStatus(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "раз"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(9, Done); err == nil {
		t.Error("SetStatus accepted a step that is not in the plan")
	}
	if err := s.SetStatus(1, Status("готово")); err == nil {
		t.Error("SetStatus accepted an unknown status")
	}
	if s.Snapshot()[0].Status != Pending {
		t.Error("a rejected call still changed the plan")
	}
}

// TestUnknownStatusReadsAsPending: a word the model invented should cost it the
// status, not the plan. Refusing the whole list over one status would throw
// away the work the step describes.
func TestUnknownStatusReadsAsPending(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "шаг", Status: "выполнено"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot()[0].Status; got != Pending {
		t.Errorf("status = %q, want pending", got)
	}
}

// TestProgressCountsRatherThanPercentages: a percentage of one step is a lie in
// either direction.
func TestProgressCountsRatherThanPercentages(t *testing.T) {
	s := New()
	if err := write(t, s,
		Item{Content: "a", Status: Done},
		Item{Content: "b", Status: Done},
		Item{Content: "c", Status: InProgress},
		Item{Content: "d"},
	); err != nil {
		t.Fatal(err)
	}
	p := s.Progress()
	if p.Total != 4 || p.Done != 2 || p.InProgress != 1 {
		t.Errorf("progress = %+v, want 4 total, 2 done, 1 active", p)
	}
}

// TestClearEmptiesThePlan: a finished task and /new both want an empty list, not
// a list of steps nobody is going to do.
func TestClearEmptiesThePlan(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "шаг"}); err != nil {
		t.Fatal(err)
	}
	s.Clear()
	if got := s.Snapshot(); len(got) != 0 {
		t.Errorf("plan = %+v, want empty", got)
	}
}

// TestSnapshotIsACopy: the UI renders from this while the agent writes, and a
// shared slice would show a half-written plan.
func TestSnapshotIsACopy(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "оригинал"}); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot()
	got[0].Content = "подмена"
	if s.Snapshot()[0].Content != "оригинал" {
		t.Error("the snapshot shares storage with the store")
	}
}

// TestConcurrentUseIsSafe: the store is written by the ADK's goroutine and read
// by the event loop, so -race on this test is the check that matters.
func TestConcurrentUseIsSafe(t *testing.T) {
	s := New()
	if err := write(t, s, Item{Content: "шаг"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for range 50 {
				switch n % 3 {
				case 0:
					_ = s.SetStatus(1, InProgress)
				case 1:
					_ = s.Progress()
				default:
					_ = s.Snapshot()
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestMakeToolsNamesThemAll: the sidebar and the plan check read the names off
// the built tools, so a tool that is built but unnamed would be invisible in one
// and present in the other.
func TestMakeToolsNamesThemAll(t *testing.T) {
	s := New()
	tools, err := s.MakeTools()
	if err != nil {
		t.Fatalf("MakeTools: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range tools {
		got[tl.Name()] = true
	}
	for _, want := range Names {
		if !got[want] {
			t.Errorf("the tool set is missing %q", want)
		}
	}
	if len(got) != len(Names) {
		t.Errorf("built %d tools, want %d", len(got), len(Names))
	}
}

// toolSchema is the named tool's argument schema as the model will see it: the
// generated declaration, round-tripped through JSON so the assertions are about
// the wire rather than about the Go types behind it.
func toolSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	ts, err := New().MakeTools()
	if err != nil {
		t.Fatalf("MakeTools: %v", err)
	}
	for _, tl := range ts {
		if tl.Name() != name {
			continue
		}
		d, ok := tl.(interface {
			Declaration() *genai.FunctionDeclaration
		})
		if !ok {
			t.Fatalf("%s does not expose a declaration", name)
		}
		fd := d.Declaration()
		if fd == nil || fd.ParametersJsonSchema == nil {
			t.Fatalf("%s carries no argument schema", name)
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
	t.Fatalf("no tool named %q", name)
	return nil
}

func schemaProp(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	p, _ := props[name].(map[string]any)
	if p == nil {
		t.Fatalf("the schema has no property %q", name)
	}
	return p
}

// TestTodoWriteStepNeedsOnlyItsText is the regression this tool had.
//
// The step's status field carried no omitempty, so the generator marked it
// required. A model that wrote the obvious thing — a list of steps, each with
// just what it does — had the whole plan rejected before todoWrite ran, over
// the one field a step that has not started has no reason to state. A step with
// no status is a pending step, which is what normalise already assumed.
func TestTodoWriteStepNeedsOnlyItsText(t *testing.T) {
	schema := toolSchema(t, "todo_write")

	items := schemaProp(t, schema, "items")
	step, _ := items["items"].(map[string]any)
	if step == nil {
		t.Fatal("items has no item schema, so a model cannot tell what a step is")
	}
	required, _ := step["required"].([]any)
	if len(required) != 1 || required[0] != "content" {
		t.Errorf("a step requires %v, want only content — status has a sensible default", required)
	}
	// content itself must stay required: a step with no text is not a step.
	if _, ok := step["properties"].(map[string]any)["content"]; !ok {
		t.Error("the step schema has no content property")
	}
}

// TestPlanToolsDescribeTheirArguments: a field with no `jsonschema` tag reaches
// the model as a bare {"type":"string"}, which says nothing about what belongs
// there. That is how a status came to be left out and a plan rejected.
func TestPlanToolsDescribeTheirArguments(t *testing.T) {
	for _, name := range []string{"todo_write", "todo_set"} {
		schema := toolSchema(t, name)
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			t.Errorf("%s has no properties", name)
			continue
		}
		for field, raw := range props {
			p, _ := raw.(map[string]any)
			// A property with nothing but a type is the "a model is guessing"
			// case. todo_read takes no arguments, so it is not checked here.
			if desc, _ := p["description"].(string); strings.TrimSpace(desc) == "" {
				t.Errorf("%s: argument %q has no description", name, field)
			}
		}
	}
}

// TestTodoSetStillNeedsBothFields: unlike a step's status, there is no default
// for "which step" or "move it where", so both stay required.
func TestTodoSetStillNeedsBothFields(t *testing.T) {
	schema := toolSchema(t, "todo_set")
	required, _ := schema["required"].([]any)
	var hasID, hasStatus bool
	for _, r := range required {
		switch r {
		case "id":
			hasID = true
		case "status":
			hasStatus = true
		}
	}
	if !hasID || !hasStatus {
		t.Errorf("todo_set requires %v, want both id and status", required)
	}
}
