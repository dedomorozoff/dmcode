  ```
  ███████╗ ███╗   ███╗ ██████╗ ██████╗ ███████╗ ██████╗
  ██╔═══██║████╗ ████║██╔════╝██╔═══██╗██╔═══██╗██╔═══╝
  ██║   ██║██╔████╔██║██║     ██║   ██║██║   ██║█████╗
  ██║   ██║██║╚██╔╝██║██║     ██║   ██║██║   ██║██╔══╝
  ███████╔╝██║ ╚═╝ ██║╚██████╗╚██████╔╝███████╔╝██████╗
  ╚═════╝  ╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝
```
**A coding agent for your terminal.**

![GitHub Release](https://img.shields.io/github/v/release/dedomorozoff/dmcode)
[![Go Version](https://img.shields.io/github/go-mod/go-version/dedomorozoff/dmcode)](https://github.com/dedomorozoff/dmcode)
[![License](https://img.shields.io/github/license/dedomorozoff/dmcode)](https://github.com/dedomorozoff/dmcode/blob/main/LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/dedomorozoff/dmcode)](https://github.com/dedomorozoff/dmcode)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/dedomorozoff/dmcode/total)

Ask it something. It reads your files, edits them, runs your tests, searches the
web and talks to your MCP servers.

---

## Install

**macOS / Linux / BSD:**

```bash
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
```

Both scripts check the download against the release's `sha256sums.txt`.

**From source** — needs Go 1.26+:

```bash
go install github.com/dedomorozoff/dmcode@latest
```

Then just run it:

```bash
dmcode
```

## The provider finds itself

No key required. On startup dmCode looks for an endpoint that actually answers,
and fails over to another one if the current provider dies mid-session.

| What it finds | When |
|---|---|
| **Ollama, LM Studio, llama.cpp, vLLM, Jan** | running locally — probed first |
| **Pollinations** | an anonymous OpenAI-compatible API, no key at all |
| **Groq, OpenRouter, OpenCode Zen, Mistral, GitHub Models, Cerebras, NVIDIA NIM, SambaNova, Hugging Face** | the key is already in `.env` or the environment |

If nothing answers, the `/setup` wizard runs: pick a provider, paste the key,
and it lands in `.env`. The free tiers that need no card are all in that list —
Cerebras (`CEREBRAS_API_KEY`), NVIDIA NIM (`NVIDIA_API_KEY`), SambaNova
(`SAMBANOVA_API_KEY`) and Hugging Face (`HF_TOKEN`) all hand out working keys
on signup, and any of them is picked up automatically once the variable is set.
Some options need no key at all; others are your own endpoints (Unsloth,
LM Studio, vLLM — anything speaking the OpenAI-compatible API).

A custom endpoint is three lines in `.env`:

```bash
OPENAI_BASE_URL=https://api.groq.com/openai/v1
OPENAI_API_KEY=gsk_...
DMCODE_MODEL=qwen/qwen3-32b
```

Worth knowing: `DMCODE_API=chat` forces the `/chat/completions` wire, and
`DMCODE_REASONING_EFFORT=low` caps the reasoning channel.

### Running a GGUF model

dmCode can start llama.cpp's `llama-server` itself, on any `.gguf` file you
already have. Pick **Local GGUF** in `/setup` and either type the path or press
`ctrl+o` to browse the disk for the file, or put it in `.env` by hand:

```bash
DMCODE_GGUF=C:\models\qwen2.5-coder-7b-q4_k_m.gguf
DMCODE_LLAMA_SERVER=llama-server            # or the full path to the binary
DMCODE_LLAMA_ARGS=--ctx-size 16384 -ngl 99  # llama-server flags, passed as-is
```

The binary is looked up in this order: `DMCODE_LLAMA_SERVER`, then
`./llama/llama-server(.exe)` — unpack a llama.cpp release into the project's
`llama/` folder and it is found with no configuration — then `PATH`.

The server is started on a free loopback port, dmCode waits for the model to
load (up to `DMCODE_GGUF_STARTUP` seconds, 180 by default) and then talks to it
over the ordinary OpenAI-compatible wire; the server's own output goes to
`~/.dmcode/llama-server.log`, and the process is taken down when dmCode exits.

## Tools

The agent works on files and a shell, rather than just talking:

`read_file` · `write_file` · `edit_file` · `list_dir` · `grep` · `glob` · `run_command`

It can also reach past the workspace on purpose:

- `web_search` — a web search over DuckDuckGo's HTML, no key needed, for when
  the answer is in a changelog or a man page rather than in the repository.
- MCP servers — see the next section.

On top of those, five that change how it works rather than what it touches —
see [the plan, questions and sub-agents](#the-plan-questions-and-sub-agents):

`todo_write` · `todo_set` · `todo_read` · `ask_user` · `sub_agent`

and, in plan mode only, `switch_mode`.

## MCP servers

dmCode speaks the Model Context Protocol. Servers are configured in the common
`mcpServers` format — the same block every MCP client uses — in
`~/.dmcode/mcp.json` (all sessions) or `.mcp.json` in the workspace (this
project only). A server is either a stdio child process:

```json
{
  "mcpServers": {
    "filesystem": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]}
  }
}
```

or a remote one behind a URL:

```json
{
  "mcpServers": {
    "remote": {"url": "https://example.com/mcp"}
  }
}
```

Servers come up lazily: a dead one costs nothing until a turn actually needs
it, and its tools simply do not appear. `/tools` lists what is reachable, and
the sidebar notes the servers that did not come up.

## Interface language

English by default. Run `/lang` to switch to Russian; the choice is saved to
`~/.dmcode/settings.json` and restored on the next start. `DMCODE_LANG=ru`
overrides it for a single run.

## Working directory

dmcode works in the directory it was started in. To point it somewhere else
without changing directory first:

```bash
dmcode -C ~/projects/myapp     # or: --dir ~/projects/myapp
```

`/cd <path>` moves the session at runtime, and bare `/cd` prints where you are.
The sidebar shows the full path, trimmed from the left.

Every tool is confined to that directory: a path that resolves outside it is
refused and the error names both directories. `run_command`'s `work_dir` is
checked the same way. The tools that open a file re-check the path after
following symlinks, so a symlink planted inside the tree cannot reach out of it.

## Coming back to a session

A session is a file in `~/.dmcode/sessions`, and dmcode prints the command to
reopen it when it exits:

```
dmcode: session sess-42 saved — resume it with:  dmcode -s sess-42
```

That line lands after the TUI is gone, so it is the one thing that survives the
window closing. `-s` on its own resumes the newest session, which is what you want
most of the time — "where I was" rather than a session you can name:

```bash
dmcode -s                # the most recent session
dmcode -s sess-42       # that one
dmcode -s -C ~/myapp    # resume and work somewhere else
```

The conversation comes back whole: the tail is printed before the first frame, so
the session does not open empty beside a model that remembers everything. The tool
calls, the results and every change block come back with it, drawn by the same code
that drew them the first time — so a restored session looks like the session you
left rather than a summary of it.

## Markdown in replies

Replies are laid out as markdown, not re-wrapped as plain prose:

- `#`–`######` headings, `**bold**`, `*italic*` and `` `code` `` are styled and
  the markers consumed. Emphasis carries colour as well as weight, so it stays
  visible on terminals whose font has no separate bold face.
- Fenced code blocks keep their indentation; an over-long line is cut rather
  than wrapped, since a wrapped code line reads as a different line of code.
- Pipe tables become aligned columns, and a table too wide for the panel sheds
  its borders and shrinks instead of breaking the frame.
- `>` blockquotes become indented asides, and `---` becomes a rule.

Prose without markup passes through unchanged. Tool output and your own prompts
are never treated as markdown, so a JSON payload containing `**` survives intact.

## Sending a picture

Attach an image and you see it before you send it — a small coloured preview
drawn in the transcript out of half-block glyphs, two pixels to a cell, with the
file name, the dimensions and the size underneath:

```
[image] screenshot.png · 1920×1080 · 214 KB
```

Three ways in, and they all end at the same place:

- `/image <path>` attaches one without sending anything, so you can look at the
  preview and decide. It appears once, as a strip above the input while it waits,
  and moves into the transcript when it is sent — the same rendered art either
  time, so what you approved and what went are the same picture. A path with spaces
  works quoted (`/image "…/my shot.png"`), the same form a dropped path arrives in.
  Type `/im` and press `enter` and the command is *completed* into the prompt with
  room for the path, rather than run — a command that needs an argument has nowhere
  to put one otherwise.
- **Drag and drop.** A terminal cannot hand over a file — dropping one inserts its
  *path* as text — so a picture path in the prompt is taken out of the prompt and
  attached. The model reads your question without the filename in it. A quoted path
  with spaces works; a path that is not a picture stays exactly where it is. The
  preview appears the moment the path is complete, not when you press enter.
- `ctrl+v` pastes the clipboard's picture when it holds one, and falls through to
  the usual text paste when it does not. Windows only: a screenshot there is a DIB
  and the rest of the platforms would each need their own protocol.

### Taking one back

A picture that came from a path in the prompt belongs to that text, so deleting the
path takes the preview with it — on the keystroke that removes the last character
of it, not on the next one. A picture from `/image` or the clipboard is not tied to
the prompt and stays put, because it was attached on purpose.

`/unimage` drops the last attachment whatever the prompt holds, and it works
mid-sentence as long as it is the last thing typed. It also removes the dropped
path, because a removal that leaves the path behind is not one: the prompt still
names the file, so sending would attach the same picture again.

PNG, JPEG and GIF are read. An image longer than 1568 pixels is reduced before it
goes anywhere, because that is the size the vision APIs treat as the point of
diminishing returns and a 4K screenshot is several megabytes of base64 in every
subsequent request of the conversation. Over 4 MB after that is refused with a
message rather than sent: it would not fit the session store's per-line limit, and
the store drops a line it cannot read — a lost turn with nothing on screen to say
so.

Pictures need the **chat wire** (`/chat/completions`). On a provider configured for
`/v1/responses` dmcode says so at the moment you attach, naming the fix, rather
than failing later inside the SDK — ADK's own client rejects an inline picture
outright. `DMCODE_API=chat` sends everything, including OpenAI, down the chat wire
instead.

Rewinding a turn brings its pictures back with the prompt, and `/new` and a session
switch leave them behind — they belong to the conversation that was going to carry
them.

## Plan, act and yolo modes

`tab` switches between act and plan. `shift+tab` turns yolo mode on and off —
that key is the only way in, deliberately (see below). Typing `/` opens a command
dialog just above the input; `↑` `↓` move through it, `enter` runs the
highlighted command, `esc` closes it and keeps what you typed. The dialog is
drawn over the transcript, so nothing in the layout shifts and the sidebar stays
readable.

- **ACT** — the full tool set; the agent reads and writes.
- **PLAN** — no write tools. The agent investigates and returns a plan without
  changing anything.
- **YOLO** — act's reach, with `ask_user` taken away. The agent works to the end
  of the task in one turn and never stops to ask.

Plan mode is enforced by withholding the write tools, not by asking the model in
the prompt. `run_command` is withheld too, because a shell can write a file
through `>` or `Out-File`. What is left is `read_file`, `list_dir`, `grep`, `glob`,
`web_search`, the plan tools, `ask_user`, `sub_agent` and `switch_mode` — a plan
is a document and a question is a question, so a mode whose whole output is a
plan should not be unable to publish one. The current mode is shown as a badge
in the status bar and the sidebar lists only the tools actually reachable.
A switch is refused while a turn is running; the conversation is kept across a
switch.

The agent can also ask to leave plan mode by itself, through `switch_mode`, once
it has a plan it is confident in. The switch lands at the end of that turn — the
runner cannot be rebuilt underneath a running one — and the conversation carries
over. It cannot go the other way: returning to plan mode is `Tab`, and that one
is yours.

### Why yolo is a key and not a command

Yolo mode is the only mode whose cost is paid *while it runs* rather than when it
is entered. Nothing about the request changes when you turn it on; what changes
is that the agent will not come back and ask, so a turn that needed a decision
now has to make it alone. Two things follow from that.

It is bound to `shift+tab` and to nothing else — no `/mode yolo`, and the agent
cannot ask for it either. A mode whose whole point is to remove interruptions
should not be reachable by accident, and should not be something a plan-mode
model can hand you on its own initiative. `/mode yolo` says which key it is
rather than reporting a bad spelling, because the likeliest way to meet this mode
is to have heard of it and guessed the command.

Leaving yolo returns to whichever of act and plan you were in, so the key does
not quietly undo a plan-mode choice you made on purpose.

The badge is the point. Yolo is the one badge drawn in the warning colour, and it
stays on screen for the whole turn, because the user has to be able to see that
the agent is not going to ask before they stop watching.

## Keys

| | |
|---|---|
| `ctrl+e` | the editor — or back to the chat; the `▣` icon at the head of the status bar does the same |
| `ctrl+p` | command palette |
| `ctrl+b` | toggle the sidebar |
| `ctrl+y` | copy the reply |
| `ctrl+z` | undo the last message (rewind) |
| `alt+g` | open the next change the agent made, in the editor (repeat to walk them) |
| `ctrl+q` | quit dmcode (in the editor it returns to the chat first) |
| `tab` | plan / act mode |
| `shift+tab` | yolo mode on / off (act and plan keep their tools) |
| `esc` | close the command list, or stop the current turn |
| `↑` `↓` | prompt history, or the command list while `/` is typed |
| `pgup` `pgdn` | scroll |
| wheel | scroll (`/mouse` turns it off, restoring drag-select) |
| drag | select anything on screen; the selection is copied on release |
| click | open the file at the line of the change you clicked |

Inside the editor, `F1` lists its own keys — a project tree, a git panel, a real
PTY, LSP completions, splits and bookmarks, which is too much for a table here.

## Going to a change

When a write lands, the transcript draws what changed: the file's name, the real
line numbers in the gutter, and the code itself. **Click any line of that block
and the editor opens the file there**, cursor on the row you pointed at. The same
goes for a line the write removed — it opens the line that replaced it, which is
the one a reader is looking for.

`alt+g` (or `/changes`) does the same without aiming: it opens the next change
below where you are, and pressing it again steps through the rest of the
session's edits and comes back round to the first.

A click that is not on a change row still does nothing, which is the rule that
keeps the selection below usable: without it, every click in the transcript would
be a jump.

## The editor

`ctrl+e` opens a code editor in the same workspace, and pressing it again puts
the chat back (`ctrl+q` does the same). The project tree waits for `ctrl+b`
rather than opening itself.
It is a real editor, not a viewer: syntax highlighting, a project tree, a git
panel with inline diffs and blame, a terminal, fuzzy file finding, splits,
bookmarks, multi-cursor editing (`alt+d`, `alt+click`), LSP completion and
go-to-definition.

The two modes share one screen rather than one window per thing. The transcript
stays in the main area when the editor is closed, the panel toggles stay live in
both, and the status bar at the bottom is the same row in each — in chat mode it
carries the icons and the git branch, and in the editor mode it adds the file's
own state (`Ln`, `Col`, encoding, language).

With no file open the editor says so rather than showing an empty tab: no tab in
the bar, no line numbers down the side, and one line naming the keys that fill it
(`ctrl+o` finds a file, `ctrl+p` has a new-file command). The editor always has a
buffer behind that, so typing works straight away — the tab appears the moment
there is something in it, under `[untitled]` until you name it with `ctrl+s`.

The four icons at the left of that row are the workspace: `▣` editor, `▤` project
tree, `⎇` git, `❯` terminal. Each is clickable, each shows its label on hover,
and the first one is the mode toggle.

## Selecting with the mouse

Drag with the left button over anything on screen — a reply, an error, your own
prompt, the status bar — and the selection is copied to the clipboard the moment
you let go. The status bar says how many characters went in, because a selection
can be one word or three screens of build output and "copied" alone does not say
which happened.

The text that lands on the clipboard is taken from the rendered frame rather than
from the transcript, so it is what you actually pointed at: the wrapping is the
wrapping on screen, and the escape sequences the renderer used are stripped. A
selection works backwards as well as forwards, and a drag that runs past the end
of a row takes what is there.

A plain click copies nothing. Without that rule every click in the transcript
would overwrite the clipboard with a single character and the feature would be
worse than useless. The one thing a click *does* do is open a change in the
editor — see [Going to a change](#going-to-a-change).

`/mouse` turns the whole thing off and hands the terminal back, which is what you
want if you prefer the terminal's own drag-select and paste menu — dmcode cannot
do both at once, because reporting mouse motion is what stops the terminal from
doing it itself.

The status bar carries the mode and the state, and nothing else — the model is in
the header and the sidebar, and the keys are in `/help`.

Commands: `/setup` `/models` `/tools` `/history` `/lang` `/mode` `/cd` `/mouse` `/proxy` `/new` `/sessions` `/resume` `/rewind` `/todo` `/changes` `/clear` `/copy` `/sidebar` `/debug` `/help` `/quit`

## Commands

Type `/` and the list opens with everything in it. Type a letter or two and it
narrows by matching those letters *in order* anywhere in a command's name, not
only at the front — so `/p` keeps `/proxy`, `/copy`, `/help` and `/setup` on
screen instead of collapsing to the one command that starts with `p`, and `/se`
finds `/sessions` whether or not you finished typing it.

The order is the answer to "which one did I mean": a command that *is* what you
typed comes first, then those that start with it, then the rest by how close
together the letters you typed appear in the name. The row under the highlight is
what a single `enter` takes, so it is the one most likely meant — `/md` gives
`/mode` rather than `/models`, `/c` gives `/cd` rather than `/changes`.

`/debug` used to work without appearing anywhere: it was handled by the
dispatcher, named in this file, and in neither the `/` list nor `ctrl+p`. The
list of commands and the set of commands being two different sets is how a
command hides.

## HTTP proxy

`HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are honoured, so a proxy already
configured for the rest of the system works with no setup. `/proxy` shows the
setting and changes it:

```
/proxy                          show what is in effect, and whether it works
/proxy 127.0.0.1:3128           set one (a bare host:port is fine)
/proxy http://user:pass@host:8080
/proxy off                      clear it
/proxy no localhost,127.0.0.1   bypass list, without touching the proxy
```

The change applies to the running session — the next request goes through it —
and is written to `.env`, so it survives a restart. `/proxy off` removes the
variables from both, rather than leaving them in the file to be read back.

`localhost` and the loopback addresses are never proxied: a proxy that cannot
reach the machine's own services is common enough that following it would break
local providers that otherwise work.

## Sessions and rewinding

Conversations are kept in `~/.dmcode/sessions`, one JSONL file each, and survive
a restart. Set `DMCODE_SESSIONS_DIR` to keep them beside a project instead.

- `ctrl+z` (or `/rewind`) undoes the last turn: the prompt comes back to the
  input for editing, the transcript loses the exchange, and the model's memory
  is cut at the same place — so the next reply cannot be to a question the user
  can no longer see. Press it again to go back further.
- `/new [name]` starts a fresh conversation. The previous one is **kept**, not
  discarded, so a `/new` pressed by mistake costs one keypress.
- `/sessions` lists what is there, searchable by typing; `enter` switches,
  `d` deletes (twice — a deleted session cannot be brought back), `esc` closes.
  `/resume <id>` opens one directly.

A failed request is retried on the same endpoint before the pool moves on: three
attempts by default, with a growing pause, so a rate limit no longer ends a turn.
Set `DMCODE_LLM_RETRIES` and `DMCODE_LLM_RETRY_MS` to change that. A request the
provider rejected is not retried, and neither is one that already streamed part
of an answer. A tool call the model could not fit into its output limit is
re-asked once with a note and a bigger budget, instead of ending the turn with a
JSON error.

## The plan, questions and sub-agents

- `todo_write` / `todo_set` / `todo_read` are the agent's plan. It publishes it
  before it starts and moves each step along as it works; `/todo` prints it and
  the sidebar shows how far along it is. `/todo clear` empties it.
- `ask_user` puts a decision to you. The agent gives two to five concrete
  options and marks the one it recommends; you pick with `↑` `↓` and `enter`,
  tick several with `space` when it is a multi question, or type your own with
  `c`. `esc` skips the question and the agent carries on with its best guess.
  The wait is **off by default** — set `DMCODE_ASK_TIMEOUT=60` to have the
  recommended option chosen for you after a minute, with a countdown on screen.
- `sub_agent` delegates one research task to a separate agent with its own
  context: surveying how something works across many files, tracing a call path,
  listing call sites. It cannot write anything and cannot ask you anything, and
  its session is throwaway — its reading does not end up in your conversation.

In plan mode the agent can call `switch_mode` to move the session to act mode on
its own, once it has a plan it is confident in. The switch happens at the end of
that turn. It cannot go back to plan mode itself — that is `Tab`, and it is
yours.

## Known limitations

Stated plainly, not hidden: a rewind does not rewrite `~/.dmcode/history.jsonl`,
so an undone prompt is still offered by `/history` until the next start.
Cancellation reaches the context but not the process, so a running `run_command`
finishes on its own schedule. The workspace boundary is a lexical check at
resolve time; the tools that open files close the symlink gap themselves, but
`grep` skipping symlinks means it will not follow one to a file outside the
tree. Pictures reach only providers on the `/chat/completions` wire, and
`ctrl+v` reads the clipboard's image on Windows alone — on Linux and macOS it
pastes text, as it always did. A pasted DIB is read at 24 or 32 bits; a
palettised or compressed one is refused with a message saying so rather than
guessed at.

Attaching and previewing a picture is confirmed working end to end on Windows.
**Sending one to a model is not yet confirmed** — no picture has been through a
real vision endpoint, so whether the `image_url` data URL dmcode puts on the wire
is one a live endpoint accepts, and whether the model then answers about the
picture, are both untested. Please report what breaks.

## Troubleshooting

`/debug` prints the line kind, the markdown flag and the width of the last
transcript lines. A reply rendering as raw markup is a routing problem — the
text is stored under a kind that is not treated as markdown — rather than a
parsing problem, and one line of output tells the two apart.

If output does not change after a rebuild, a previously built `dmcode` is still
running: overwriting `dist/dmcode.exe` fails with "being used by another
process" in that case. Close the old session and start the new binary.

## Build from source

```bash
git clone https://github.com/dedomorozoff/dmcode
cd dmcode
make install       # build and install into GOBIN (or GOPATH/bin)
dmcode
```

`make install` defaults to `$(go env GOBIN)`, falling back to `GOPATH/bin` —
both writable without root, and both present on Windows. For a system-wide
install pass a prefix:

```bash
sudo make install PREFIX=/usr/local   # -> /usr/local/bin/dmcode
make uninstall                        # remove it again
```

Other targets:

```bash
make build              # -> dist/dmcode
make test               # unit tests
make test-race          # unit tests with the race detector
make vet                # go vet
make clean              # remove dist/
```

Cross-compile with `make build-linux-amd64`, `build-darwin-arm64`,
`build-windows-amd64`, or any other GOOS-GOARCH pair. Packages: `make deb`,
`make rpm`, `make pkg`.

`v*` tags build releases through GitHub Actions: binaries for eight platforms,
plus `.deb`, `.rpm`, an Arch package and a Windows zip.

## Layout

```
main.go              chains the packages together, nothing else
internal/agent       the agent, its instructions, sub-agents, the mode switch
internal/ask         the ask_user broker and its timer
internal/clipimg     reads a picture off the system clipboard (Windows)
internal/config      .env, endpoints, the setup wizard
internal/discover    finds the providers that actually answer
internal/editor      the workspace editor: tree, git, terminal, splits, LSP
internal/i18n        English source strings and the Russian catalog
internal/imgprev     draws an image as coloured half-blocks, reduces it for the wire
internal/llm         OpenAI-compatible wire, failover, retries
internal/mcp         external MCP servers and their config
internal/memsession  the session store and its JSONL persistence
internal/todo        the agent's plan
internal/tools       the workspace tools and the boundary they are confined to
internal/ui          the Bubble Tea terminal interface
```

## Configuration

Everything is optional; the defaults work without any of it.

| Variable | What it does |
|---|---|
| `DMCODE_SESSIONS_DIR` | where conversations are kept (default `~/.dmcode/sessions`) |
| `DMCODE_LLM_RETRIES` | attempts per endpoint before failing over (default 3) |
| `DMCODE_LLM_RETRY_MS` | first backoff pause, doubling after that (default 500) |
| `DMCODE_ASK_TIMEOUT` | seconds before a question picks its own recommended answer (**default 0 — off**) |
| `DMCODE_LANG` | `en` or `ru` for one run |
| `DMCODE_API` | `chat` to force the `/chat/completions` wire |
| `DMCODE_REASONING_EFFORT` | caps the reasoning channel |

## Roadmap

Plans are in [ROADMAP.md](ROADMAP.md): permissions for dangerous commands,
context compaction, LSP.

## License

[MIT](LICENSE) © Dedo Morozoff
