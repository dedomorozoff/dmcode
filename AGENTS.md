# AGENTS.md — Guidelines for AI Coding Agents in `dmcode`

`dmcode` is an autonomous, terminal-based AI coding assistant inspired by Charmbracelet's `crush`, built with **Go**, **google/adk-go** (`google.golang.org/adk/v2`), and **Bubble Tea** (`charm.land/bubbletea/v2`).

> ### ⚠️ Unverified — the session store, retries and the new tools
>
> The features added most recently — `internal/memsession` (rewind and persistent
> sessions), the retry policy in `internal/llm`, and the `todo_*`, `ask_user`,
> `sub_agent` and `switch_mode` tools — **compile and pass unit tests but are not
> verified end to end**. There are no live checks against a real endpoint, and no
> TUI session has been driven through a rewind, a session switch, a question
> overlay, a plan or a delegation. If you are asked to fix something here, expect
> the bug to be in the integration, not in the logic the unit tests cover.
>
> Untested, and worth testing first:
> - that `RewindToLastUserMessage` really cuts the model's memory and not only
>   the transcript — nothing asserts this against a real runner;
> - that a session file read back after a restart is what a live turn would have
>   produced — the tests check the store in isolation, not the round trip through
>   `runner.Run`;
> - that the ask overlay's timer and channel behave against a **live** turn; the
>   tests drive `askKey` directly, with no ADK goroutine on the other end;
> - that `switch_mode`'s `EndInvocation` plus deferred `rebuildRunner` does not
>   strand the runner — the UI test covers the pending-mode bookkeeping only;
> - that a sub-agent's report survives the trip back into the parent's context
>   without blowing the context window.
>
> Also untested on Windows: the session file's permission mode, which `os.Chmod`
> cannot express there, so `TestSessionFileIsNotWorldReadable` skips itself.
>
> Known gaps, deliberately left and documented in the code: a rewind does not
> rewrite `~/.dmcode/history.jsonl`; cancellation reaches the context but not the
> process; the workspace boundary is lexical and a symlink can escape it.

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
  - `internal/agent`: the system instruction (act and plan variants), agent construction, and the two tools that change what the agent *is* — `subagent.go` (delegation) and `modeswitch.go` (the plan→act switch).
  - `internal/tools`: the workspace instruments (`read_file`, `write_file`, `edit_file`, `list_dir`, `grep`, `glob`, `run_command`) **and the workspace boundary** they are confined to. `todo_*` is added from `internal/todo`, not here.
  - `internal/ui`: the Bubble Tea TUI — `ui.go` (event loop, layout, status bar), `markdown.go` (reply rendering), `mode.go` (plan/act, and the pending agent-requested switch), `history.go` (prompt history, `/cd`), `rewind.go` (ctrl+z, session switching), `sessions_view.go` (the `/sessions` overlay), `ask_view.go` (the question overlay and the sub-agent notes), `plan_view.go` (the plan block).
  - `internal/memsession`: the session store — the ADK `session.Service` dmcode runs on, plus the JSONL persistence under `~/.dmcode/sessions`.
  - `internal/todo`: the agent's plan — the store behind `todo_write`/`todo_set`/`todo_read` and the `/todo` view.
  - `internal/ask`: the broker between the `ask_user` tool and the question overlay. Its own package because `tools` cannot import `ui` and `ui` does not build tools; the `Broker` is the seam.
  - `internal/i18n`: user-facing strings; English is the source language, `catalog_ru.go` holds the Russian one.

### Mode switches

`uiModel.pendingMode` is a mode the **agent** asked for, and it is applied only
in `turnDoneMsg`, never where it is asked for: the runner is built around one
instrument set for the whole turn, so swapping it mid-turn would let the model
keep calling tools that have just been withdrawn. `switch_mode` therefore calls
`ctx.EndInvocation()` and records the request; the UI rebuilds between turns,
keeping the session id and the store, so the planning context survives.

The tool exists **only in the plan-mode set**, which `ui.newRunner` builds rather
than `main.go` — it needs the model's hook and the current mode. In act mode the
model cannot ask for a mode it is already in, and cannot ask to go back to plan
mode at all; that is `Tab`, and it belongs to the user. This is what prevents
plan↔act oscillation.

### Questions and delegation

- `ask.Ask` blocks the tool goroutine on a channel the UI answers. The reply
  channel is buffered: the tool may already have been released (cancelled turn,
  or the broker's timer), and a send that waited for a reader that had gone would
  hang the event loop instead of the turn. The same rule is why the `ask_user`
  tool returns every outcome — including "nobody answered" — as *text*, never as
  a tool error the model would sensibly retry.
- The overlay's timer is off by default (`DMCODE_ASK_TIMEOUT=0`). With it off,
  no `tea.Tick` is scheduled at all: waking the event loop once a second for a
  question with no deadline is work for nothing.
- A sub-agent gets `memsession.NewMemory()` — never the persistent store. Its
  reading must not reach the parent's memory, and its throwaway session must not
  appear in `/sessions`. It also gets no `sub_agent` (recursion) and no
  `ask_user` (a prompt whose context the user cannot see).

### Session store

`memsession.Service` replaces `session.InMemoryService` for one reason: that
service hands out a *copy* of the event slice, so nothing upstream can take an
event back, and neither ctrl+z nor a session switch is possible against it. Every
ADK session interface (`Service`, `Session`, `Events`, `State`) is exported, so
the store is implemented outside the ADK package.

- `RewindToLastUserMessage` cuts at the last `Author == "user"` event, removing
  that message too, and replays state from what survives.
- Writes are append-only JSONL, one file per session, with a metadata header on
  the first line so `/sessions` never has to read a conversation to list it.
  A rewind rewrites the file (temp + rename); an error is recorded, not returned,
  so a full disk cannot fail a turn.
- `Service` is mutex-guarded and every method that may do I/O takes the write
  lock — including `load`, which can lazily read from disk. `recordWriteErr` is
  the one helper that must **not** take the lock itself.

### Retries

`failoverModel.GenerateContent` retries a single endpoint before moving to the
next. `retryable()` answers "is another host worth trying" and so allows a 401;
`sameEndpointRetryable()` answers "is asking this host again worth it" and does
not. Keeping them separate is the point — a rejected key is another endpoint's
problem, not a reason to repeat the same request twice.

### Workspace boundary

`tools.SetRoot` is called once at startup (and again on `/cd`) and every tool
path goes through `tools.resolve`. A path resolving outside the root is refused
with an error naming both directories. Two consequences to keep in mind:

- Relative paths are resolved against the **root**, not the process directory.
  They coincide in a normal session, but resolving against the root is what stops
  `/cd` and the boundary from disagreeing.
- The check is lexical. A symlink *inside* the tree is the known soft spot:
  `write_file` has to be allowed to create files that do not exist yet, so they
  cannot be resolved first.

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

- **Render History Performance:** `renderHistory()` still re-wraps the whole
  history whenever `historyDirty` is set, and streaming sets it on every token.
  The whole-transcript cache in `ui.go` means a long session can lag; an
  incremental per-line cache is the obvious next step.
- **Workspace boundary is lexical:** a symlink inside the workspace can still
  reach outside it. See §1.
- **`tab` is overloaded:** it completes a suggestion inside a `/` command and
  switches mode everywhere else. A user who expected a tab character in a prompt
  gets a mode switch instead.
- **Cancellation reaches the context, not the process:** `startTurn` runs the turn
  on a cancellable context and `Esc`/`Ctrl+C` cancel it, but a tool that ignores
  its context — `run_command` on a child process, a sub-agent mid-model-call —
  finishes on its own schedule first.

Already fixed, and worth not regressing:

- Monolithic `package main` — split into `internal/*` (see §1).
- No path confinement — `tools.SetRoot`/`tools.resolve`.
- `edit_file` rigidity — a whitespace-tolerant match was added.
- **Cancellation** — turns run on a cancellable context, and `Esc`/`Ctrl+C`
  release the turn while keeping the conversation.
- **Model switching wiping the session** — `switchModelCmd` keeps `m.sessionID`
  and the session service, so the conversation survives a `/model` change.
- **A session you cannot take back** — `memsession` owns the store, which is what
  made ctrl+z and `/sessions` possible at all (see §1).

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

- Monolithic `package main` — split into `internal/*` (see §1).
- No path confinement — `tools.SetRoot`/`tools.resolve`.
- `edit_file` rigidity — a whitespace-tolerant match was added.
- **Markdown "working every other time"** — the per-turn text accumulator was never
  reset at the end of an LLM round, so a turn that used a tool had its second
  round's closing response appended on top of the deltas already shown. The
  duplicate landed without a line break, which is what turned `## Done` into
  `## Done## Done` and left a table without its delimiter row. `turnText` in
  `internal/ui/ui.go` now owns the accumulator per *round* and either trims the
  already-streamed prefix or replaces the round; `applyAgentText` applies the
  result. Tests live in `internal/ui/turn_text_test.go`.
