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

## Keys

| | |
|---|---|
| `ctrl+p` | command palette |
| `ctrl+b` | toggle the sidebar |
| `ctrl+y` | copy the reply |
| `esc` | stop the current turn |
| `↑` `↓` | prompt history |
| `pgup` `pgdn` | scroll |

Commands: `/setup` `/models` `/tools` `/history` `/lang` `/new` `/clear` `/copy` `/sidebar` `/help` `/quit`

## Build from source

```bash
git clone https://github.com/dedomorozoff/dmcode
cd dmcode
make build              # -> dist/dmcode
make test               # unit tests
make vet                # go vet
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

