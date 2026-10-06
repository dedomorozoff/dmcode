# AGENTS.md — Guidelines for AI Coding Agents in `dmcode`

`dmcode` is an autonomous, terminal-based AI coding assistant inspired by Charmbracelet's `crush`, built with **Go**, **google/adk-go** (`google.golang.org/adk/v2`), and **Bubble Tea** (`charm.land/bubbletea/v2`).

> ### ✅ Verified — `ask_user`, `todo_*`, and attaching a picture; ⚠️ still unverified — the rest
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
> **Attaching a picture is verified on Windows against a live session**: a
> clipboard screenshot is read, drawn and attached, and a dropped path attaches as
> the path completes. Four bugs on that path were invisible until something was
> measured, and every one is worth reading before changing this feature — see §5,
> "Four bugs that only a measurement found". The one thing still unproven is the
> step after the attachment: no picture has been sent to a real vision endpoint, so
> nothing has confirmed that a live endpoint accepts the `image_url` data URL dmcode
> puts on the wire, or that the model then answers about the picture.
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
>   TUI session has been driven through a real yolo turn;
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
  - `internal/discover`: which providers the session can run on, and the llama-server around a local `.gguf`.
  - `internal/llm`: the OpenAI-compatible wire and the failover pool.
  - `internal/agent`: the system instruction (act and plan variants), agent construction, and the two tools that change what the agent *is* — `subagent.go` (delegation) and `modeswitch.go` (the plan→act switch).
  - `internal/tools`: the workspace instruments (`read_file`, `write_file`, `edit_file`, `list_dir`, `grep`, `glob`, `run_command`, `web_search`, `project_map`) **and the workspace boundary** they are confined to. `web_search` is the one instrument that reaches past the boundary on purpose (DuckDuckGo HTML, no key). `todo_*` is added from `internal/todo`, not here.
  - `internal/mcp`: external MCP servers — config from `~/.dmcode/mcp.json` and the workspace `.mcp.json` (the common `mcpServers` format, a stdio `command` or a `url`), one lazy `mcptoolset` per server, wired through `llmagent.Config.Toolsets` so a dead server costs nothing until a turn needs it. `List` is the one eager pass: names for the sidebar, notes for the servers that did not come up.
  - `internal/ui`: the Bubble Tea TUI — `ui.go` (event loop, layout, status bar),
    `markdown.go` (reply rendering), `mode.go` (plan/act, and the pending
    agent-requested switch), `history.go` (prompt history, `/cd`), `rewind.go`
    (ctrl+z, session switching), `jump.go` (clicking a change opens the file at
    that line), `sessions_view.go` (the `/sessions` overlay), `ask_view.go` (the
    question overlay and the sub-agent notes), `plan_view.go` (the plan block).
  - `internal/memsession`: the session store — the ADK `session.Service` dmcode runs on, plus the JSONL persistence under `~/.dmcode/sessions`.
  - `internal/todo`: the agent's plan — the store behind `todo_write`/`todo_set`/`todo_read` and the `/todo` view.
  - `internal/ask`: the broker between the `ask_user` tool and the question overlay. Its own package because `tools` cannot import `ui` and `ui` does not build tools; the `Broker` is the seam.
  - `internal/i18n`: user-facing strings; English is the source language, `catalog_ru.go` holds the Russian one.
  - `internal/imgprev`: turns image bytes into a picture in the terminal and into
    the reduced bytes that go on the wire. Two outputs from one input, in that
    order, so what is drawn is what is sent. `Load` does all of it.
  - `internal/clipimg`: reads the clipboard's picture. A package of its own
    because the answer is entirely platform-specific — a Windows screenshot is a
    DIB, a macOS one a file URL, a Linux one an image URI from whichever tool owns
    the selection — and nothing about that belongs in the TUI.

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

### From a change in the transcript to the file

A `kindDiff` row can be clicked and the editor opens the file at that line
(`internal/ui/jump.go`). Five things about it are not obvious, and each is one
that reads fine until it is wrong:

- **The line number has to survive as a value, not as text.** By the time the
  click arrives the row is styled, gutter-numbered, maybe wrapped onto a second
  screen row and maybe ANSI-truncated, so the number is a `codeRef` beside the
  rendered row (`renderedRows`). Reading it back out of the drawn row would mean
  re-deriving the gutter's own arithmetic — including which blank gutter was a
  wrapped continuation — which is the "answers a different question" mistake.
- **`cachedLine` holds the refs, not just the strings.** The render cache exists
  so a streamed token re-wraps one line, which means most frames reuse it. A
  cache that kept the rows and dropped the refs would empty the click index on
  exactly those frames: the click works once, then stops working on every message
  that scrolls past. `TestTheChangeIndexSurvivesTheRenderCache` is the guard.
- **A screen row is not a transcript row.** `transcriptTop()` (the header, when
  it is up, plus the panel's own top border) and `vp.YOffset()` are that
  arithmetic. The tests build a real frame, *find* the change row in it and click
  there, so an off-by-one fails instead of jumping somewhere plausible — the four
  mutations worth trying are `transcriptTop`, the cached refs, the `hostCall` flag
  in `Host.Release`, and `jumpFrom`.
- **A box on screen is a box on screen.** With an overlay up, `buildFrame` draws
  the box and not the chat, so the hit test's arithmetic is answering about a
  screen that is not there. `chatOverlayUp` covers the command list too, which
  is painted *over* the panel rather than replacing it: those transcript rows are
  still in the frame, and the row under the pointer would be the one behind the
  list.
- **`alt+g`, not `ctrl+g`.** The editor answers `ctrl+g` with its git panel
  before the chat sees the key, so the mnemonic would work in a bare chat and do
  nothing once the editor existed — §5's `ctrl+v` trap from the other direction.
- **`Host.Click/Motion/Release/Wheel` set `hostCall`, exactly as `Host.Key`
  does.** A jump taken from a click that arrived through the editor is *inside*
  the editor's `Update`, and a direct `m.ed.Chat = false` there is clobbered by
  the copy that `Update` returns — the jump would be lost every time, silently.

A `-` row carries the old file's number and lands on the replacement: the block
trims a shared prefix and suffix off both texts and numbers both sides from the
same place, so the line a removal used to be on is the line its replacement is on.

### The editor's empty buffer

`internal/editor/editor` always has a tab, so there is always a buffer to type
into and a `cur()` that cannot be nil. `renderPaneRows` indexes
`tabs[pane.tabIdx]` outright and about a hundred other places call `cur()`, so
that tab is load-bearing and cannot simply be deleted — which is why the fix is
presentational rather than structural: `tab.scratch()` derives "this is the
standing empty buffer, not a file" from the state already there (no path, not
dirty, nothing typed), and everything that draws it asks that.

Three consequences, each with a test in `scratch_tab_test.go`:

- **Derived, not tracked.** A `bool` set once would have to be unset in every
  path that clears a buffer; the predicate cannot disagree with the buffer.
- **It is dropped when a file opens** (`openPath`), not just hidden. Left in the
  slice it is an invisible tab 1, and every tab the user can see is numbered
  from 2 — and `Alt+1..9` jumps by real index, so the numbers and the keys
  would disagree.
- **Both tab-closing paths ask `lastTabIsEmpty()`** — `closeActiveTab`
  (`ctrl+w`) and `closeTabAt` (`ctrl+x`, middle-click) each decide "the last tab
  going means quit" independently. A rule written into one of them is a trap in
  the other, which is exactly what `TestCloseLastTabReturnsQuit` used to pin.

### The `/` list

Typing `/` builds the list through `rankCommands` and `commandRank`
(`internal/ui`), and neither is a prefix filter. Three grades, lower first: an
exact name, a name starting with what was typed, and a name containing those
letters **in order**. The third is the whole point — with prefix matching alone
`/p` is `/proxy` and nothing else, so one keystroke left one row and the list
closed the moment it opened. A length tie-break inside a grade settles what the
rank cannot (`/md` is `/mode`, not `/models`), and the highlighted row is the one
a single `enter` takes, so its being the *likely* one is the whole contract.

Three things that are easy to get wrong here:

- **An empty needle keeps the declared order.** Every command ties at rank 0, so
  the length tie-break would otherwise sort the whole list by how long the
  command names happen to be. `rankCommands` returns early instead.
- **The list of commands and the set of commands must be one set.** `/debug` was
  handled by the dispatcher, named in the README, and in neither the `/` list nor
  `ctrl+p` — `/de` matched nothing. A command that works but is not offered is
  invisible, and `TestEveryCommandThatWorksIsOffered` is the guard. `/model <id>`
  is deliberately *not* listed: bare `/model` does nothing, and offering it would
  advertise a trap.
- **`HasPrefix` on a command name is a trap in the dispatcher too.** `/debug`
  answered to `/debugger` too; every other prefix command uses `CutPrefix`, and so
  does this one now.
- **`enter` on a row that needs an argument completes it; it does not run it.**
  `/im`, `enter`, path is the flow the list invites, and it used to run `/image`
  with no argument: a usage line, an emptied prompt, and the path sent to the
  model as a question about a filename. `command.takesArg` is the per-command
  half, and it is deliberately narrow — `/proxy`, `/cd`, `/new`, `/mode` and
  `/todo` all have a useful *bare* form, and completing those would take it away.

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

### Pictures

Five decisions in here are the ones to keep:

- **The art is stored in `line.text`, not in a field.** `line{kind, text}` is
  built positionally in a hundred places, so a third field would touch all of
  them. `kindImage` + `verbatim` renders the stored string as-is instead, which
  means `cachedLine` needed no change either — its key is `(kind, text, width)`
  and the art *is* the text.
- **The preview is a fixed width, not the panel's.** A verbatim row that does not
  fit is dropped whole, so a preview sized to the panel would vanish on a narrower
  terminal; a fixed one also means `rowRows` never has to re-render it.
  `captionLast` is the exception that keeps this honest: when the block cannot
  fit, the caption row survives on its own, because a turn sent as nothing but a
  screenshot would otherwise leave nothing on screen at all.
- **The bleed is part of the picture, not a margin beside it.** The transcript
  indents every row by `chatIndent`, so a preview has to reach the panel border.
  Two other ways were tried and both are worse: bare spaces carry the terminal's
  own background (a dark stripe down the left on a dark terminal), and painting the
  margin in a sampled colour is worse still, because a space holds one colour while
  the cell beside it holds two — so a seam runs down the edge wherever the image
  changes top to bottom. `imgprev.Load` takes a `bleed` and samples `cols+bleed`
  cells, so the edge is continuous.
- **A pending attachment is drawn in exactly one place.** `addPending` writes
  nothing to the transcript; the strip above the input is the whole of what an
  unsent picture looks like, and `showPreviews` runs at the send, which is the
  moment it joins the conversation. It used to do both, and that was a bug rather
  than a redundancy: the same picture sat on screen twice, and dropping it removed
  one copy and left the other. A rewind returns pictures to the strip for the same
  reason — an un-sent turn's pictures are undecided, and the transcript is for
  things that happened.
- **`watchInputImage` runs on every keystroke**, so its guard is the whole design:
  a path is only read once its extension says it could be an image, and only a
  change of that candidate (`m.watchedPath`) triggers anything. It returns a command
  rather than decoding inline, because a file read inside `Update` freezes the
  interface on the keystroke that completed the path.
- **Reduction happens before both outputs.** `imgprev.Load` shrinks to 1568px
  *first*, then renders the preview from the shrunk image and hands the same bytes
  to the wire. Drawing the original and sending a reduced copy would make the
  preview a picture of something the model never saw.
- **`chatMessage.Content` is `any`.** The chat API has two shapes for it and a
  turn with no picture still sends a plain string — byte-identical to what it was
  before images existed, which is what keeps an endpoint that implements only the
  string form working. `Text()` exists because every reader wants the prose and
  none of them should type-assert.

`contentsToChat` gained an `InlineData` case that used to be **absent**, and its
absence is the bug this whole feature would have shipped against: a part that is
neither text, nor a call, nor a response was skipped, so a turn carrying a picture
would succeed and the model would answer about something else with nothing on
screen to say a picture was involved. An empty blob is now an error for the same
reason.

**Pictures only reach the chat wire.** `canSendImages()` refuses on
`/v1/responses` at the moment of the attach, naming `DMCODE_API=chat`, because
ADK's `openaimodel` rejects an inline blob outright (`unsupported content part:
InlineData` — true in both v2.4.0 and v2.5.0). Refusing early turns an SDK
error into an instruction.

### Session store

`memsession.Service` replaces `session.InMemoryService` for one reason: that
service hands out a *copy* of the event slice, so nothing upstream can take an
event back, and neither ctrl+z nor a session switch is possible against it. Every
ADK session interface (`Service`, `Session`, `Events`, `State`) is exported, so
the store is implemented outside the ADK package.

- `RewindToLastUserMessage` cuts at the last `Author == "user"` event, removing
  that message too, and replays state from what survives.- Writes are append-only JSONL, one file per session, with a metadata header on
  the first line so `/sessions` never has to read a conversation to list it.
  A rewind rewrites the file (temp + rename); an error is recorded, not returned,
  so a full disk cannot fail a turn.
- **`Turn` carries `Entries`, not an `Agent` string.** `eventText` reads text
  parts and nothing else, so a transcript built from it dropped every tool call,
  every result and every change block — a restored session came back as a
  conversation in which the agent had plainly done no work, while the events sat
  on disk unread. `Entry` hands back the raw `*genai.FunctionCall` /
  `*genai.FunctionResponse` rather than text this package formatted, because the
  UI has one renderer for a tool line and one for a result
  (`toolLine`/`toolArgs`/`renderToolResponse`), and a second set of formatters
  would be a second answer to the same question with nothing to catch the drift.
  `Broken` means *no* entries, not "no prose": a turn whose model answered with a
  tool call is not a broken one.
- `Service` is mutex-guarded and every method that may do I/O takes the write
  lock — including `load`, which can lazily read from disk. `recordWriteErr` is
  the one helper that must **not** take the lock itself.

### Coming back to a session

`-s` is the door back into a saved conversation, and it is taken out of
`os.Args` by hand rather than declared, because Go's `flag` package cannot
express an optional value: a string flag demands an argument, a bool flag refuses
one, and the two requests are different — "the newest session" is what a user
types almost every time, and it is not an id anyone can guess. `takeResumeFlag`
also leaves a following `-flag` alone, so `dmcode -s -C ~/myapp` resumes *and*
relocates rather than reading `-C` as a session id.

Two things about where the id is settled, both of which are answers to "which
bug":

- **main resolves it, before the UI exists.** `NewestSessionID` reads only the
  header of each session file, and `r.Run` is handed `m.sessionID` at *turn*
  time — so a session chosen after `newRunner` would have the runner already
  pointed at the fresh id `InitialModel` minted. An empty answer is not an error:
  a store with nothing in it means a new session, and failing a start over a flag
  the user typed out of habit would be worse.
- **The hint is a string, and it is printed after `prog.Run` returns.** The
  transcript was on the alternate screen, so every row that named the session —
  header, sidebar, `/sessions` — is gone by then, and this is the only moment a
  line about it survives. Making it `resumeHint(id) string` rather than a print
  is what lets a test say the line names a command the parser accepts.

### Retries

`failoverModel.GenerateContent` retries a single endpoint before moving to the
next. `retryable()` answers "is another host worth trying" and so allows a 401;
`sameEndpointRetryable()` answers "is asking this host again worth it" and does
not. Keeping them separate is the point — a rejected key is another endpoint's
problem, not a reason to repeat the same request twice.

### A local model loads behind the interface

`discover.StartGGUF` starts llama-server and returns; the wait is somebody else's
job. `main` no longer blocks — `DetectProviders` returns a `Session` carrying the
pool **and** the in-flight `Launch`, because a pool alone would claim the model is
available when nothing is listening on its port yet.

Four things about it are not obvious, and each is one that reads fine until it is
wrong:

- **The split is spawn / wait, not slow / fast.** Everything checkable in a
  millisecond — the file exists, the binary exists — is checked *before* the
  interface is drawn, because a mistyped path should be reported in milliseconds
  rather than after the load timeout. Only loading the weights is deferred.
- **`ggufState` is set at spawn, not after the wait.** That is the whole difference
  between a load that can be abandoned and one that cannot: `StopGGUF` reaching the
  child mid-load is what stops quitting from leaving an orphan holding a large
  model in memory. The state is also what makes a second `/setup` pick *share* the
  in-flight load instead of serialising on a mutex for its whole duration, on the
  event loop.
- **The UI holds an interface, not `*discover.Launch`** (`ggufLoad`). Every
  behaviour worth testing here — refusing a message, cancelling, swapping the
  provider in — needs a load that is not a real model, and the only way to have a
  load without one is the seam. `internal/discover` tests spawn the test binary
  itself as a fake llama-server, because "the caller is not blocked" and "the child
  is killed" are only provable against a real process.
- **A cancelled load is not a failed load** (`discover.ErrCancelled`). The user
  ended it; a report naming a timeout they caused is the tool arguing with them.

`handleGGUFReady` sets `m.pool` as well as `m.prov`. A runner rebuilt from a stale
pool — `Tab`, `shift+tab`, `/cd` all do — would otherwise put the session back on
the endpoint the user just switched away from, so the swap would hold for one turn
and be quietly undone.

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
- **The boundary does not apply to a picture the user attached.** Screenshots live
  in `~/Pictures` as often as in the project, and a user naming their own file is
  not the agent reaching out of its workspace. `tools.ResolveExisting(p,
  allowOutside)` is the seam: the symlink re-check is still done, but a path
  outside the root is only refused when the caller says so. An image leaves the
  machine either way, which is the thing the boundary is actually for.

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
- **An oversized picture is a lost turn, not a slow one.** The session store reads
  JSONL with `bufio.Scanner` capped at `maxLineBytes` (8 MB) and *skips* a line it
  cannot scan (`readLines`, `store.go:377`). Base64 of a 6 MB screenshot does not
  fit, so the event would vanish and the model's memory would lose a message with
  nothing on screen to say so. `imgprev`'s 4 MB limit is what keeps that
  unreachable; it is not a courtesy to the endpoint.
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
  same endpoint once: the same conversation, a note saying the call was too big
  (with a concrete recipe for `write_file`: a short head, then appends anchored
  on the last line), and — only where the endpoint reported its usage — a budget
  of twice what the call had already used. Two rules keep it honest: a request
  that carries a budget of its own is left alone, which keeps the 64-token tool
  probe in `verify.go` a single request, and the re-ask happens once. Text the
  first attempt already streamed stays on screen — the transcript cannot take it
  back — but the re-ask goes ahead anyway: a duplicated sentence is cheaper than
  a lost turn, and the second attempt is preceded by a blank-line delta so the
  re-done narration lands on its own row instead of glued to the half-answer.
  A second truncation is reported as the first one, never as whatever the failed
  re-ask said. The cost is a second generation on a local model, and it can
  still fail — a model that insists on one huge call gets the truncation error,
  which now also tells the user what to ask for next.

---

## 5. Four bugs that only a measurement found

Attaching a picture compiled, passed its unit tests, and was wrong in four
separate ways. Each one looks correct when read, and each one survived a careful
look at the code. They are collected here because the pattern is the lesson: in
every case the code answered a **different question** than the one being asked.

- **`ctrl+v` was bound to a key the terminal never sends.** A terminal that binds
  `ctrl+v` converts the key to a paste and does not deliver the keystroke, so
  `case "ctrl+v"` was unreachable on most terminals — and a screenshot on the
  clipboard produces no text, so there was no paste to fall back to and the
  shortcut did nothing at all. `tea.PasteMsg` now asks the clipboard too. The
  keystroke route asks for the picture and *then* the text, because it used to
  return the picture command unconditionally: on any chat-wire provider, a
  text-only clipboard produced no message whatsoever, so `ctrl+v` had silently
  stopped pasting text too.

- **An advertised clipboard format is not a renderable one.** Windows synthesises
  clipboard formats from the ones it holds, so an application that lists
  `CF_DIBV5` and declines to render it leaves the format "available" while
  `GetClipboardData` returns `ERROR_NOT_FOUND` — surfaced to the user as "Element
  not found" while a readable `CF_DIB` sat beside it. `clipimg.firstImage` takes
  the first format that actually *decodes*, because a handle that is not a DIB
  cannot be told apart from a missing one until its header has been read.

- **The bounds check forgot the header shares the allocation.** `decodeDIB`
  compared the pixel count against the handle's size, so a DIB overstating its
  dimensions by more than a header's worth passed and the read went past the end
  of memory the clipboard owns — immediately below a comment claiming every read
  was bounds-checked. The check is now `off+total > size`.

- **The preview never reset its colour.** Each cell re-states both of its
  colours, so the one surviving a row is the last cell's, and the caption below
  was drawn in the colour of the image's bottom-right pixel. Invisible on a
  picture whose edges are dark, which is why looking at it did not find it.

And one about the tests rather than the code: the clipboard round-trip test
destroyed the user's clipboard on every `go test ./...`, while its own comment
claimed an environment gate it never implemented. It is gone. `decodeDIB` is
tested through synthesised bytes and the format policy through injected
functions, because **a test that has to reach a global, shared, destructive
resource to check a branch is a test that will fail for someone else** — and will
be deleted, or worked around, by whoever it breaks for.

That rule was then broken a second time by the editor tree that landed after it,
which called `clipboard.WriteAll` straight from ctrl+c, ctrl+x and ctrl+v. Every
copy and cut test put its own fixture into the user's clipboard — which is why
`go test ./...` left "hello" and "alpha" there — and ctrl+v *read* the real one,
so what a paste test received depended on what the user had copied last. The
seam `internal/ui` already had for reading is now on both sides in both
packages (`writeClipboardText`, `readClipboardText` in `panel_select.go` and
`image.go`), and `fakeClipboard` swaps it for the duration of a test.
`TestClipboardTestsNeverTouchTheRealClipboard` is the guard on that seam: a
direct call still compiles and still passes every other test, so something has
to assert that the copy reached the write indirection. The lesson generalises —
**the fix for a destructive test is an injection point plus a test that fails
when the injection point is bypassed, not the removal of the test.**
