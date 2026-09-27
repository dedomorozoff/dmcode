// Package agent wires the coding agent: the system instruction, the model
// client, and the tool set.
package agent

import (
	"context"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"dmcode/internal/config"
	"dmcode/internal/discover"
	"dmcode/internal/llm"
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

func BuildAgent(ctx context.Context, p config.Provider, tools []tool.Tool) (agent.Agent, error) {
	m, err := llm.BuildLLM(ctx, p)
	if err != nil {
		return nil, err
	}
	return BuildAgentWithModel(m, tools)
}

// buildAgentWithModel wraps a client — plain or pooled — in the coding agent.
func BuildAgentWithModel(m model.LLM, tools []tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "dmcode",
		Model:       m,
		Description: "Autonomous coding agent that reads, writes, and builds code.",
		Instruction: instruction,
		Tools:       tools,
	})
}

// buildPooledAgent builds the agent over a failover pool, so a 429 or a dropped
// connection on the configured endpoint does not end the turn.
func BuildPooledAgent(ctx context.Context, pool []config.Provider, tools []tool.Tool, onSwitch func(llm.SwitchEvent)) (agent.Agent, error) {
	m, err := llm.NewFailoverModel(ctx, pool, discover.FreeBackups, onSwitch)
	if err != nil {
		return nil, err
	}
	return BuildAgentWithModel(m, tools)
}
