<div align="center">

# dmCode

**A coding agent for your terminal.**

Ask it something. It reads your files, edits them and runs your tests.
Built on [google/adk-go](https://github.com/google/adk-go) and [Bubble Tea](https://github.com/charmbracelet/bubbletea).

One static binary. No API key required.

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
- **PLAN** — `read_file`, `list_dir`, `grep` and `glob` only. The agent
  investigates and returns a plan without changing anything.

Plan mode is enforced by withholding the write tools, not by asking the model
in the prompt. `run_command` is excluded too, because a shell can write a file
through `>` or `Out-File`. The current mode is shown as a badge in the status
bar and the sidebar lists only the tools actually reachable. A switch is refused
while a turn is running; the conversation is kept across a switch.

## Keys

| | |
|---|---|
| `ctrl+p` | command palette |
| `ctrl+b` | toggle the sidebar |
| `ctrl+y` | copy the reply |
| `tab` | plan / act mode |
| `esc` | stop the current turn |
| `↑` `↓` | prompt history |
| `pgup` `pgdn` | scroll |
| wheel | scroll (`/mouse` turns it off, restoring drag-select) |

Commands: `/setup` `/models` `/tools` `/history` `/lang` `/mode` `/cd` `/mouse` `/new` `/clear` `/copy` `/sidebar` `/debug` `/help` `/quit`

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
internal/agent       builds the agent and its system prompt
internal/config      .env, endpoints, the setup wizard
internal/discover    finds the providers that actually answer
internal/i18n        English source strings and the Russian catalog
internal/llm         OpenAI-compatible wire, failover between endpoints
internal/tools       the agent's tools
internal/ui          the Bubble Tea terminal interface
```

## Roadmap

Plans are in [ROADMAP.md](ROADMAP.md): turn cancellation, permissions for
dangerous commands, session persistence, MCP and LSP.

