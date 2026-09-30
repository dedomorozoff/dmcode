package agent

import (
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Plan mode is where the model reads and proposes; act mode is where it edits.
// This is the only way out of plan mode for the agent itself — the other is the
// user pressing Tab, and the other Tab is for the act→plan direction the agent
// is not allowed to take. A model that could move itself back into plan mode
// mid-task would oscillate, planning and unplanning the same change forever.
const (
	// SwitchModeName is the tool name. It is exported because the UI builds the
	// plan-mode set with this tool and has to be able to say so in a test.
	SwitchModeName = "switch_mode"
	modeActName    = "act"
	modePlanName   = "plan"
)

// ModeRequest is the UI's answer to a switch the agent asked for: applied after
// the turn ends, or refused with a reason the model can read.
type ModeRequest func(Mode, string)

// switchModeResult is what the tool returns. Reason is empty when the switch was
// accepted, and says why not when it was refused — a bare "no" would leave the
// model guessing whether to ask again.
type switchModeResult struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
	// Applied is set when the switch is now the session's mode, so the model
	// knows to carry on rather than to end its turn and wait.
	Applied bool `json:"applied"`
}

// SwitchModeTool builds the tool that lets the agent leave plan mode.
//
// request is the UI's hook. The switch cannot be applied while the tool runs:
// the runner is built around one instrument set for the whole turn, and
// swapping it mid-turn would leave the model calling tools that have just been
// withdrawn. So the request is recorded, the tool ends the turn, and the UI
// applies it when the turn is done.
func SwitchModeTool(current Mode, request ModeRequest) (tool.Tool, error) {
	h := &modeSwitcher{current: current, request: request}
	return functiontool.New(functiontool.Config{
		Name: SwitchModeName,
		Description: "Asks to leave plan mode and start editing, for the rest of this session. Call it when the " +
			"plan is ready, you have presented it, and you are confident it is the right one — not before, and " +
			"not while the request is still ambiguous. If the request is ambiguous, ask_user instead. The switch " +
			"happens at the end of this turn: end your turn with a one-line summary of the plan you are " +
			"applying, and the work will continue under act mode. You cannot go back to plan mode yourself; only " +
			"the user can, with Tab.",
	}, h.run)
}

type modeSwitcher struct {
	current Mode
	request ModeRequest
	// switched guards against a second call in the same turn. The second one
	// would queue a second rebuild of the agent for a switch already pending.
	switched bool
}

// switchArgs is the tool's input.
type switchArgs struct {
	Mode string `json:"mode"`
}

func (h *modeSwitcher) run(ctx agent.Context, args switchArgs) (switchModeResult, error) {
	// EndInvocation is the ADK's own way of finishing a turn early. It is taken
	// as a value here so the decision can be tested without a whole
	// agent.Context: the interface is large, and a fake of it would be a page of
	// methods that say nothing about this tool.
	return h.decide(ctx.EndInvocation, args)
}

// decide is the tool's whole behaviour, with the invocation's end as a function.
func (h *modeSwitcher) decide(end func(), args switchArgs) (switchModeResult, error) {
	want := strings.ToLower(strings.TrimSpace(args.Mode))
	switch want {
	case modeActName:
	case modePlanName:
		// Refused with the reason, not with an error: the model asked a question
		// and deserves an answer it can act on.
		return switchModeResult{Reason: "only the user can put the session back into plan mode (Tab)"}, nil
	case "":
		return switchModeResult{Reason: `say which mode you want: {"mode": "act"}`}, nil
	default:
		return switchModeResult{Reason: `unknown mode "` + want + `": use "act"`}, nil
	}
	if h.current != ModePlan {
		return switchModeResult{
			Accepted: true,
			Applied:  true,
			Reason:   "this session is already in act mode",
		}, nil
	}
	if h.switched {
		// Already asked in this turn. Answering as if it were new would make the
		// model believe the first request failed.
		return switchModeResult{Accepted: true, Reason: "the switch is already queued for the end of this turn"}, nil
	}
	if h.request == nil {
		return switchModeResult{Reason: "this session cannot switch modes on its own — ask the user to press Tab"}, nil
	}
	h.switched = true
	// Ending the invocation is what makes the switch safe: the runner is built
	// around one instrument set for the whole turn, so the rest of the turn must
	// not run before the UI has rebuilt it.
	end()
	h.request(ModeAct, "")
	return switchModeResult{Accepted: true, Reason: "switching to act mode at the end of this turn"}, nil
}

// PLANSWITCH
