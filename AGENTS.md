# AGENTS.md — Guidelines for AI Coding Agents in `dmcode`

`dmcode` is an autonomous, terminal-based AI coding assistant inspired by Charmbracelet's `crush`, built with **Go**, **google/adk-go** (`google.golang.org/adk/v2`), and **Bubble Tea** (`charm.land/bubbletea/v2`).

> ### ✅ Verified — `ask_user`, `todo_*`; ⚠️ still unverified — the rest
>
> The `todo_*` and `ask_user` tools **were broken end to end and are now fixed**.
> They compiled, their unit tests passed, and in use they did nothing at all: the
> transcript showed a tool call and a schema-validation message, and no result.
> The cause was their **generated argument schema**, not their logic — see §4,
> "A tool's argument schema is generated from its Go type". `ask_user` took its
> options as a nested, closed, undocumented object, so every shape a model
> naturally sent was rejected; `todo_write` marked a step's `status` required, so
> a plan written without it was rejected whole. Both are fixed, and the fix is
> covered by tests that drive the exact payloads a model was observed to send
> through a real `runner.Run` turn.
>
> Still unverified, and worth testing first:
> - `sub_agent`'s nested turn — a second agent with its own session, run from
>   inside a tool call. Unit-tested in isolation; not driven against a live
>   endpoint end to end, and not yet checked for its report fitting the parent's
>   context window without truncation problems.
> - that `RewindToLastUserMessage` really cuts the model's memory and not only
>   the transcript — nothing asserts this against a real runner;
> - that a session file read back after a restart is what a live turn would have
>   produced — the tests check the store in isolation, not the round trip through
>   `runner.Run`;
> - that the ask overlay's timer and channel behave against a **live** TUI turn —
>   the broker side is now driven end to end, but the overlay's own key handling
>   is tested with no ADK goroutine on the other end;
> - that `switch_mode`'s `EndInvocation` plus deferred `rebuildRunner` does not
>   strand the runner — the UI test covers the pending-mode bookkeeping only;
> - yolo mode has unit tests for the binding, the tool set and the badge, but no
>   TUI session has been driven through a real yolo turn.
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
  - `internal/tools`: the workspace instruments (`read_file`, `write_file`, `edit_file`, `list_dir`, `grep`, `glob`, `run_command`, `web_search`) **and the workspace boundary** they are confined to. `web_search` is the one instrument that reaches past the boundary on purpose (DuckDuckGo HTML, no key). `todo_*` is added from `internal/todo`, not here.
  - `internal/mcp`: external MCP servers — config from `~/.dmcode/mcp.json` and the workspace `.mcp.json` (the common `mcpServers` format, a stdio `command` or a `url`), one lazy `mcptoolset` per server, wired through `llmagent.Config.Toolsets` so a dead server costs nothing until a turn needs it. `List` is the one eager pass: names for the sidebar, notes for the servers that did not come up.
  - `internal/ui`: the Bubble Tea TUI — `ui.go` (event loop, layout, status bar), `markdown.go` (reply rendering), `mode.go` (plan/act, and the pending agent-requested switch), `history.go` (prompt history, `/cd`), `rewind.go` (ctrl+z, session switching), `sessions_view.go` (the `/sessions` overlay), `ask_view.go` (the question overlay and the sub-agent notes), `plan_view.go` (the plan block).
  - `internal/memsession`: the session store — the ADK `session.Service` dmcode runs on, plus the JSONL persistence under `~/.dmcode/sessions`.
  - `internal/todo`: the agent's plan — the store behind `todo_write`/`todo_set`/`todo_read` and the `/todo` view.
  - `internal/ask`: the broker between the `ask_user` tool and the question overlay. Its own package because `tools` cannot import `ui` and `ui` does not build tools; the `Broker` is the seam.
  - `internal/i18n`: user-facing strings; English is the source language, `catalog_ru.go` holds the Russian one.

### Mouse selection

`MouseModeAllMotion`, not `CellMotion`. Cell motion is enough for the wheel, but
a drag only arrives as `MouseMotionMsg`, and cell-motion reporting never sends
one — so selection and scrolling cannot both be had under `CellMotion`. The cost
is that the terminal's own drag-select is gone while the mouse is on, which is
what `/mouse` is for; that trade is stated in the comment on the `MouseMode`
line in `View`, because it is otherwise invisible.

Three decisions in here are the ones to keep:

- **The clipboard is fed from `m.frame`, not from `m.history`.** `frame` is the
  last frame handed to the renderer, cached in `View` *after* `paintSelection`.
  Copying the history instead would hand back unwrapped lines and the render's
  gutter markers — not what the user pointed at.
- **`selectionCells` normalises the two corners.** A backwards drag must copy in
  reading order; storing them as anchor/focus and sorting at read time is what
  makes that true.
- **`highlightCells` re-arms reverse video after every SGR**, rather than
  wrapping the selected slice in it once. A styled row carries its own resets and
  a wrapper applied at the front is cancelled by the first colour change, so the
  selection would visibly stop halfway along a line.

Columns are cells, not bytes and not runes: the loop decodes each rune and asks
`ansi.StringWidth` for its width, because a CJK character is two cells wide and
counting runes cuts the first one in half. `TestSelectionCountsWideRunesAsTwoCells`
is the one that would catch a regression there.

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

### Yolo mode

`shift+tab`, and nothing else. Yolo is act's reach with `ask_user` withdrawn
(`main.go` builds `yoloTools`; `activeTools` picks it), so "the agent will not
stop to ask" is a property of what it can reach rather than a line in the
instruction it is asked to believe. Three rules hold it in place, each with a
test in `internal/ui/mode_test.go`:

- **`Tab` does not reach it.** `agentMode.toggled` is the plan/act pair only.
  If `Tab` could, the mode with the least warning attached would be one keypress
  from the mode the user chose, on the key they already trust for something else.
- **No command reaches it.** `/mode yolo` names the key instead of reporting a
  bad spelling, and `setModeRequest` refuses a yolo request from the agent, as
  does `applyPendingMode` for anything already queued. A mode whose cost is paid
  while it runs has to be something the user pressed for.
- **Leaving it returns to the mode it came from** (`uiModel.beforeYolo`), so the
  key does not silently undo a deliberate plan-mode choice.

`agentMode` and `agent.Mode` are converted by written-out `agentMode()` /
`agentModeFrom` functions, never by a numeric cast. The two enums used to line up
by accident; a cast would keep that true silently and build the wrong agent the
day either side is reordered. A yolo session that finds no `yoloTools` says so
rather than showing a badge it cannot honour.

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

- **A tool's argument schema is generated from its Go type, and it is unforgiving.**
  `functiontool` derives the declaration with `jsonschema.For[T]`, which means:
  a field with no `jsonschema:"…"` tag reaches the model as a bare
  `{"type":"string"}` and tells it nothing; a field is **required** unless its
  `json` tag carries `omitempty`/`omitzero`; and any struct becomes an object with
  `additionalProperties:false`. A nested object is therefore a shape the model has
  to guess, and a wrong guess is a hard rejection the model cannot act on. This is
  not hypothetical: `ask_user` declared its options as `[]Option{Label, Note,
  Recommended}`, and models sent `["a","b"]` and then
  `[{"Label": "a", "value": "1"}]`. Both were refused — the first because items
  had to be objects, the second for the unknown property `value` — so the tool did
  nothing at all, and the transcript showed only a validation message.
  **Prefer flat arguments, `omitempty` on anything with a sensible default, and a
  `jsonschema` tag on every field.** See `internal/ask/ask_test.go`
  (`TestOptionsArePlainStrings`, `TestEveryArgumentIsDescribed`) and
  `internal/todo/todo_test.go` (`TestTodoWriteStepNeedsOnlyItsText`), which fail if
  the shape regresses. Note that the rejection came from the *endpoint* validating
  the schema dmcode put on the wire, not from the ADK's own argument conversion,
  which is lenient — so a lenient Go unmarshaller would not have helped.
- **Workspace boundary is lexical at resolve time:** `tools.resolve` must decide before the file is touched, so a path that does not exist yet cannot be dereferenced. Tools that actually open something close the gap themselves: `read_file`, `edit_file` and `write_file` re-check the path after following symlinks (`withinRootAfterLinks`), `write_file` checks the deepest existing ancestor of the target directory, and `grep` skips entries that are symlinks rather than reading through them. See `internal/tools/tools.go` and `internal/tools/symlink_test.go`.
- **Cancellation reaches the context, not the process:** `startTurn` runs the turn
  on a cancellable context and `Esc`/`Ctrl+C` cancel it, but a tool that ignores
  its context — `run_command` on a child process, a sub-agent mid-model-call —
  finishes on its own schedule first.
- **Session store and retries are unverified.** See the warning at the top: the
  rewind, the JSONL round trip, the ask overlay's timer and the mode switch have
  unit tests but no end-to-end check.

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
- **The progress note sat in the status bar** — "calling: …" and "running…" are
  turn state, and the bar is one row the badges need. The note now renders in
  the transcript flow itself, right under the last message: `syncVP` appends
  `progressTail` to the viewport content, so it scrolls with the conversation
  and leaves nothing behind when the turn ends. The bar keeps the badges while
  a turn runs.
- **The header and the sidebar said the same things** — model, session, folder,
  twice on screen whenever the panel was open. `headerVisible` now shows the top
  bar only when the sidebar is absent (narrow terminal or ctrl+b), and its rows
  go back to the transcript.
- **The sidebar lost its bottom sections** — one row per tool spilled past the
  panel width or ate the whole height, trimming the plan and the hotkeys. The
  tool list is now one wrapped entry (capped at 12 rows with a "…" marker), and a
  too-short panel drops the hotkeys — the most replaceable rows — before it trims
  anything else.
- **Markdown "working every other time"** — the per-turn text accumulator was never
  reset at the end of an LLM round, so a turn that used a tool had its second
  round's closing response appended on top of the deltas already shown. The
  duplicate landed without a line break, which is what turned `## Done` into
  `## Done## Done` and left a table without its delimiter row. `turnText` in
  `internal/ui/ui.go` now owns the accumulator per *round* and either trims the
  already-streamed prefix or replaces the round; `applyAgentText` applies the
  result. Tests live in `internal/ui/turn_text_test.go`.
- **A session you cannot take back** — `memsession` owns the store, which is what
  made ctrl+z and `/sessions` possible at all (see §1).
- **A change tally outliving its session** — `newSession` calls
  `dmtools.ResetChanges()`, so a fresh session does not report edits the previous
  one made.
- **A turn lost to the output limit** — a tool call the model could not finish
  (`finish_reason=length` mid-JSON) ended the turn with `unexpected end of JSON
  input`, which reads as a bug in dmcode. `internal/llm/truncate.go` types that
  failure apart from a merely malformed one (`truncatedCallError`) and re-asks the
  same endpoint once: the same conversation, a note saying the call was too big,
  and — only where the endpoint reported its usage — a budget of twice what the
  call had already used. Two rules keep it honest, and both are the ones the
  failover pool already follows: nothing is re-asked once anything has been
  yielded (the transcript cannot take back the first answer), and a request that
  carries a budget of its own is left alone, which keeps the 64-token tool probe
  in `verify.go` a single request. A second truncation is reported as the first
  one, never as whatever the failed re-ask said. The cost is a second generation
  on a local model, and it can still fail — a model that insists on one huge call
  gets the truncation error, which is the honest answer.
