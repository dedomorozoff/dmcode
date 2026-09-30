# dmCode v0.1.5

A conversation you can take back: undo the last message, keep several sessions and
switch between them, and let the agent plan, ask and delegate.

> ### ⚠️ Unverified — work in progress
>
> **Everything in this section compiles and passes unit tests, but none of it has
> been verified end to end.** There are no live checks against a real endpoint,
> and no TUI session has been driven through a rewind, a session switch, a
> question overlay, a plan or a delegation. Treat this as a branch under
> construction: test it, expect rough edges, and please report what breaks.
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

# dmCode v0.1.4

The interface gets out of the way, there are more providers that are genuinely
free, and dmcode works behind a proxy.

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

### Four more free providers

The setup wizard now offers **Cerebras**, **NVIDIA NIM**, **SambaNova** and
**Hugging Face**. All four issue a working OpenAI-compatible key on signup with no
card attached, and each was probed live before being listed — `/v1/models` answers,
so a key from the signup page is all it takes to reach the agent. NVIDIA NIM alone
serves eighty-odd models, including GLM, Kimi and Nemotron.

**GitHub Models** and **Mistral** were already picked up from a key in the
environment but were missing from the wizard, so they are offered there too.

The preset table and the wizard are two hand-maintained copies of the same
endpoints, free to drift with nothing to show it: a user with `NVIDIA_API_KEY`
exported would run on one model, and the same user picking NVIDIA in `/setup` on
another. The preset list is now a named function and a test compares it against the
wizard by URL, model and wire.

### The status bar says what matters

It carried the model name, a run of hotkeys, the mode and the state. The model was
the third place on screen showing it — the header and the sidebar already have it —
and the hotkey list was a fixed string competing for one row, so on a narrow
terminal it was the part that dropped off, leaving `ctrl+p │ ctrl+y │ esc` half
printed. Neither earns its place.

The bar is now the mode badge leading, then the state, then whatever status fits.
The mode leads because it decides whether the agent is allowed to touch your files,
so it is the one thing that must never be hunted for.

### `tab` means one thing

It completed a suggestion inside a `/` command and switched mode everywhere else —
the same key doing two unrelated things depending on what happened to be in the
input. The command list is now picked with the arrows, which leaves `tab` meaning
one thing everywhere: the mode.

### The command list is a dialog

Typing `/` opens a bordered dialog just above the input, in the same chrome as
`ctrl+p`. `↑` `↓` move through it, `enter` runs the highlighted command, `esc`
closes it and keeps what you typed.

It is drawn **over** the chat panel rather than taking rows from it. As rows of the
frame the list shrank the transcript every time a `/` was typed and jumped back when
it closed, and on a short terminal it ran into the bottom of the screen with the
input box still to fit. The frame is now identical with the dialog open and closed.
Only the chat panel's columns are rewritten, so the sidebar — model, folder, tools —
stays readable while you pick.

### The sidebar reports what changed

The SESSION block now carries a file count and a signed line count:

```
 SESSION
  sess-1448381972…
  turns: 4
  tools: 11
  files: 3
  +128 -34
```

The numbers come from the tools, not the interface: every write passes through one
place, and it is the only place that knows a file's before and after. The tally is
diffed against the content a file had when the session first touched it, so an agent
that rewrites one line three times has changed one line — the sidebar reports the
end state, not the agent's steps. The block stays hidden until something has
changed, and `/new` clears it.


### Behind a proxy

`HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are honoured, so a proxy already
configured for the rest of the system works with no setup. `/proxy` shows the
setting, changes it, and says whether the provider answers through it:

```
/proxy                          show what is in effect
/proxy 127.0.0.1:3128           set one (a bare host:port is fine)
/proxy off                      clear it
/proxy no localhost,127.0.0.1   bypass list, without touching the proxy
```

The change applies to the running session and is written to `.env`, so it survives
a restart. Two details worth knowing:

- Go's own `http.ProxyFromEnvironment` reads the environment once, on the first
  request, and caches it — a proxy set from inside the program would appear to do
  nothing until a restart. dmcode consults the proxy per request instead, which is
  what makes `/proxy` take effect immediately.
- `/proxy off` **removes** the variables from `.env` rather than just ceasing to
  write them. A proxy left in the file would be read back on the next launch and
  silently come back.

`localhost` and the loopback addresses are never proxied: a proxy that cannot reach
the machine's own services is common enough that following it would break local
providers that otherwise work.

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
- The change tally counts what passed through the tools. A file changed by
  `run_command` is not counted, and neither is a change made outside the session.
- ~~`tab` is overloaded: it completes a suggestion inside a `/` command and
  switches mode everywhere else.~~ **Fixed in v0.1.4**: the command list is picked
  with the arrows and `enter`, so `tab` only ever switches mode.
- ~~`~/.dmcode/history.jsonl` is append-only~~ — a prompt that was rewound is
  still offered by `/history` until the next start. The session files are
  rewritten; the prompt history is not.

See [ROADMAP.md](https://github.com/dedomorozoff/dmcode/blob/main/ROADMAP.md)
for what is planned next.
