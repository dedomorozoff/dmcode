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
- **Key Modules:** the code is already split into `internal/` packages:
  - `main.go`: startup only — flag parsing, `-C` relocation, then chaining the packages below.
  - `internal/config`: provider config and the `.env` file.
  - `internal/discover`: which providers the session can run on.
  - `internal/llm`: the OpenAI-compatible wire and the failover pool.
  - `internal/agent`: the system instruction (act and plan variants) and agent construction.
  - `internal/tools`: the instruments (`read_file`, `write_file`, `edit_file`, `list_dir`, `grep`, `glob`, `run_command`) **and the workspace boundary** they are confined to.
  - `internal/ui`: the Bubble Tea TUI — `ui.go` (event loop, layout, status bar), `markdown.go` (reply rendering), `mode.go` (plan/act), `history.go` (prompt history, `/cd`).
  - `internal/i18n`: user-facing strings; English is the source language, `catalog_ru.go` holds the Russian one.

### Workspace boundary

`tools.SetRoot` is called once at startup (and again on `/cd`) and every tool
path goes through `tools.resolve`. A path resolving outside the root is refused
with an error naming both directories. Two consequences to keep in mind:

- Relative paths are resolved against the **root**, not the process directory.
  They coincide in a normal session, but resolving against the root is what stops
  `/cd` and the boundary from disagreeing.
- The check is lexical. A symlink *inside* the tree used to be the soft spot:
  `write_file` has to be allowed to create files that do not exist yet, so they
  cannot be resolved first. The tools that open something now re-check the path
  after following symlinks (see §4).

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

- **Workspace boundary is lexical at resolve time:** `tools.resolve` must decide before the file is touched, so a path that does not exist yet cannot be dereferenced. Tools that actually open something close the gap themselves: `read_file`, `edit_file` and `write_file` re-check the path after following symlinks (`withinRootAfterLinks`), `write_file` checks the deepest existing ancestor of the target directory, and `grep` skips entries that are symlinks rather than reading through them. See `internal/tools/tools.go` and `internal/tools/symlink_test.go`.

### Diagnosing "the TUI shows X but my test says otherwise"

A stale process is the first thing to rule out, not the last. Overwriting
`dist/dmcode.exe` fails with "being used by another process" while a *previous*
build keeps running, and a session started from that old binary shows output no
amount of rebuilding will change. When output disagrees with a passing test,
confirm which binary the session is running before reading any more code.

`/debug` exists for the same reason: it prints the line kind, the markdown flag
and the width of the last transcript lines. A reply that renders as raw markup
is a routing problem (the text is stored under a kind whose `rowStyle` has
`markdown: false`) rather than a parsing problem, and the counter tells the two
apart in one line.

Already fixed, and worth not regressing:

- **Cancellation:** `startTurn` runs the turn on a `context.WithCancel` it stores in `m.cancelTurn`; `Esc` and `Ctrl+C` cancel the running LLM/tool call instead of killing the process.
- **Model Switching Context Bug:** `switchModelCmd` reuses the session service and `modelSwitchedMsg` handling preserves `m.sessionID`, so a model switch keeps the conversation. A regression test covers it (`internal/ui/model_switch_test.go`).
- **Render History Performance:** `renderHistory()` keeps a per-line cache (`m.cachedRows`, `cachedLine` in `internal/ui/ui.go`) that is index-aligned with the history. A streamed token re-renders only the line whose text changed; the cache is compared against a full rebuild in `internal/ui/render_cache_test.go`.
- Monolithic `package main` — split into `internal/*` (see §1).
- No path confinement — `tools.SetRoot`/`tools.resolve`.
- `edit_file` rigidity — a whitespace-tolerant match was added.
- **Overloaded `tab`** — it completed a suggestion inside a `/` command and
  switched mode everywhere else, so the same key did two unrelated things
  depending on what was in the input. The `/` commands are now a dialog picked
  with `↑` `↓` + `enter` (`esc` dismisses it and keeps the typed text), which
  leaves `tab` meaning one thing everywhere: the mode.
- **The command list was a block of frame rows** — it shrank the transcript on
  every `/`, jumped back when it closed, and on a short terminal ran into the
  bottom of the screen with the input still to fit. It is now a bordered dialog
  painted over the chat panel, anchored above the input, in the same chrome as
  `ctrl+p`; the frame is byte-identical with it open and closed, and only the
  chat panel's columns are rewritten so the sidebar survives.
- **The status bar carried a hotkey list and the model name** — the model was the
  third place on screen showing it, the header and the sidebar already have it,
  and the fixed `ctrl+p │ ctrl+y │ esc` run was the part that dropped off the edge
  on a narrow terminal, leaving it half-printed. The bar is now the mode badge and
  the state badge, and `TestStatusBarShowsTheModeAndNothingElse` pins it there.
- **Markdown "working every other time"** — the per-turn text accumulator was never
  reset at the end of an LLM round, so a turn that used a tool had its second
  round's closing response appended on top of the deltas already shown. The
  duplicate landed without a line break, which is what turned `## Done` into
  `## Done## Done` and left a table without its delimiter row. `turnText` in
  `internal/ui/ui.go` now owns the accumulator per *round* and either trims the
  already-streamed prefix or replaces the round; `applyAgentText` applies the
  result. Tests live in `internal/ui/turn_text_test.go`.
