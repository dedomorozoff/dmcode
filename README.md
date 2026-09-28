<div align="center">

# dmCode

**A coding agent for your terminal.**

Ask it something. It reads your files, edits them and runs your tests.
Built on [google/adk-go](https://github.com/google/adk-go) and [Bubble Tea](https://github.com/charmbracelet/bubbletea).

One static binary. No API key required.

> ### ⚠️ Unverified — work in progress
>
> **The features marked below as new are not verified end to end.** They compile
> and carry unit tests, but nobody has run a real agent through them yet: there
> are no live checks against a real endpoint, and no TUI session has been driven
> through `ctrl+z`, the session switcher, a question overlay, a plan or a
> delegation. Treat them as a branch under construction, expect rough edges, and
> test before you rely on them.
>
> Specifically **not yet tested**: that a rewind really cuts the model's memory
> and not just the screen; that a session written to disk is byte-for-byte what
> comes back after a restart; that the question timer and its overlay behave
> correctly against a live turn; that `switch_mode` lands between turns without
> stranding the runner; and that a sub-agent's report survives the trip back
> into the parent's context.
>
> Known gaps, stated plainly: `~/.dmcode/history.jsonl` is not rewritten by a
> rewind, so an undone prompt is still offered by `/history` until the next
> start. Cancellation reaches the context but not the process, so a running
> `run_command` finishes on its own schedule. The workspace boundary is a
> lexical check, and a symlink inside the directory can still reach outside it.
> File permissions on the session store cannot be asserted on Windows, so that
> test is skipped there.
>
> Please report what breaks.

</div>

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
| **Groq, OpenRouter, OpenCode Zen, Mistral, GitHub Models** | the key is already in `.env` or the environment |

If nothing answers, the `/setup` wizard runs: pick a provider, paste the key,
and it lands in `.env`. Some options need no key at all; others are your own
endpoints (Unsloth, LM Studio, vLLM — anything speaking the OpenAI-compatible
API).

A custom endpoint is three lines in `.env`:

```bash
OPENAI_BASE_URL=https://api.groq.com/openai/v1
OPENAI_API_KEY=gsk_...
DMCODE_MODEL=qwen/qwen3-32b
```

Worth knowing: `DMCODE_API=chat` forces the `/chat/completions` wire, and
`DMCODE_REASONING_EFFORT=low` caps the reasoning channel.

## Interface language

English by default. Run `/lang` to switch to Russian; the choice is saved to
`~/.dmcode/settings.json` and restored on the next start. `DMCODE_LANG=ru`
overrides it for a single run.

## Tools

The agent works on files and a shell, rather than just talking:

`read_file` · `write_file` · `edit_file` · `list_dir` · `grep` · `glob` · `run_command`

On top of those, five that change how it works rather than what it touches —
see [the plan, questions and sub-agents](#the-plan-questions-and-sub-agents):

`todo_write` · `todo_set` · `todo_read` · `ask_user` · `sub_agent`

and, in plan mode only, `switch_mode`.

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
checked the same way.

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

## Plan and act modes

`tab` switches between them (inside a `/` command, tab still completes
suggestions):

- **ACT** — the full tool set; the agent reads and writes.
- **PLAN** — no write tools. The agent investigates and returns a plan without
  changing anything.

Plan mode is enforced by withholding the write tools, not by asking the model in
the prompt. `run_command` is withheld too, because a shell can write a file
through `>` or `Out-File`. What is left is `read_file`, `list_dir`, `grep`, `glob`,
the plan tools, `ask_user`, `sub_agent` and `switch_mode` — a plan is a document
and a question is a question, so a mode whose whole output is a plan should not
be unable to publish one. The current mode is shown as a badge in the status bar
and the sidebar lists only the tools actually reachable. A switch is refused
while a turn is running; the conversation is kept across a switch.

The agent can also ask to leave plan mode by itself, through `switch_mode`, once
it has a plan it is confident in. The switch lands at the end of that turn — the
runner cannot be rebuilt underneath a running one — and the conversation carries
over. It cannot go the other way: returning to plan mode is `Tab`, and that one
is yours.

## Keys

| | |
|---|---|
| `ctrl+p` | command palette |
| `ctrl+b` | toggle the sidebar |
| `ctrl+y` | copy the reply |
| `ctrl+z` | undo the last message (rewind) |
| `tab` | plan / act mode |
| `esc` | stop the current turn |
| `↑` `↓` | prompt history |
| `pgup` `pgdn` | scroll |
| wheel | scroll (`/mouse` turns it off, restoring drag-select) |

Commands: `/setup` `/models` `/tools` `/history` `/lang` `/mode` `/cd` `/mouse` `/new` `/sessions` `/resume` `/rewind` `/todo` `/clear` `/copy` `/sidebar` `/debug` `/help` `/quit`

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
of an answer.

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
internal/config      .env, endpoints, the setup wizard
internal/discover    finds the providers that actually answer
internal/i18n        English source strings and the Russian catalog
internal/llm         OpenAI-compatible wire, failover, retries
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
context compaction, MCP and LSP.

