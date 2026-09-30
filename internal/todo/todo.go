// Package todo holds the agent's working plan: the list of steps it intends to
// take, which step it is on, and which are done.
//
// It is a package-level singleton rather than a field on the UI model because
// both sides need it — the model writes the plan through a tool, the user reads
// it with /todo and in the sidebar — and the tool is built in main.go, long
// before the model exists to hand a reference to. The mutex is what makes that
// safe: a tool runs on the ADK's goroutine while /todo runs on the event loop.
package todo

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Status is where one step stands.
type Status string

const (
	// Pending is a step that has not been started.
	Pending Status = "pending"
	// InProgress is the step being worked on. At most one is meant to be, but
	// nothing enforces it: the model is reporting a plan, not being audited.
	InProgress Status = "in_progress"
	// Done is a finished step.
	Done Status = "done"
)

// Item is one step of the plan.
type Item struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
	Status  Status `json:"status"`
}

// maxItems bounds the plan. A list that grows without limit is not a plan, and
// the model's context would carry every step of every attempt into the next
// turn.
const maxItems = 50

// maxItemRunes bounds one step, so a model that writes a paragraph per step
// cannot push everything else out of the context window.
const maxItemRunes = 300

// Store is the plan, guarded for the two goroutines that touch it.
type Store struct {
	mu    sync.RWMutex
	items []Item
	// nextID keeps identifiers monotonic within a session, so a step's number
	// in the sidebar matches the one the model used when it set the step done.
	nextID int
}

// Default is the store this process shares between the agent and the UI.
var Default = &Store{}

// New returns an independent store, for a test or for a session that must not
// see the global one.
func New() *Store { return &Store{} }

// Replace swaps the whole plan.
//
// Replacing rather than merging is the operation the model actually wants: it
// is rewriting its plan as it understands it now, and a step it has dropped
// from the list is a step it no longer intends to take. A merge would leave
// dropped steps behind forever, because nothing else ever removes them.
func (s *Store) Replace(items []Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(items) > maxItems {
		return fmt.Errorf("the plan has %d steps, at most %d are kept", len(items), maxItems)
	}
	out := make([]Item, 0, len(items))
	for i, it := range items {
		content := strings.TrimSpace(it.Content)
		if content == "" {
			return fmt.Errorf("step %d is empty", i+1)
		}
		if r := []rune(content); len(r) > maxItemRunes {
			return fmt.Errorf("step %d is %d characters long, at most %d are kept", i+1, len(r), maxItemRunes)
		}
		out = append(out, Item{ID: i + 1, Content: content, Status: normalise(it.Status)})
	}
	s.items = out
	s.nextID = len(out)
	return nil
}

// SetStatus marks one step done, in progress, or pending again.
func (s *Store) SetStatus(id int, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status != Pending && status != InProgress && status != Done {
		return fmt.Errorf("unknown status %q: use pending, in_progress or done", status)
	}
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Status = status
			return nil
		}
	}
	return fmt.Errorf("there is no step %d in the plan", id)
}

// Clear empties the plan, which /new and a finished task both want.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
	s.nextID = 0
}

// Snapshot returns a copy of the plan for the UI to render.
func (s *Store) Snapshot() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Item(nil), s.items...)
}

// Progress is how far the plan has got, as counts rather than a percentage: a
// percentage of one step is a lie in either direction.
type Progress struct {
	Total      int
	Done       int
	InProgress int
}

// Progress reports the plan's state.
func (s *Store) Progress() Progress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var p Progress
	p.Total = len(s.items)
	for _, it := range s.items {
		switch it.Status {
		case Done:
			p.Done++
		case InProgress:
			p.InProgress++
		}
	}
	return p
}

// Render is the plan as plain text, for the transcript and for the model.
func Render(items []Item) string {
	if len(items) == 0 {
		return "(the plan is empty)"
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", it.ID, it.Status, it.Content))
	}
	return strings.Join(lines, "\n")
}

// normalise maps whatever the model wrote onto a known status, defaulting to
// pending. An unrecognised status is treated as "not started" rather than
// rejected: the plan is a report of intent, and refusing to record it over a
// word would lose the whole list.
func normalise(s Status) Status {
	switch Status(strings.ToLower(strings.TrimSpace(string(s)))) {
	case InProgress:
		return InProgress
	case Done:
		return Done
	}
	return Pending
}

// sortedCopy is for callers that want a stable order without the lock held.
func (s *Store) sortedCopy() []Item {
	items := s.Snapshot()
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// todoWriteArgs is the whole plan in one call.
type todoWriteArgs struct {
	Items []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"items"`
}

type todoWriteResult struct {
	Accepted int    `json:"accepted"`
	Plan     string `json:"plan"`
}

// todoWrite replaces the plan.
//
// One call carrying the whole list is deliberate. A per-step "add" tool lets a
// model accumulate a plan across a dozen calls, half of them superseded, and
// nothing ever removes a step it has changed its mind about. Writing the list
// out in full makes revision a rewrite, which is what revision is.
func (s *Store) todoWrite(ctx agent.Context, in todoWriteArgs) (todoWriteResult, error) {
	items := make([]Item, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, Item{Content: it.Content, Status: Status(it.Status)})
	}
	if err := s.Replace(items); err != nil {
		return todoWriteResult{}, err
	}
	out := s.Snapshot()
	return todoWriteResult{Accepted: len(out), Plan: Render(out)}, nil
}

type todoSetArgs struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

type todoSetResult struct {
	Plan string `json:"plan"`
}

// todoSetStatus moves one step along, which is the call a long task makes
// dozens of times. Writing the whole plan back for it would burn context on
// every step and let an unrelated slip rewrite steps the model did not mean to
// touch.
func (s *Store) todoSetStatus(ctx agent.Context, in todoSetArgs) (todoSetResult, error) {
	if err := s.SetStatus(in.ID, Status(in.Status)); err != nil {
		return todoSetResult{}, err
	}
	return todoSetResult{Plan: Render(s.sortedCopy())}, nil
}

type todoReadResult struct {
	Plan   string `json:"plan"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Active int    `json:"in_progress"`
}

func (s *Store) todoRead(ctx agent.Context, in struct{}) (todoReadResult, error) {
	p := s.Progress()
	return todoReadResult{
		Plan:   Render(s.sortedCopy()),
		Done:   p.Done,
		Total:  p.Total,
		Active: p.InProgress,
	}, nil
}

// MakeTools builds the two plan tools for this store.
func (s *Store) MakeTools() ([]tool.Tool, error) {
	write, err := functiontool.New(functiontool.Config{
		Name: "todo_write",
		Description: "Replaces the task plan with the given steps. Pass the whole list every time, in order, " +
			"each step as {content, status} where status is pending, in_progress or done. Use it to publish a " +
			"plan and to revise one; a step left out of the list is a step you no longer intend to take.",
	}, s.todoWrite)
	if err != nil {
		return nil, err
	}
	set, err := functiontool.New(functiontool.Config{
		Name:        "todo_set",
		Description: "Marks one step of the plan as pending, in_progress or done, by its id from todo_write.",
	}, s.todoSetStatus)
	if err != nil {
		return nil, err
	}
	read, err := functiontool.New(functiontool.Config{
		Name:        "todo_read",
		Description: "Returns the current plan and how many steps are done. Read it if you are unsure what the plan is.",
	}, s.todoRead)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{write, set, read}, nil
}

// Names are the tools this package contributes, for the caller that has to keep
// the read-only set in step with the full one.
var Names = []string{"todo_write", "todo_set", "todo_read"}
