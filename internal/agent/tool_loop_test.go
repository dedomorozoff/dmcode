package agent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/memsession"
)

// scriptedModel answers with a tool call until the tool has been run, and with
// prose once its result is in the conversation it was handed.
//
// A model that only ever calls a tool is the shape of the loop this guards: if
// the tool's result never comes back in the next request, this model has no way
// to know it already asked, and asks again. A correct store makes the loop
// impossible by construction.
type scriptedModel struct {
	asked  int
	seen   []int // prompt length of each request
	wanted int   // how many tool results the model wants to see
}

func (m *scriptedModel) Name() string { return "scripted" }

func (m *scriptedModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		results := 0
		for _, c := range req.Contents {
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.FunctionResponse != nil {
					results++
				}
			}
		}
		m.seen = append(m.seen, results)
		// A store that hides the result sends this model round forever, so the
		// test would hang rather than fail. Bailing out turns that into the
		// assertion below, which says what actually went wrong.
		if m.asked >= maxScriptedCalls {
			yield(nil, errTooManyCalls)
			return
		}
		if results < m.wanted {
			m.asked++
			yield(&model.LLMResponse{
				Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{Name: "list_dir", Args: map[string]any{"path": "."}}},
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

// maxScriptedCalls is far more than a healthy turn needs. A turn that calls one
// tool and then answers uses two requests.
const maxScriptedCalls = 8

var errTooManyCalls = errors.New("the model kept calling the same tool")

// TestToolLoopSeesItsOwnResults is the load-bearing check for the whole agent:
// a model that called a tool has to be shown that tool's result on the next
// request.
//
// The store this runs on is dmcode's, not the ADK's, and the difference is not
// cosmetic. A store that hands the runner a snapshot makes every request carry
// an empty conversation, so the model repeats one identical tool call until the
// runner stops it — which is exactly the "list_dir loops forever" report.
func TestToolLoopSeesItsOwnResults(t *testing.T) {
	listDir, err := functiontool.New(functiontool.Config{Name: "list_dir"},
		func(adkagent.Context, struct{}) (string, error) { return "main.go", nil })
	if err != nil {
		t.Fatal(err)
	}

	m := &scriptedModel{wanted: 1}
	a, err := BuildAgentWithModel(m, []tool.Tool{listDir})
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{
		AppName: "dmcode", Agent: a,
		SessionService: memsession.NewMemory(), AutoCreateSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var lastText string
	for ev, err := range r.Run(context.Background(), "u1", "s1",
		genai.NewContentFromText("что тут?", genai.RoleUser),
		adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE}) {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if ev.LLMResponse.Content == nil {
			continue
		}
		for _, p := range ev.LLMResponse.Content.Parts {
			if p.Text != "" {
				lastText += p.Text
			}
		}
	}

	if m.asked != 1 {
		t.Errorf("model called the tool %d times, want 1 — it is not seeing the result", m.asked)
	}
	if want := []int{0, 1}; !equalInts(m.seen, want) {
		t.Errorf("tool results visible per request = %v, want %v", m.seen, want)
	}
	if lastText == "" {
		t.Error("the turn ended without a reply")
	}
}

// TestUserPromptReachesTheModel: the same defect, one turn earlier. With an
// empty prompt the model is answering a question nobody asked, which is what a
// user sees as an agent that ignores them and starts exploring.
func TestUserPromptReachesTheModel(t *testing.T) {
	m := &seenModel{}
	a, err := BuildAgentWithModel(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{
		AppName: "dmcode", Agent: a,
		SessionService: memsession.NewMemory(), AutoCreateSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range r.Run(context.Background(), "u1", "s1",
		genai.NewContentFromText("привет", genai.RoleUser),
		adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE}) {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	}
	if m.prompt != "привет" {
		t.Errorf("model saw %q, want %q", m.prompt, "привет")
	}
}

// seenModel records the text of the first request it is handed.
type seenModel struct{ prompt string }

func (m *seenModel) Name() string { return "seen" }

func (m *seenModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for _, c := range req.Contents {
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.Text != "" {
					m.prompt = p.Text
				}
			}
		}
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "ok"}}},
			FinishReason: genai.FinishReasonStop,
		}, nil)
	}
}

// TestToolArgumentsSurviveTheSchemaConversion is the other half of a working
// tool call: the declaration has to carry its argument names, or the model is
// left guessing which is a loop of its own.
func TestToolArgumentsSurviveTheSchemaConversion(t *testing.T) {
	listDir, err := functiontool.New(functiontool.Config{
		Name:        "list_dir",
		Description: "Lists directory entries.",
	}, func(adkagent.Context, struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive,omitempty"`
	}) (string, error) {
		return "x", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{}}
	pt, ok := listDir.(interface {
		ProcessRequest(adkagent.Context, *model.LLMRequest) error
	})
	if !ok {
		t.Fatal("the tool does not pack a declaration")
	}
	if err := pt.ProcessRequest(nil, req); err != nil {
		t.Fatal(err)
	}
	var decl *genai.FunctionDeclaration
	for _, tl := range req.Config.Tools {
		for _, d := range tl.FunctionDeclarations {
			if d.Name == "list_dir" {
				decl = d
			}
		}
	}
	if decl == nil {
		t.Fatal("no declaration for list_dir")
	}
	b, _ := json.Marshal(decl.ParametersJsonSchema)
	if len(b) == 0 || string(b) == "null" {
		t.Fatal("the declaration carries no argument schema")
	}
	if decl.ParametersJsonSchema == nil {
		t.Fatal("ParametersJsonSchema is nil")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
