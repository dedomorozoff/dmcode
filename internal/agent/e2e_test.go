package agent_test

// A turn, end to end, against a fake endpoint.
//
// The wire is where dmcode's tools are most likely to be wrong, and it is the
// one place where a mistake is invisible from the inside. A tool whose
// argument schema does not match what a model actually sends is rejected by
// the endpoint *before the tool body runs*: the transcript shows a call and a
// schema-validation message and no result, and the tool did nothing at all
// however well it was described.
//
// That happened twice. `ask_user` took its options as a nested, closed object,
// so every shape a model reached for was refused. `todo_write` marked a step's
// status required, so a plan written without it was rejected whole. Both times
// the unit tests passed, because they exercised the Go types and not the JSON
// that goes on the wire — and the rejection came from the endpoint validating
// the schema dmcode put there, not from the ADK's own argument conversion,
// which is lenient. A lenient Go unmarshaller would not have helped.
//
// So this file drives whole turns against an httptest server that speaks the
// OpenAI chat protocol: the real chat client, the real agent, the real tool
// set, the real session store. The fake server is scripted rather than
// clever — it answers with a tool call, then with prose — and it records every
// request, so a test can ask what was actually sent rather than what the code
// believes it sends.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/llm"
	"github.com/dedomorozoff/dmcode/internal/memsession"
	"github.com/dedomorozoff/dmcode/internal/todo"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// endpoint is a scripted OpenAI-compatible chat server.
//
// It answers each request with the next script in the list and keeps every
// request body, so a test can ask what the client actually sent — which is the
// only question that matters when the complaint is "the endpoint rejected it".
type endpoint struct {
	mu       sync.Mutex
	requests []map[string]any
	// script is one answer per request, in order. A request past the end of the
	// script gets the last one again, so a test that expects two rounds does
	// not hang on a third.
	script [][]string
	// url is where the server listens.
	url string
}

func newEndpoint(t *testing.T, script ...[]string) *endpoint {
	t.Helper()
	e := &endpoint{script: script}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
			return
		}
		e.mu.Lock()
		e.requests = append(e.requests, body)
		n := len(e.requests) - 1
		if n >= len(e.script) {
			n = len(e.script) - 1
		}
		chunks := e.script[n]
		e.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("the response writer is not a flusher")
			return
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

// request returns the body of the nth request.
func (e *endpoint) request(t *testing.T, n int) map[string]any {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if n >= len(e.requests) {
		t.Fatalf("the endpoint was asked %d times, want at least %d", len(e.requests), n+1)
	}
	return e.requests[n]
}

// toolCall is the chunk that asks for one tool, and the chunk that ends the
// call.
func toolCall(name, args string) []string {
	return []string{
		fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":%q,"arguments":%q}}]}}]}`, name, args),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
}

// prose is the chunk that answers in words.
func prose(text string) []string {
	return []string{
		fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, text),
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	}
}

// toolMessage returns the content of the tool result a request carries.
//
// The message is found by its role rather than by its position: the number of
// messages before it depends on whether the request carried a system
// instruction and on how the model wrapped its own tool call, and a test that
// counted would be answering a different question from the one it means to
// ask.
func toolMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m == nil || m["role"] != "tool" {
			continue
		}
		content, _ := m["content"].(string)
		return content
	}
	t.Fatalf("the request carries no tool result:\n%v", msgs)
	return ""
}

// toolSet is the instrument set a real session runs on: the workspace tools,
// the plan tools, the question tool and the delegation tool.
func toolSet(t *testing.T) []tool.Tool {
	t.Helper()
	tools, err := dmtools.MakeTools()
	if err != nil {
		t.Fatal(err)
	}
	broker := ask.NewBroker(0)
	askTool, err := broker.MakeTool()
	if err != nil {
		t.Fatal(err)
	}
	tools = append(tools, askTool)
	subTool, err := dmagent.SubAgentTool(nil, tools, nil)
	if err != nil {
		t.Fatal(err)
	}
	return append(tools, subTool)
}

// runTurn drives one turn against the endpoint and returns what the model
// finally said.
func runTurn(t *testing.T, ep *endpoint, prompt string) string {
	t.Helper()
	m, err := llm.BuildLLM(context.Background(), config.Provider{
		BaseURL: ep.url + "/v1",
		Model:   "test-coder",
		API:     config.APIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := dmagent.BuildAgentMode(m, toolSet(t), dmagent.ModeAct)
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
	var answer string
	for ev, err := range r.Run(context.Background(), "u1", "s1",
		genai.NewContentFromText(prompt, genai.RoleUser),
		adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE}) {
		if err != nil {
			t.Fatalf("the turn failed: %v", err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.Text != "" {
				answer += p.Text
			}
		}
	}
	return answer
}

// TestATurnReachesTheWireAndTheToolComesBack is the whole stack, once.
//
// The model asks for a file, the tool reads it, the result goes back to the
// model, and the model answers. Every part of that is dmcode's own code: the
// chat client, the agent, the tool set, the session store. The only thing
// fake is the endpoint, and it is fake in the one way that lets the test see
// the wire.
func TestATurnReachesTheWireAndTheToolComesBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("the file says hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dmtools.SetRoot(dir); err != nil {
		t.Fatal(err)
	}

	ep := newEndpoint(t,
		toolCall("read_file", `{"path":"notes.txt"}`),
		prose("the file says hello"),
	)

	answer := runTurn(t, ep, "what does notes.txt say?")
	if !strings.Contains(answer, "the file says hello") {
		t.Errorf("the turn ended with %q, want the model's answer", answer)
	}

	// The tool really ran: the second request carries its result, and the
	// result is the file's own text. A store that hid the result would send
	// the model round again, and a tool that never ran would send an error.
	second := ep.request(t, 1)
	msgs, _ := second["messages"].([]any)
	carried := false
	for i, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m == nil || m["role"] != "tool" {
			continue
		}
		carried = true
		content, _ := m["content"].(string)
		if !strings.Contains(content, "the file says hello") {
			t.Errorf("the tool result on the wire is %q, want the file's text", content)
		}
		_ = i
	}
	if !carried {
		t.Error("the second request carries no tool result, so the model never saw what the tool found")
	}
}

// TestTheDeclarationsReachTheWire checks the thing the endpoint would reject.
//
// Every tool is packed into a declaration the way the agent packs it, and the
// declaration is read as the endpoint would see it: an object with named
// properties, and every property described. A property with nothing but a type
// tells the model nothing about what belongs there, and a model that guesses
// wrong is refused before the tool body runs.
func TestTheDeclarationsReachTheWire(t *testing.T) {
	for _, tl := range toolSet(t) {
		name := tl.Name()
		schema := declarationSchema(t, tl)
		if kind, _ := schema["type"].(string); kind != "object" {
			t.Errorf("%s: the schema is a %q, want an object", name, kind)
		}
		props, _ := schema["properties"].(map[string]any)
		for field, raw := range props {
			p, _ := raw.(map[string]any)
			if p == nil {
				t.Errorf("%s: argument %q has no schema", name, field)
				continue
			}
			if desc, _ := p["description"].(string); strings.TrimSpace(desc) == "" {
				t.Errorf("%s: argument %q has no description — a bare type says nothing", name, field)
			}
			// A nested object is a shape the model has to guess, and every wrong
			// guess is a rejection it cannot learn from. That is what
			// `ask_user`'s options used to be: a list of objects with a label
			// and a note, and no model that guessed it exactly.
			//
			// An *array* of objects is a different thing: the shape inside it
			// is described, and `todo_write`'s steps are exactly that.
			if kind, _ := p["type"].(string); kind == "object" {
				t.Errorf("%s: argument %q is a nested object; that is the shape the model gets wrong", name, field)
			}
		}
	}
}

// requiredArgs is what a model has to fill in, per tool.
//
// A field that becomes required is a contract with the *endpoint*, not with
// the tool: a model that leaves it out has its whole call rejected before the
// body runs, and the rejection is a hard error it cannot learn from. That is
// what happened to `todo_write`, whose step grew a required status, and to
// `ask_user`, whose options were a shape no model guessed right.
//
// So the required set is written down here, and a tool that grows one fails
// this test until somebody decides it on purpose. The list is a specification,
// not a snapshot: "this argument is required" is a claim about what the
// endpoint will refuse, and it should be read as one.
var requiredArgs = map[string][]string{
	"read_file":   {"path"},
	"write_file":  {"path", "content"},
	"edit_file":   {"path"},
	"list_dir":    {},
	"grep":        {"pattern"},
	"glob":        {"pattern"},
	"run_command": {"command"},
	"move_file":   {"destination", "source"},
	"delete_file": {"path"},
	"web_search":  {"query"},
	"fetch_url":   {"url"},
	"project_map": {},
	"todo_write":  {"items"},
	"todo_set":    {"id", "status"},
	"todo_read":   {},
	"ask_user":    {"options", "question"},
	"sub_agent":   {"task"},
}

// TestTheRequiredArgumentsAreTheOnesThatWereDecided pins the table above
// against the schemas that go on the wire.
func TestTheRequiredArgumentsAreTheOnesThatWereDecided(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range toolSet(t) {
		name := tl.Name()
		seen[name] = true
		schema := declarationSchema(t, tl)
		var got []string
		if raw, ok := schema["required"].([]any); ok {
			for _, r := range raw {
				got = append(got, fmt.Sprint(r))
			}
		}
		sort.Strings(got)
		want := requiredArgs[name]
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s requires %v, want %v — a new required argument is a change to what the endpoint will accept", name, got, want)
		}
	}
	for name := range requiredArgs {
		if !seen[name] {
			t.Errorf("the tool set has no %q, so the table above is describing a tool that is not there", name)
		}
	}
}

// TestThePlanToolAcceptsThePayloadAModelNaturallySends is the `todo_write`
// regression, driven end to end.
//
// The payload is the one a model reaches for: a list of steps, each saying
// what it does and nothing else. A step that has not started has no status to
// state, and the tool that says so has to accept it — through the wire, not
// only in its own unit test.
func TestThePlanToolAcceptsThePayloadAModelNaturallySends(t *testing.T) {
	if err := dmtools.SetRoot(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	todo.Default.Clear()

	ep := newEndpoint(t,
		toolCall("todo_write", `{"items":[{"content":"read the code"},{"content":"fix it"}]}`),
		prose("the plan is published"),
	)
	answer := runTurn(t, ep, "make a plan")
	if !strings.Contains(answer, "the plan is published") {
		t.Errorf("the turn ended with %q, want the model's answer", answer)
	}

	items := todo.Default.Snapshot()
	if len(items) != 2 {
		t.Fatalf("the plan holds %d steps, want 2 — the tool did not run", len(items))
	}
	if items[0].Content != "read the code" || items[1].Content != "fix it" {
		t.Errorf("the plan is %+v, want the two steps the model sent", items)
	}
	// The result reached the model rather than being refused on the way.
	result := toolMessage(t, ep.request(t, 1))
	if !strings.Contains(result, "2") {
		t.Errorf("the tool result on the wire is %q, want the plan's size", result)
	}
}

// TestTheQuestionToolAcceptsThePayloadAModelNaturallySends is the `ask_user`
// regression, driven end to end.
//
// The payload is the one a model reaches for: the question and a list of
// options as plain strings. Options as objects — with a label, a note, a
// recommended flag — were what the tool used to ask for, and every model that
// did not guess that shape exactly had its call refused before the question
// was ever asked.
func TestTheQuestionToolAcceptsThePayloadAModelNaturallySends(t *testing.T) {
	if err := dmtools.SetRoot(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	ep := newEndpoint(t,
		toolCall("ask_user", `{"question":"Which library?","options":["the first","the second"],"recommended":"the first"}`),
		prose("I took the first"),
	)
	answer := runTurn(t, ep, "ask me")
	if !strings.Contains(answer, "I took the first") {
		t.Errorf("the turn ended with %q, want the model's answer", answer)
	}

	// Nobody is there to answer, so the tool says so and the turn carries on —
	// which is the whole point of returning every outcome as text.
	result := toolMessage(t, ep.request(t, 1))
	if !strings.Contains(strings.ToLower(result), "skip") {
		t.Errorf("the tool result on the wire is %q, want it to say nobody answered", result)
	}
}

// declarationSchema packs a tool into a declaration and reads the argument
// schema out of it, the way the agent does on its way to the wire.
func declarationSchema(t *testing.T, tl tool.Tool) map[string]any {
	t.Helper()
	packer, ok := tl.(interface {
		ProcessRequest(adkagent.Context, *model.LLMRequest) error
	})
	if !ok {
		t.Fatalf("the tool %q does not pack a declaration", tl.Name())
	}
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{}}
	if err := packer.ProcessRequest(nil, req); err != nil {
		t.Fatalf("the tool %q does not pack: %v", tl.Name(), err)
	}
	for _, group := range req.Config.Tools {
		for _, d := range group.FunctionDeclarations {
			if d.Name != tl.Name() {
				continue
			}
			if d.Description == "" {
				t.Errorf("the tool %q has no description", tl.Name())
			}
			if d.ParametersJsonSchema == nil {
				t.Fatalf("the tool %q carries no argument schema", tl.Name())
			}
			raw, err := json.Marshal(d.ParametersJsonSchema)
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatalf("no declaration for %q", tl.Name())
	return nil
}
