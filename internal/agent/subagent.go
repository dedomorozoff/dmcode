package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/llm"
	"github.com/dedomorozoff/dmcode/internal/memsession"
)

// subInstruction is what a delegated agent is told. It is a separate
// instruction, not a prefix of the main one: a sub-agent is a specialist with
// one job, and the main agent's rules about editing the workspace would only
// tell it to do things it must not do.
const subInstruction = `You are a research sub-agent working for another agent.

You investigate and report. You never modify anything: you have no write tools, and you must not try to work around that.

Method:
1. Search broadly first, then read what looks relevant. Do not stop at the first hit.
2. Cite exact file paths and line numbers for everything you report. A claim without a location is a guess.
3. Report what you found, what you expected to find and did not, and anything that contradicts the task as given.
4. Stop when you can answer the task. A short report that answers it beats a long one that pads it.

Write for an agent that cannot see your screen: no "as shown above", no "the file you mentioned" unless the task said it.`

// SubAgentName is the tool name, for the caller keeping the read-only set in
// step with the full one.
const SubAgentName = "sub_agent"

// SubEvent is a progress note from inside a delegation, so the user can see
// what the sub-agent is doing instead of watching one spinner for a minute.
type SubEvent struct {
	// Task is the delegation the note belongs to, truncated for display.
	Task string
	// Text is what happened.
	Text string
}

// subResult is what the parent agent gets back.
type subResult struct {
	Report string   `json:"report"`
	Files  []string `json:"files_read"`
	// Note is why the report is missing, when it is. An empty note beside an
	// empty report would read as "there is nothing to find", which is a claim.
	Note string `json:"note,omitempty"`
}

// subArgs is the tool's input.
type subArgs struct {
	// Task is what the sub-agent must find out. One job: a question with three
	// unrelated halves produces a report that answers none of them well.
	Task string `json:"task"`
	// Context is what the parent already knows, so the sub-agent does not
	// rediscover it.
	Context string `json:"context,omitempty"`
	// Report says what shape of answer is wanted: findings, a list of call
	// sites, a summary of behaviour.
	Report string `json:"report,omitempty"`
}

// Bounds on one delegation. Both exist because a sub-agent with no ceiling is a
// second main agent: the same context window, the same tokens, and nobody
// watching it.
const (
	subDefaultTimeout = 5 * time.Minute
	subMaxReportChars = 8000
	subTaskPreview    = 60
)

// subRunner holds what one built tool needs: the endpoint pool to delegate to,
// the instruments to delegate with, and the way to tell the user about it.
type subRunner struct {
	pool     []config.Provider
	readOnly []tool.Tool
	notify   func(SubEvent)
	timeout  time.Duration
}

// subToolSet is the instrument set a delegation may use.
//
// Two tools are dropped however they arrive. Delegating further turns one
// question into an unbounded tree of them, each with its own context window.
// Asking the user from inside a delegation puts a prompt on screen whose
// context the user cannot see, and whose answer would come back to an agent that
// is not the one holding the conversation.
func subToolSet(readOnly []tool.Tool) []tool.Tool {
	sub := make([]tool.Tool, 0, len(readOnly))
	for _, t := range readOnly {
		switch t.Name() {
		case SubAgentName, ask.Name:
			continue
		}
		sub = append(sub, t)
	}
	return sub
}

// SubAgentTool builds the delegation tool.
//
// readOnly is passed in rather than filtered here because the caller already
// holds the exact set the sidebar shows for the current mode, and a second
// filtering rule would be a second thing that could disagree with it.
func SubAgentTool(pool []config.Provider, readOnly []tool.Tool, notify func(SubEvent)) (tool.Tool, error) {
	h := &subRunner{
		pool:     pool,
		readOnly: subToolSet(readOnly),
		notify:   notify,
		timeout:  subDefaultTimeout,
	}
	return functiontool.New(functiontool.Config{
		Name: SubAgentName,
		Description: "Delegates one research task to a separate agent with its own context, and returns its " +
			"report. Use it for work that would fill your context with material you need only once: surveying " +
			"how something works across many files, tracing a call path, listing every call site, checking a " +
			"suspicion. Give it one job, say what you already know, and say what shape of report you want. " +
			"The sub-agent cannot write anything and cannot ask the user anything, so what it reports back is " +
			"yours to act on. Do not use it for work you can do in a couple of tool calls.",
	}, h.run)
}

// run executes one delegation.
func (h *subRunner) run(ctx agent.Context, in subArgs) (subResult, error) {
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return subResult{}, fmt.Errorf("the task is empty — say what the sub-agent should find out")
	}
	preview := subPreview(task)
	h.say(preview, "started")

	// Bounded by a wall clock as well as by the context: a delegation that hangs
	// must not take the user's turn down with it.
	turnCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	m, err := h.buildModel(turnCtx)
	if err != nil {
		h.say(preview, "could not start: "+err.Error())
		return subResult{Note: "the sub-agent could not be started: " + err.Error()}, nil
	}

	sub, err := llmagent.New(llmagent.Config{
		Name:        "dmcode-sub",
		Model:       m,
		Description: "Research sub-agent: reads the workspace and reports findings.",
		Instruction: subInstruction,
		Tools:       h.readOnly,
	})
	if err != nil {
		return subResult{Note: "the sub-agent could not be built: " + err.Error()}, nil
	}

	// Its own session, always. A shared one would put the sub-agent's reading
	// into the parent's memory, and the parent would then carry a transcript it
	// never asked for into every later turn. It is also what keeps a delegation
	// out of /sessions: a throwaway conversation nobody can switch to.
	r, err := runner.New(runner.Config{
		AppName:           "dmcode",
		Agent:             sub,
		SessionService:    memsession.NewMemory(),
		AutoCreateSession: true,
	})
	if err != nil {
		return subResult{Note: "the sub-agent could not run: " + err.Error()}, nil
	}

	prompt := task
	if c := strings.TrimSpace(in.Context); c != "" {
		prompt += "\n\nWhat is already known: " + c
	}
	if w := strings.TrimSpace(in.Report); w != "" {
		prompt += "\n\nWhat the report should contain: " + w
	}

	msg := genai.NewContentFromText(prompt, genai.RoleUser)
	var (
		report strings.Builder
		files  = map[string]bool{}
	)
	for ev, err := range r.Run(turnCtx, "user", "sub", msg, agent.RunConfig{StreamingMode: agent.StreamingModeNone}) {
		if err != nil {
			// A delegation that failed is still worth returning: the parent
			// decides whether to carry on, and an error it can read is more use
			// than a failed tool call it cannot.
			h.say(preview, "failed: "+err.Error())
			return subResult{
				Report: trimReport(report.String()),
				Files:  sortedKeys(files),
				Note:   "the sub-agent stopped early: " + err.Error(),
			}, nil
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				report.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				h.say(preview, part.FunctionCall.Name)
				if name, ok := part.FunctionCall.Args["path"].(string); ok && name != "" {
					files[name] = true
				}
			}
		}
	}

	out := trimReport(report.String())
	if out == "" {
		h.say(preview, "came back with nothing")
		return subResult{
			Note: "the sub-agent returned no report — it may have found nothing, or spent its turn on tool calls",
		}, nil
	}
	h.say(preview, fmt.Sprintf("done, %d characters", len(out)))
	return subResult{Report: out, Files: sortedKeys(files)}, nil
}

// buildModel makes the client the sub-agent will use.
//
// A plain client rather than the failover pool: a delegation already sits inside
// a pooled turn, and wrapping it in a second pool would let one slow endpoint
// spend twice the wait before the failure reached the parent.
func (h *subRunner) buildModel(ctx context.Context) (model.LLM, error) {
	if len(h.pool) == 0 {
		return nil, fmt.Errorf("no provider is configured")
	}
	return llm.BuildLLM(ctx, h.pool[0])
}

// say reports progress, if anything is listening. A nil notifier is normal: the
// tool is built before the program exists.
func (h *subRunner) say(task, text string) {
	if h.notify != nil {
		h.notify(SubEvent{Task: task, Text: text})
	}
}

// subPreview is the short form of a task, for the transcript.
func subPreview(task string) string {
	task = strings.TrimSpace(strings.ReplaceAll(task, "\n", " "))
	r := []rune(task)
	if len(r) <= subTaskPreview {
		return task
	}
	return string(r[:subTaskPreview]) + "…"
}

// trimReport cuts a report to what the parent can afford, marking the cut so the
// agent does not read a truncated list as a complete one.
func trimReport(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= subMaxReportChars {
		return s
	}
	return string(r[:subMaxReportChars]) + "\n… [report truncated]"
}

// sortedKeys is the file list in a stable order, so two identical delegations
// produce identical results.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
