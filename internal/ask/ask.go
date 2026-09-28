// Package ask lets the agent put a choice to the user and wait for it.
//
// It is a package of its own because the tool and the TUI have to meet in the
// middle and neither may import the other: tools cannot import ui, and ui does
// not build tools. The Broker is the seam — the tool calls Ask and blocks, the
// UI registers a prompt function and answers through a channel.
package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Option is one choice on offer.
type Option struct {
	Label string `json:"label"`
	// Note is a short clarification shown beside the label.
	Note string `json:"note,omitempty"`
	// Recommended marks the option the timer will pick. At most one is meant to
	// be recommended; several are tolerated and the first wins, because a plan
	// that answers for the user must land somewhere definite.
	Recommended bool `json:"recommended,omitempty"`
}

// Request is a question the agent needs answered before it can go on.
type Request struct {
	Question string   `json:"question"`
	Options  []Option `json:"options"`
	// Multi lets the user tick several options instead of choosing one.
	Multi bool `json:"multi,omitempty"`
	// AllowCustom adds a free-text row, for the option the model did not think
	// of. It is on when the agent expects an answer no list can carry.
	AllowCustom bool `json:"allow_custom,omitempty"`
	// TimeoutSeconds asks for a specific wait. Zero means the broker's own
	// setting, which is off unless the user turned it on.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// Answer is what the user chose.
type Answer struct {
	Selected []string
	Custom   string
	// Auto marks an answer the timer produced. The model is told, so a decision
	// it did not get to make is not silently presented as the user's.
	Auto bool
	// Skipped is the user pressing escape: no answer, and the agent is expected
	// to carry on with what it can do without one.
	Skipped bool
}

// String renders the answer for the model, in a form it can act on.
func (a Answer) String() string {
	switch {
	case a.Skipped:
		return "skipped: the user gave no answer — proceed with what you can do without it, and say what you assumed"
	case a.Auto:
		return fmt.Sprintf("no answer in time, so this was chosen by default: %s (the user did not confirm it)",
			strings.Join(a.pick(), ", "))
	case a.Custom != "":
		return "the user wrote: " + a.Custom
	}
	if len(a.Selected) == 0 {
		return "no options were selected — ask again with clearer options"
	}
	return "chosen: " + strings.Join(a.Selected, ", ")
}

// pick is what the answer amounts to, whichever way it was arrived at.
func (a Answer) pick() []string {
	if a.Custom != "" {
		return []string{a.Custom}
	}
	return a.Selected
}

// PromptFunc hands a request to the UI and returns the channel the answer will
// arrive on. It is registered by RunTUI, after the program exists.
type PromptFunc func(Request) <-chan Answer

// Broker connects the ask_user tool to the interface.
type Broker struct {
	mu      sync.RWMutex
	prompt  PromptFunc
	timeout time.Duration
}

// NewBroker returns a broker with the given default wait. A zero timeout means
// the question waits for the user indefinitely, which is the default: an agent
// that answers for a user who is still reading is worse than one that waits.
func NewBroker(timeout time.Duration) *Broker {
	return &Broker{timeout: timeout}
}

// SetPromptFunc registers the UI side. Passing nil detaches it, which is what a
// headless test does.
func (b *Broker) SetPromptFunc(f PromptFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prompt = f
}

// Timeout reports the default wait.
func (b *Broker) Timeout() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.timeout
}

// ErrNoPrompt reports that nothing is there to answer a question. It is a
// normal outcome in a test or a headless run, not a fault: the tool turns it
// into "skipped" rather than blocking a turn on an answer that can never come.
var ErrNoPrompt = errors.New("ask: no interface is attached to answer questions")

// Ask puts req to the user and waits for the answer.
//
// Three things can end the wait: the user answers, the context is cancelled
// (Esc), or the timer fires. The last of those is the reason the timer exists,
// and it is off by default, so an unanswered question waits as long as the user
// is willing to let it.
func (b *Broker) Ask(ctx context.Context, req Request) (Answer, error) {
	b.mu.RLock()
	prompt, def := b.prompt, b.timeout
	b.mu.RUnlock()

	if prompt == nil {
		return Answer{Skipped: true}, ErrNoPrompt
	}
	if len(req.Options) == 0 && !req.AllowCustom {
		// Nothing to choose and nowhere to type: the model has not built a real
		// question, and waiting for an answer to one is a turn that never ends.
		return Answer{Skipped: true}, errors.New("ask: the question has no options and no way to type one")
	}

	wait := def
	if req.TimeoutSeconds > 0 {
		wait = time.Duration(req.TimeoutSeconds) * time.Second
	}
	reply := prompt(req)
	if reply == nil {
		return Answer{Skipped: true}, ErrNoPrompt
	}

	var expiry <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		expiry = timer.C
	}

	select {
	case ans, ok := <-reply:
		if !ok {
			// The UI closed the channel without answering, which is a shutdown
			// rather than a reply.
			return Answer{Skipped: true}, nil
		}
		return ans, nil
	case <-ctx.Done():
		return Answer{Skipped: true}, ctx.Err()
	case <-expiry:
		// The user did not answer, so the recommended option goes. Auto is set
		// so the model reports a default rather than claiming a choice.
		ans := Answer{Auto: true}
		for _, o := range req.Options {
			if o.Recommended {
				ans.Selected = []string{o.Label}
				return ans, nil
			}
		}
		// No recommendation: the first option is the one the model listed first,
		// which is the closest thing to a default it stated.
		if len(req.Options) > 0 {
			ans.Selected = []string{req.Options[0].Label}
		}
		return ans, nil
	}
}

// askUser is the tool body. It is a method so the broker it asks is the one the
// session was built with, rather than a package-level one a test could swap out
// from under a running turn.
//
// Every outcome is returned as text, never as a tool error. A cancelled turn, a
// missing interface and a skipped question are all "nobody answered", and the
// model needs that as an answer it can act on — not as a failure it would
// sensibly retry, and not as a failed tool call in the transcript.
func (b *Broker) askUser(ctx agent.Context, in Request) (string, error) {
	ans, _ := b.Ask(ctx, in)
	return ans.String(), nil
}

// MakeTool builds the ask_user tool.
func (b *Broker) MakeTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "ask_user",
		Description: "Asks the user to choose, and waits for the answer. Use it when a decision is needed before " +
			"you can go on: give the question, two to five concrete options, and mark the one you recommend with " +
			"recommended=true. Set multi=true when several answers are valid, allow_custom=true when the user may " +
			"want something not on the list. Do not guess instead of asking, and do not ask about anything you " +
			"can find out by reading the code. If the user skips the question, carry on with your best guess and " +
			"say what you assumed.",
	}, b.askUser)
}

// Name is the tool this package contributes, for the caller keeping the
// read-only set in step with the full one.
const Name = "ask_user"
