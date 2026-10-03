# dmCode v0.2.1

dmcode is a workspace now. The chat is still the agent you know; the same
window also holds a real code editor — project tree, git, a terminal, splits,
bookmarks and LSP — and switches between them with one key.

> ### ⚠️ Two things to read before trusting them
>
> **The editor's keys and the terminal panel have been exercised, but not
> driven for hours by a person.** The frames, the mouse arithmetic and the
> chat-mode composition are covered by tests that assert the row counts and
> the hit-testing; what is not proven is how the editor feels over a long
> session, or how the panels behave on platforms other than the one this was
> built on.
>
> **Sending a picture to a model is still unverified.** Attaching and
> previewing are confirmed working on Windows against a live session; no
> picture has been through a real vision endpoint, so whether the `image_url`
> data URL dmcode puts on the wire is one a live endpoint accepts is untested.
> Please report what breaks.

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

## What's new since v0.2.0

Three fixes from the workspace's first days, no new surface:

- `ctrl+e` in the editor now returns to the chat — the key that opened the
  editor toggles both ways. `ctrl+q` still works, `F1` keeps the help panel,
  and the help table and the status hints name the new binding.
- The project tree no longer opens itself when the editor is entered. It
  answers `ctrl+b`, `F9` or its own status-bar icon, and otherwise stays
  where the user last left it.
- Status-icon tooltips no longer freeze over the transcript. Chat mode keeps
  tracking the icon strip on mouse motion, so a callout left over from the
  editor mode clears on the first move instead of sitting on the screen with
  nothing to remove it.
- `ctrl+q` quits dmcode from the chat — the key the sidebar has advertised
  all along actually works now. Inside the editor it still means "back to
  the chat"; from the chat the key ends the program, the way `ctrl+c` did
  on an empty prompt.
- A multi-cursor crash reported from the field is fixed: a line join moved
  the lines under the cursors below it without moving the cursors, and the
  next backspace sliced past the end of an empty line, taking the whole
  session down. Joins now run strictly bottom-up regardless of cursor
  order — which also un-skips the higher of two joins — and shift every
  cursor at or below the joined line; a stale cursor is clamped instead of
  panicking.

## What's new since v0.1.7

### The editor lives in the same window

`ctrl+e` opens it, `ctrl+q` puts it back. One screen rather than one window per
thing: the transcript is the main area when the editor is closed, and the panel
toggles stay live in both modes.

What came across is the editor body — syntax highlighting, the project tree, a
git panel with inline diffs and blame, a real PTY, fuzzy file finding, splits,
bookmarks, LSP completion and go-to-definition. dmcode keeps its own agent, so
the old editor's AI panel, ghost text, chat rail and DAP debug panel are gone
rather than half-present.

`F1` inside the editor lists its keys. It is a long list on purpose: a project
tree, a git panel and a terminal are three different grammars, and a table in a
README is not where anyone looks while they are typing.

### One status bar, and only what belongs to the screen you are on

The bottom row is the workspace's, and its four icons are: editor, tree, git,
terminal. Each is clickable, each shows its label on hover.

The editor icon is first deliberately. Every other icon opens a panel; that one
changes what the whole screen is, so it belongs where the eye lands before a row
of toggles has been read. It is also the only icon whose state is a mode rather
than a panel, and it lights up while the editor is up.

That reorder freed the rest of the row. In chat mode the bar now carries the
icons and the git branch, and nothing else: `Ln 4, Col 12`, the encoding, the
language tag and the F1 hint are gone, because they described a file nobody can
see on that screen while competing with the only part of the row a user can act
on. In the editor mode all of it comes back — the file's own state belongs to
the file's own screen.

### The agent can see

Drop a screenshot into the prompt, paste one from the clipboard, or attach one
with `/image` — and dmcode draws it, in colour, before you send it.

An attachment lives in exactly one place at a time: while it waits it is a strip
above the input, and when you send that same rendering moves into the transcript.
Three ways in — `/image <path>`, drag-and-drop of a picture path in the prompt,
and `ctrl+v` for the clipboard's picture on Windows.

Reduction happens before both outputs: an image over 1568px is shrunk first, and
*then* the preview is drawn and the same bytes go on the wire. Drawing the
original and sending a reduced copy would show you a preview of something the
model never saw.

Pictures reach only providers on the `/chat/completions` wire, and dmcode says so
at the moment you attach rather than failing later inside the SDK.
`DMCODE_API=chat` sends everything down the chat wire instead.

### A conversation you can take back

`ctrl+z` rewinds the last turn — the prompt comes back to the input and the
answer goes with it — and `/sessions` lists conversations with `/resume` to
switch between them.

### Selection, yolo mode, and the tools that now work

`ask_user` and `todo_write` compiled, passed their unit tests and did nothing at
all: the transcript showed a tool call and a schema-validation message, and no
result. The cause was not their logic but their *generated argument schema* —
`functiontool` derives it from the Go type, so a field with no description
reaches the model as a bare `{"type":"string"}`, and a field is required unless
its JSON tag says otherwise. `ask_user` had declared its options as a nested
object, so every shape a model naturally sent was refused. Both are flat now,
every field described, with the exact payloads a model was observed to send driven
through a real `runner.Run` turn by the tests.

`shift+tab` is yolo: act's reach with `ask_user` withdrawn, so "the agent will
not stop to ask" is a property of what it can reach rather than a line in an
instruction it is asked to believe.

A drag selects in the transcript and releases into the clipboard, with a
character count in the status bar — a selection can be one word or three screens
of build output, and "copied" alone does not say which happened. `/mouse` turns
it off, because with the mouse on the terminal's own drag-select is gone.

## Fixes worth naming

Each of these looked correct and was wrong, and none was found by reading the
code. They are here because the pattern is the lesson.

- **The chat frame was one row short with the terminal docked**, so the status bar
  sat a character above the bottom edge. Chat mode framed the terminal without
  the leading divider row that `termExtraRows` reserves and that every mouse
  coordinate counts from.
- **The tests wrote to your clipboard.** Every copy and cut test in the editor
  suite put its own fixture — "hello", "alpha" — into the one clipboard the user
  has, so a `go test ./...` left that text behind; and a paste test *read* the
  real one, so what the buffer received depended on what you had copied last.
  The clipboard is now reached through two indirections in both packages and
  swapped per test, and a test fails if anything calls the clipboard directly.
- **`ctrl+v` was bound to a key the terminal never sends.** A terminal that binds
  `ctrl+v` converts it to a paste, and a screenshot produces no text to fall back
  to, so the shortcut did nothing at all.
- **A clipboard format the OS advertises is not one it will render.** Windows
  synthesises formats from the ones it holds, so an app that lists `CF_DIBV5`
  and declines to render it left the format "available" while `GetClipboardData`
  returned `ERROR_NOT_FOUND` — as "Element not found", with a readable `CF_DIB`
  beside it.
- **The picture bounds check forgot the header shared the allocation**, so a DIB
  overstating its dimensions by more than a header's worth read past the end of
  memory the clipboard owns.
- **The preview never reset its colour**, so the caption below was drawn in the
  colour of the image's bottom-right pixel — invisible on a picture with dark
  edges, which is why a careful look missed it.
- **The help had rotted.** It listed a duplicate git-diff row, a tree-ops row
  pointing at `help.tree_ops`, a key that does not exist and so rendered as the
  raw key, and it left out F12, Ctrl+Space and Ctrl+/. It also had no entry for
  the way back to the chat. A test now holds the list to the catalog and to
  itself, which is how the missing key was found.

## Known limitations

Stated plainly, not hidden.

- Sending a picture to a model is unverified end to end (above).
- A rewind does not rewrite `~/.dmcode/history.jsonl`, so an undone prompt is
  still offered by `/history` until the next start.
- Cancellation reaches the context but not the process, so a running
  `run_command` finishes on its own schedule.
- The workspace boundary is a lexical check at resolve time; the tools that open
  files close the symlink gap themselves, but `grep` skips symlinks rather than
  following one out of the tree.
- The editor's sub-agent nested turn, the rewind against a real runner, and the
  session-file round trip through `runner.Run` all have unit tests and no
  end-to-end check.

# dmCode v0.1.8

The agent can see. Drop a screenshot into the prompt, paste one from the
clipboard, or attach one with `/image` — and dmcode draws it, in colour, before
you send it, so you can see what you are about to ask about.

> ### ⚠️ Partly verified — read this before trusting it with a real screenshot
>
> **Attaching and previewing are confirmed working on Windows**: a clipboard
> screenshot is read, drawn, and attached, and a dropped path attaches the moment
> the path is complete. That was tested against a real clipboard, on a real
> session, and the failures it took to get there are listed at the bottom because
> each of them was invisible until something was measured.
>
> **Sending a picture to a model is not yet confirmed.** A picture has not been
> put through a real vision endpoint end to end, so what is unproven is that the
> `image_url` data URL dmcode puts on the wire is one a live endpoint accepts, and
> that the model then answers about the picture. Everything up to the request is
> covered by tests, including the exact JSON shape.
>
> Known gaps, stated plainly: `ctrl+v` reads the clipboard on Windows only, because
> a screenshot is a DIB there and every other platform needs its own protocol —
> pasting a picture on Linux or macOS falls through to the text paste. 24- and
> 32-bit DIBs are read; a 1-, 4- or 8-bit palettised one, or a compressed one, is
> refused with a message naming what is supported. The picture sent to the model
> is the same reduced bytes the preview is drawn from, never the original.

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

## What's new since v0.1.7

### You can see the picture you are about to send

An attachment is drawn in the transcript out of half-block glyphs, two pixels to a
cell, with the file name, the dimensions and the size underneath:

```
[image] screenshot.png · 1920×1080 · 214 KB
```

It is in exactly one place at a time. While it waits it is a strip above the
input; when you send, that same rendering moves into the transcript and the strip
empties. It used to do both, and that was a bug rather than a redundancy: the same
picture sat on screen twice, and removing it took away one copy and left the
other. So "what am I about to send" is answerable from the screen, and there is
only ever one copy of it.

### Three ways in, and two of them are how you already work

- `/image <path>` attaches without sending, so you can look and decide.
- **Drag and drop.** A terminal cannot hand over a file — dropping one inserts its
  *path* as text — so a picture path in the prompt is taken out of the prompt and
  attached. The model reads your question without the filename in it. A quoted
  path with spaces works; a path that is not a picture stays exactly where it is.
- `ctrl+v` pastes the clipboard's picture when it holds one, and falls through to
  the ordinary text paste when it does not. Windows only.

### The preview is the picture, not a picture of the picture

Reduction happens first and both outputs come from it: an image longer than 1568
pixels is shrunk, and *then* the preview is drawn and the same bytes go on the
wire. Drawing the original and sending a reduced copy would show you a preview of
something the model never saw.

The preview is also a fixed width rather than the panel's, because a row that does
not fit is dropped whole — a preview sized to the panel would vanish on a narrower
terminal instead of shrinking. And the left edge is part of the image: the
transcript indents every row, so the picture samples two extra cells to reach the
border. A margin of spaces was tried first and reads as a dark stripe down the side
on a dark terminal; painting the margin in a sampled colour is worse still, because
a space holds one colour while the cell beside it holds two, so a seam runs down
the edge wherever the image changes top to bottom.

### Pictures need the chat wire, and dmcode says so when you attach

On a provider configured for `/v1/responses` the attach is refused at once, naming
the fix, instead of failing later inside the SDK — ADK's own client rejects an
inline picture outright. `DMCODE_API=chat` sends everything, including OpenAI, down
the chat wire instead.

### Deleting the path takes the picture with it

A picture that came from a path in the prompt belongs to that text: delete the path
and the preview goes, on the keystroke that removed the last character of it. A
picture from `/image` or the clipboard is not tied to the prompt and stays, because
it was attached on purpose.

The slow part of this was the delete. Decoding a file is asynchronous, so a read
started on a path can finish after the path is gone — the picture arrives onto a
prompt that no longer mentions it, which is the same bug as a preview that will
not be deleted, arriving afterwards instead of before. The read now carries the
path it was started for and the arrival is matched against what the prompt says.

### `/unimage` works in a prompt that already holds a file

`/unimage` is most often wanted exactly when a dropped path is sitting in the
prompt, and typing it there *appends* to the path, so a whole-line command match
never fired. That was worse than doing nothing: the line stopped being a command
and was sent to the model as a question about a file called `/unimage`, carrying
the very picture being removed.

It is now recognised as the last thing typed, and it takes the path with it —
otherwise the removal does not hold, because the prompt still names the file and
the send attaches the same picture again. A pasted picture has no path in the
prompt, so nothing is cut and your text is left alone.

### Four things that only showed up once something was measured

Each of these looked correct and was wrong, and none of them was found by reading
the code. They are here because the pattern is the point: every one is a case where
a *question* was answered with the wrong question.

- **`ctrl+v` was on a key the terminal never sends.** A terminal that binds `ctrl+v`
  converts the key to a paste and does not deliver the keystroke, so the picture
  branch was unreachable on most terminals — and a screenshot on the clipboard has
  no text to paste, so there was nothing to fall back to. The paste event now asks
  the clipboard too, and the keystroke route asks for the picture and falls back to
  the text, because it previously returned the picture command unconditionally and
  so did *nothing at all* when the clipboard held text.
- **A format the clipboard advertises is not a format it will render.** Windows
  synthesises clipboard formats from the ones it holds, so an application that
  lists `CF_DIBV5` but will not render it leaves the format "available" while
  `GetClipboardData` returns `ERROR_NOT_FOUND` — reported to the user as
  "Element not found" while a perfectly readable `CF_DIB` sat next to it. dmcode now
  takes the first format that actually *decodes*, which is the only thing that can
  tell a handle that is not a picture from one that is missing.
- **The picture bounds check forgot the header was in the same allocation.** It
  compared the pixel count against the handle's size, so a DIB lying about its
  dimensions by more than a header's worth of bytes passed and the decoder read
  memory the clipboard does not own — directly under a comment claiming every read
  was bounds-checked.
- **The preview never reset its colour.** Each cell re-states both of its colours,
  so the one that survived a row was the last cell's, and the caption underneath
  was drawn in the colour of the image's bottom-right pixel. Invisible on a picture
  whose edges happen to be dark, which is why it survived a careful look.

One more, about the tests rather than the code: the clipboard round-trip test
destroyed the user's clipboard on every `go test ./...` while its own comment
claimed an environment-variable gate that did not exist. It is gone. The decoder is
now tested through synthesised bytes — orientation, 24- and 32-bit, row padding,
bit-field masks, and nine malformed headers — and the format-selection policy is
tested through injected functions. Nothing in the suite touches the clipboard, and
a test that has to reach a global, shared, destructive resource to check a branch
is a test that will fail for someone else.

# dmCode v0.1.7

Three tools that did nothing, a yolo mode, and selection you can copy with.

## What's new since v0.1.6

### Two tools that never ran, and why

`ask_user` and `todo_write` compiled, passed their unit tests, and did nothing at
all: the transcript showed a tool call and a schema-validation message, and no
result. The cause was not their logic but their *generated argument schema*,
which `functiontool` derives from the Go type — so a field with no description
reaches the model as a bare `{"type":"string"}` and tells it nothing, and a field
is required unless its JSON tag says otherwise. `ask_user` had declared its options
as a nested object, so every shape a model naturally sent was refused;
`todo_write` marked a step's `status` required, so a plan written without it was
rejected whole.

Both are flat now, every field described, with the exact payloads a model was
observed to send driven through a real `runner.Run` turn by the tests.

### `shift+tab` runs without stopping to ask

Yolo mode is act's reach with `ask_user` withdrawn, so "the agent will not stop to
ask" is a property of what it can reach rather than a line in the instruction it is
asked to believe. It is that one key and nothing else: `tab` cannot reach it, no
command names it, and leaving it returns to the mode it came from.

### Selecting with the mouse

A drag selects in the transcript and releases into the clipboard, with a character
count in the status bar because a selection can be a word or three screens of build
output. `/mouse` turns it off, because with the mouse on the terminal's own
drag-select is gone.

# dmCode v0.1.6

The agent reaches the web, and to the tools you already have.

## What's new since v0.1.5

### `web_search`, without a key

DuckDuckGo's HTML endpoint, fetched directly. It is the one instrument that
deliberately reaches past the workspace boundary, and it is the reason the boundary
is stated as a boundary rather than a wall: everything else stays inside the
project, and this one does not.

### MCP servers

Config from `~/.dmcode/mcp.json` and from a `.mcp.json` in the workspace, in the
common `mcpServers` format — a stdio `command` or a `url`. One lazy toolset per
server, wired in so a dead server costs nothing until a turn actually needs it.
`/mcp` is the one eager pass: names for the sidebar, and a note for each server
that did not come up.

# dmCode v0.1.5

A conversation you can take back: undo the last message, keep several sessions and
switch between them, and let the agent plan, ask and delegate. Plus a model on
your own disk, an agent that stops repeating itself, and a turn that survives
the model running out of room.

> ### ⚠️ Unverified — work in progress
>
> **Everything in this section compiles and passes unit tests, but none of it has
> been verified end to end.** There are no live checks against a real endpoint,
> and no TUI session has been driven through a rewind, a session switch, a
> question overlay, a plan or a delegation. Test it, expect rough edges, and
> please report what breaks.
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
> a running `run_command` finishes on its own schedule. Session file permissions
> cannot be asserted on Windows, so that test skips there.

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

## What's new since v0.1.4

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

### A model on your own disk

Point dmcode at a `.gguf` file and it starts [llama.cpp](https://github.com/ggml-org/llama.cpp)'s
`llama-server` itself, on a free loopback port, waits for the model to load and
talks to it over the ordinary OpenAI-compatible wire. No API key, no manual
server, no second terminal.

The file is picked in `/setup` - typed, or browsed on disk - or named in
`DMCODE_GGUF`. Everything the server needs goes in `DMCODE_LLAMA_ARGS`
(`--ctx-size 16384`, `--n-gpu-layers 99`, and so on; it is llama-server's own
command line, split on spaces). The binary is looked for in the project's
`llama/` folder, then `DMCODE_LLAMA_SERVER`, then `PATH`, and it is shut down
again when dmcode exits. A model that fails to load never blocks startup:
detection falls through to the usual providers.

A local model is also the one most likely to run out of room mid-sentence, so
this release is a good place to read the next item.

### A turn the model ran out of room for

A tool call the model could not finish used to end the whole turn with
`unexpected end of JSON input`, which reads as a bug in dmcode rather than as a
limit: the output cap had cut the call's arguments off part way through, the
call was discarded, and the work it was about to do simply did not happen.

Now the endpoint is asked the same question **once more**, with a note that the
call was too big and that nothing was applied, and - where the endpoint reports
its token usage - a budget of twice what the call had already used. A few rules
keep that from being a surprise:

- the re-ask only happens while nothing has been shown yet, so an answer is
  never printed twice;
- a request that carries a budget of its own is left alone, which keeps the
  tool-support probe a single fast request;
- if the second attempt is cut off too, you get the original truncation, not
  whatever the failed re-ask had to say.

The agent is also told to keep arguments small in the first place: one hunk per
`edit_file`, a new file written short and then extended, and no essay before a
call. A model that insists on one huge call still gets the error - that is the
honest answer, and the message now names the token count it hit.

### The agent stops repeating itself

Three separate faults let a single word send the model into an endless
`list_dir` - the same call, byte for byte, until the runner cut the turn off.
Any one of them alone would have been survivable.

- The session store handed the runner a snapshot taken at `Create`, while the
  runner reads the conversation back out of the object `AppendEvent` mutates.
  Every request therefore carried an empty conversation: the model never saw
  your prompt, never saw the result of the tool it had just called, and had no
  way to know it had already asked.
- Tool arguments were never put on the wire. `functiontool` fills in
  `ParametersJsonSchema` and leaves `Parameters` nil, and the converter read
  the nil one, so all eleven tools were advertised as `{"type":"object"}` - as
  taking no arguments at all.
- The optional arguments of `read_file`, `edit_file`, `list_dir`, `grep` and
  `run_command` were all marked required by the schema generator, so the model
  was asked to pass `limit_lines` and `recursive` on every call - a contract it
  cannot honour, because it does not know the defaults.

With the loop gone, the reason it could spin is gone too. `list_dir`, `grep`
and `glob` used to report truncation as a bare `... (truncated)` with no way to
ask for the rest, so the only move left was to repeat the call and get the same
truncation. All three now return one page at a time with `offset`, `returned`,
`total`, `next_offset` and `truncated`; `grep` grows `context_lines`, which is
what makes it an answer rather than a list of line numbers.

### Only the line that changed is redrawn

Streaming used to rebuild and re-wrap the whole transcript on every token. The
render cache is now per-line, index-aligned with the history, so a token
re-renders only the line whose text changed - and a test compares the cache
against a full rebuild, so it cannot quietly stop matching.

### Symlinks no longer walk out of the project

The workspace boundary was a lexical check on the path as written, which left a
soft spot: a symlink *inside* the tree could reach files outside it. `write_file`
has to be allowed to create files that do not exist yet, so nothing can be
resolved before it is touched - the tools that actually open something re-check
the path after dereferencing links, `write_file` checks the deepest existing
ancestor of the target directory, and `grep` skips symlinked entries instead of
reading through them.

### `/proxy` is a dialog

Setting a proxy used to drop the argument before using it, so `/proxy <url>`
tried to parse the word `/proxy` as part of the address and `/proxy off` fell
through to the agent as a chat message. It is now a dialog in the style of
`/setup`: the current state is in the title, the menu covers turning it on,
editing the bypass list, turning it off and testing the connection, and an
invalid address keeps the field on screen with the reason in the status bar.
The typed fast paths still work beside it.

`ctrl+l` clears the screen and `ctrl+n` starts a session, both through one
method - the palette's copy of that behaviour had already drifted, and three
implementations of one action is three chances to drift again.

### Configuration

| Variable | What it does |
|---|---|
| `DMCODE_SESSIONS_DIR` | where conversations are kept (default `~/.dmcode/sessions`) |
| `DMCODE_LLM_RETRIES` | attempts per endpoint before failing over (default 3) |
| `DMCODE_LLM_RETRY_MS` | first backoff pause, doubling after that (default 500) |
| `DMCODE_ASK_TIMEOUT` | seconds before a question picks its own answer (default 0 — off) |
| `DMCODE_GGUF` | a `.gguf` model to run through a self-started `llama-server` |
| `DMCODE_LLAMA_ARGS` | that server's own flags, e.g. `--ctx-size 16384` (default none) |
| `DMCODE_LLAMA_SERVER` | where `llama-server` is, if not in `llama/` or on `PATH` |
| `DMCODE_GGUF_STARTUP` | seconds to wait for a model to load (default 180) |

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
