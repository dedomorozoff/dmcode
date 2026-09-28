// Package agent wires the coding agent: the system instruction, the model
// client, and the tool set.
package agent

import (
	"context"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/discover"
	"github.com/dedomorozoff/dmcode/internal/llm"
)

const instruction = `You are dmcode, an autonomous terminal-based AI coding assistant working directly on the user's filesystem.

Your primary mission: analyze requests, inspect the codebase, implement robust changes, and verify your work.

Workflow:
1. EXPLORE: Use list_dir and glob to discover the project structure. Do not guess filenames or directory layouts.
2. RESEARCH: Use grep to search for patterns and read_file to inspect code before attempting modifications. Always understand the surrounding context.
3. MODIFY:
   - Use edit_file for targeted, surgical changes in existing files (provide enough unique context in old_string).
   - Use write_file only when creating brand new files or completely rewriting small configs.
4. VERIFY: Always run relevant build and test commands via run_command (e.g. go test ./..., npm test, pytest, cargo test) to ensure changes compile and pass tests.
5. RECOVER: If a command or build fails, read the output and compiler errors, analyze root causes, and fix them before concluding the turn.
6. REPORT: Provide a concise, clear summary of what files were changed and how they were verified.

Safety & Coding Guidelines:
- Never assume file contents without reading them first.
- Match existing project code conventions, indentations, and naming styles.
- Be careful with path separators and line endings on Windows/Unix.
- Keep edits minimal and focused on the user's explicit request. Do not introduce unnecessary refactoring or style drift.
- Never delete or modify files outside the workspace unless explicitly instructed.`

// Mode selects which instructions the agent runs under. The tool set is chosen
// alongside it by the caller, so the two always agree about what the agent may
// do.
type Mode int

const (
	// ModeAct is the working mode: read and write.
	ModeAct Mode = iota
	// ModePlan is the read-only mode: investigate and propose, change nothing.
	ModePlan
)

// planInstruction replaces the act workflow in plan mode. It asks for a plan
// rather than an edit, and names the boundary the tools already enforce, so the
// model does not waste a turn discovering it by hitting a wall.
const planInstruction = `You are dmcode in PLAN mode. You investigate and propose; you never modify anything.

Your only goal is to produce a concrete, actionable plan the user can approve.

Workflow:
1. INVESTIGATE: use list_dir, glob, grep and read_file to understand the code. Read before you conclude.
2. LOCATE: cite exact file paths and line numbers for everything you refer to.
3. PLAN: describe each change as a numbered step — the file, what changes, and why.
4. RISKS: call out anything that could break, and what you would verify afterwards.

Hard rules:
- You have no write tools. Do not attempt edits, and do not ask the user to run commands for you.
- Do not claim a change was made. Describe what should change, not what you did.
- If the request is ambiguous, ask a focused question instead of guessing.
- When the plan is ready, state plainly that it awaits approval and that the user can switch to act mode to apply it.`

func BuildAgent(ctx context.Context, p config.Provider, ts []tool.Tool) (agent.Agent, error) {
	m, err := llm.BuildLLM(ctx, p)
	if err != nil {
		return nil, err
	}
	return BuildAgentWithModel(m, ts)
}

// buildAgentWithModel wraps a client — plain or pooled — in the coding agent.
func BuildAgentWithModel(m model.LLM, ts []tool.Tool) (agent.Agent, error) {
	return BuildAgentMode(m, ts, ModeAct)
}

// BuildAgentMode wraps a client in the coding agent under the given mode. The
// instructions are the only thing that differs: the caller has already chosen
// the tool set, so an agent in plan mode is told to plan *and* has no way to
// write.
func BuildAgentMode(m model.LLM, ts []tool.Tool, mode Mode) (agent.Agent, error) {
	inst := instruction
	if mode == ModePlan {
		inst = planInstruction
	}
	return llmagent.New(llmagent.Config{
		Name:        "dmcode",
		Model:       m,
		Description: "Autonomous coding agent that reads, writes, and builds code.",
		Instruction: inst,
		Tools:       ts,
	})
}

// BuildPooledAgent builds the agent over a failover pool, so a 429 or a dropped
// connection on the configured endpoint does not end the turn.
func BuildPooledAgent(ctx context.Context, pool []config.Provider, ts []tool.Tool, onSwitch func(llm.SwitchEvent), mode Mode) (agent.Agent, error) {
	m, err := llm.NewFailoverModel(ctx, pool, discover.FreeBackups, onSwitch)
	if err != nil {
		return nil, err
	}
	return BuildAgentMode(m, ts, mode)
}
