# AGENTS.md — Guidelines for AI Coding Agents in `dmcode`

`dmcode` is an autonomous, terminal-based AI coding assistant inspired by Charmbracelet's `crush`, built with **Go**, **google/adk-go** (`google.golang.org/adk/v2`), and **Bubble Tea** (`charm.land/bubbletea/v2`).

---

## 1. Architecture & Stack Overview

- **Language & Runtime:** Go 1.25+ (configured for toolchain 1.26+ in `go.mod`).
- **Core Agent Framework:** `google.golang.org/adk/v2`
  - `llmagent` (`agent/llmagent`): handles tool execution, prompt formatting, reasoning loop.
  - `openaimodel` (`model/openaimodel`): OpenAI-compatible endpoint driver (Groq, OpenRouter, Mistral, GitHub Models, Ollama, etc.).
  - `runner` (`runner.Runner`): executes turns, streams events (`StreamingModeSSE`), manages session states.
  - `session` (`session.Service`): tracks conversation history and context window.
- **TUI Framework:** Charmbracelet's `bubbletea/v2`, `bubbles/v2`, `lipgloss/v2`.
- **Key Modules:**
  - `main.go`: Startup, `.env` discovery, provider wizard, LLM agent wiring.
  - `tools.go`: Agent tool definitions (`read_file`, `write_file`, `edit_file`, `list_dir`, `grep`, `glob`, `run_command`).
  - `ui.go`: Bubble Tea TUI, event streaming, viewport rendering, palette, model selector.

---

## 2. Development Workflow & Commands

Whenever you make changes to `dmcode`, execute the following checks:

```bash
# 1. Check formatting and tidy dependencies
go fmt ./...
go mod tidy

# 2. Run existing unit tests
go test -v ./...

# 3. Build executable to verify compilation
go build -o dmcode.exe .
```

Always verify compilation and run `go test ./...` after any code modification.

---

## 3. Core Coding Conventions & Rules

1. **Idiomatic Go:**
   - Return clean errors with `%w` wrapping where appropriate.
   - Do not ignore returned errors without justification.
   - Avoid global mutable states where possible; pass dependencies cleanly.

2. **Cross-Platform Compatibility (Windows, macOS, Linux):**
   - Handle path separators using `filepath.Clean`, `filepath.ToSlash`, and `filepath.FromSlash`.
   - Never hardcode `/bin/sh` without fallback to Windows shells (`powershell.exe` or `cmd.exe`).
   - Normalise newlines (`\r\n` vs `\n`) when processing text files and doing string replacements.

3. **Concurrency & Thread Safety in Bubble Tea:**
   - `prog.Send(...)` sends messages into the Bubble Tea event queue. Ensure long-running operations run as `tea.Cmd` goroutines.
   - Respect context cancellation: any running command or LLM stream must be cancellable via a `context.Context` when the user interrupts (e.g., `Esc` or `Ctrl+C`).

4. **Safety & File Operations:**
   - Always validate that file paths remain within permissible workspace boundaries (unless user explicitly confirms).
   - Atomic writes: use temporary files + rename when writing to avoid corrupting files on unexpected exit.
   - Create parent directories automatically (`os.MkdirAll`) before creating files.

5. **Tool Design Principles:**
   - Tool arguments must have clear JSON tags and descriptive documentation in `functiontool.Config`.
   - Return structured results or truncated strings to prevent blowing up the LLM's context window.

---

## 4. Current Architecture Deficiencies to Keep in Mind

If you are asked to fix or improve `dmcode`, be aware of these known architectural pitfalls:

- **Monolithic main package:** All files currently reside in `package main`. Move towards modular packages:
  - `internal/agent` — agent construction and prompt definitions
  - `internal/tools` — tool implementations and permission checks
  - `internal/ui` — Bubbletea components and rendering
  - `internal/config` — provider configuration, env, and settings
- **Cancellation:** Turns currently run on `context.Background()` with no way to interrupt an active tool call or LLM streaming loop.
- **Model Switching Context Bug:** Switching models currently allocates a `newSessionID()`, wiping conversation history despite UI stating otherwise.
- **String Replacement Rigidity:** `edit_file` only supports strict literal matches, failing when whitespace or line endings differ slightly.
- **Render History Performance:** `renderHistory()` re-renders and re-wraps the entire history on every frame/token. Large sessions cause UI lag.
