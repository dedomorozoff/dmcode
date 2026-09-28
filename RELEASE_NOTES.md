# dmCode v0.1.4

A conversation you can take back: undo the last message, keep several sessions and
switch between them, and let the agent plan, ask and delegate.

> ### ⚠️ Unverified — this release is a work in progress
>
> **Everything below compiles and passes unit tests, but none of it has been
> verified end to end.** There are no live checks against a real endpoint, and no
> TUI session has been driven through a rewind, a session switch, a question
> overlay, a plan or a delegation. Treat this as a branch under construction:
> test it, expect rough edges, and please report what breaks.
>
> What that leaves unproven: that a rewind cuts the model's memory and not just
> the screen; that a session read back after a restart is what a live turn would
> have written; that the question timer behaves against a live turn; that
> `switch_mode` lands between turns without stranding the runner; and that a
> sub-agent's report survives the trip back into the parent's context.
>
> Known gaps, stated plainly: a rewind does not rewrite
> `~/.dmcode/history.jsonl`, so an undone prompt is still offered by `/history`
> until the next start. Cancellation reaches the context but not the process, so
> a running `run_command` finishes on its own schedule. The workspace boundary is
> lexical, and a symlink inside the directory can still reach outside it. Session
> file permissions cannot be asserted on Windows, so that test skips there.

## Install

```bash
# macOS / Linux / BSD
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
```

Or from a checkout:

```bash
make install       # -> $(go env GOBIN), or GOPATH/bin
```

Or with Go 1.26+:

```bash
go install github.com/dedomorozoff/dmcode@latest
```

## What's new since v0.1.3

### `ctrl+z` undoes a message

`ctrl+z` (or `/rewind`) takes back the last turn. The prompt comes back into the
input so you can edit it and send it again, the transcript loses the exchange,
and the model's memory is cut at the same place — so the next reply cannot be to
a question you can no longer see. Press it again to go back further.

The two cuts have to land together, and that is why dmcode now runs on its own
session store. The stock ADK in-memory service hands out a *copy* of the event
slice, so nothing upstream can take an event back: against it, both a rewind and
a second conversation are impossible. `internal/memsession` implements the same
interfaces, which is enough to make both ordinary.

### Sessions that survive, and a way to get back to them

Conversations are kept in `~/.dmcode/sessions`, one JSONL file each, and are still
there after a restart. `DMCODE_SESSIONS_DIR` puts them beside a project instead.

- `/sessions` lists them, searchable by typing. `enter` switches, `d` deletes —
  twice, because a deleted conversation cannot be brought back, and `esc` closes.
- `/resume <id>` opens one directly.
- `/new [name]` starts a fresh conversation. **The old one is kept**, not
  discarded, so a `/new` pressed by mistake costs one keypress.
- Switching prints the last few exchanges of the session you opened. Without
  that you would be looking at an empty screen next to a model that remembers
  everything.

### A rate limit no longer ends the turn

A failed request is retried on the same endpoint before the pool moves on: three
attempts by default, with a pause that doubles each time and a little jitter so
that clients which all hit the same limit do not all come back at the same
instant. The transcript says what it is waiting out — `↻ attempt 2/3 in 1s — HTTP
429` — because a silent pause after an error reads as a hang.

`DMCODE_LLM_RETRIES` and `DMCODE_LLM_RETRY_MS` tune it. Two things are never
retried: a request the provider rejected (it will be rejected again, here and on
every other host) and one that has already streamed part of an answer (the user
would read it twice).

### The agent's plan

`todo_write`, `todo_set` and `todo_read` are the plan. The agent publishes it
before it starts and moves each step along as it works, so a long task is visible
while it happens rather than described afterwards. `/todo` prints it, `/todo
clear` empties it, and the sidebar carries a `PLAN 2/5` line with the step in
hand.

### Questions, with a timer you choose

When a decision changes what the agent would do, it stops and asks. You get the
question, two to five concrete options, the one it recommends marked, and the
option to type your own. `↑` `↓` and `enter` choose, `space` ticks several when
it is a multi question, `c` opens the text field, `esc` skips and lets the agent
carry on with its best guess.

The wait is **off by default**. `DMCODE_ASK_TIMEOUT=60` turns on a countdown; if
it runs out the recommended option is chosen, and the model is told it was chosen
by default and not confirmed by you.

### Delegation

`sub_agent` hands one research task to a separate agent with its own context:
surveying how something works across many files, tracing a call path, listing
every call site. Its reading does not land in your conversation, and it can
neither write anything nor ask you anything — so what comes back is yours to act
on. Its progress is shown as it happens, so a delegation that reads forty files
does not look like a frozen turn.

### Leaving plan mode by itself

In plan mode the agent can call `switch_mode` once it has a plan it is confident
in. The switch happens at the end of that turn — the runner cannot be rebuilt
underneath a running one — and the conversation carries over into act mode.

It cannot go the other way. Returning to plan mode is `Tab`, and that one is
yours; the tool is not even present in act mode, which is also what keeps the
agent from planning and unplanning the same change for ever.

Plan mode also keeps the non-write tools it now needs: `todo_*`, `ask_user`,
`sub_agent` and `switch_mode`. A mode whose entire output is a plan should not be
unable to publish one.

### Configuration

| Variable | What it does |
|---|---|
| `DMCODE_SESSIONS_DIR` | where conversations are kept (default `~/.dmcode/sessions`) |
| `DMCODE_LLM_RETRIES` | attempts per endpoint before failing over (default 3) |
| `DMCODE_LLM_RETRY_MS` | first backoff pause, doubling after that (default 500) |
| `DMCODE_ASK_TIMEOUT` | seconds before a question picks its own answer (default 0 — off) |

# dmCode v0.1.3

A working interface: markdown actually renders, the mouse wheel scrolls, and the
agent can plan before it changes anything.

## Install

```bash
# macOS / Linux / BSD
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
```

Or from a checkout:

```bash
make install       # -> $(go env GOBIN), or GOPATH/bin
```

Or with Go 1.26+:

```bash
go install github.com/dedomorozoff/dmcode@latest
```

## What's new since v0.1.2

### Markdown renders

Replies were pushed through the same plain-text wrapper as everything else, which
re-wrapped them on spaces: bold words showed their `**`, headings showed their
`##`, and a code block lost every indent that made it readable. Replies are now
laid out as markdown before wrapping.

- **Headings, `**bold**`, `*italic*` and `` `code` ``** are styled and the markers
  are consumed. Emphasis carries colour as well as weight, so it stays visible on
  terminals whose font has no separate bold face — a bold span in the same colour
  as its surroundings is indistinguishable from plain text.
- **Fenced code keeps its shape.** Indentation is preserved rather than re-flowed,
  and an over-long line is cut rather than wrapped, because a wrapped code line
  reads as a different line of code. A half-arrived reply with an unclosed fence —
  the normal case mid-stream — renders as a block to the end of the text.
- **Tables and blockquotes.** Pipe tables become aligned columns, dropping the
  raw `|`; a table too wide for the panel sheds its borders and shrinks instead
  of breaking the frame. A `>` line becomes an indented aside.
- Prose without markup comes through unchanged, and tool output and your own
  prompts are never treated as markdown — a JSON payload containing `**` survives
  intact.

### The mouse wheel scrolls

Two things were broken, and both had to be fixed. The view never enabled mouse
mode, so the terminal did not report wheel events at all; and no event was ever
passed to the viewport, which owns the wheel handling. Scrolling also released
the follow-the-tail stick, so a streamed reply no longer yanks the view back down
mid-read.

`/mouse` turns wheel scrolling off and gives native drag-select back, for when
you want to copy by hand.

### Plan and act modes

`tab` switches between them. Inside a `/` command, tab still completes
suggestions.

- **ACT** is the full tool set.
- **PLAN** is `read_file`, `list_dir`, `grep` and `glob` only. The agent
  investigates and returns a plan, changing nothing.

Plan mode withholds the write tools rather than asking the model in the prompt,
and `run_command` is withheld too — a shell can write a file through `>` or
`Out-File`, so "read-only" cannot be promised through it. The current mode shows
as a badge in the status bar, the sidebar lists only the tools actually
reachable, and a switch is refused while a turn is running. The conversation is
kept across a switch.

### It works where you start it

`dmcode -C ~/projects/myapp` (or `--dir`) works in a directory other than the
one you launched from, so a binary on `PATH` can be pointed at any project.
`/cd <path>` moves the session at runtime and bare `/cd` reports where you are.
The sidebar shows the full path, trimmed from the left.

**Every tool is now confined to that directory.** A path resolving outside it is
refused with an error naming both directories, and `run_command`'s `work_dir` is
checked the same way. Before this, `read_file` with `../../.ssh/id_rsa` simply
succeeded.

### Diagnostics

- **`/debug`** prints the line kind, the markdown flag and the width of the last
  transcript lines. A reply rendering as raw markup is a routing problem — the
  text is stored under a kind that is not treated as markdown — rather than a
  parsing problem, and one line of output tells the two apart.
- The terminal's colour capabilities are now requested at startup, so styling is
  not stuck in a conservative base palette on terminals that support more.

## Assets

Binaries for `linux`, `darwin`, `freebsd`, `openbsd` and `netbsd` on `amd64`
and `arm64`, plus `windows-amd64` as both a raw `.exe` and a zip. Packages:
`.deb`, `.rpm`, Arch `.pkg.tar.zst`, and a termux tarball.

## Known limitations

*(At v0.1.3. Most of these are addressed in v0.1.4 above — see the current
section for what is still true.)*

- ~~No cancellation yet: `Esc` interrupts a turn, but a running shell command is
  not cancellable.~~ Fixed: the turn runs on a cancellable context. A child
  process still finishes on its own schedule.
- ~~Switching models starts a new session, so conversation history is dropped.~~
  Fixed: `/model` keeps the session id and the store.
- The transcript is re-rendered on every streamed token, so a long session can
  lag. The fix is an incremental per-line cache. **Still true.**
- The workspace boundary is a lexical check. A symlink *inside* the directory can
  still reach outside it, because `write_file` must be allowed to create files
  that do not exist yet and cannot be resolved first. **Still true.**
- `tab` is overloaded: it completes a suggestion inside a `/` command and
  switches mode everywhere else. **Still true.**
- ~~`~/.dmcode/history.jsonl` is append-only~~ — a prompt that was rewound is
  still offered by `/history` until the next start. The session files are
  rewritten; the prompt history is not.

See [ROADMAP.md](https://github.com/dedomorozoff/dmcode/blob/main/ROADMAP.md)
for what is planned next.
