# dmCode v0.1.1

First tagged release. Single static binary, no runtime dependencies, and no
API key required to get started.

## Install

```bash
# macOS / Linux / BSD
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
```

Or build from source (Go 1.26+):

```bash
go install github.com/dedomorozoff/dmcode@latest
```

## Highlights

- **Provider auto-discovery.** On startup dmCode finds an endpoint that
  actually answers: local servers (Ollama, LM Studio, llama.cpp, vLLM, Jan) are
  probed first, then keyless hosted endpoints, then anything already present in
  `.env`. If the active provider dies mid-session, the turn fails over to the
  next working one instead of erroring out.
- **Interactive `/setup`.** No configuration to write by hand. Pick a provider,
  paste the key, it lands in `.env`. Options with no key at all, plus custom
  OpenAI-compatible endpoints, are offered alongside the usual hosted ones.
- **Packaged layout.** The monolithic `main` package is now split into
  `internal/{agent,config,discover,llm,tools,ui}`.
- **New tools and tests.** `tools`, `history` rendering, provider verification
  and the config/discover packages gained their own test suites.
- **Reproducible release tooling.** `Makefile` with cross-compilation for the
  full GOOS/GOARCH matrix and targets for deb, rpm, Arch, termux and Windows zip.

## Assets

Binaries for `linux`, `darwin`, `freebsd`, `openbsd` and `netbsd` on `amd64`
and `arm64`, plus `windows-amd64` as both a raw `.exe` and a zip. Packages:
`.deb`, `.rpm`, Arch `.pkg.tar.zst`, and a termux tarball.

## Known limitations

- No cancellation yet: `Esc` interrupts a turn, but a running shell command is
  not cancellable.
- Switching models starts a new session, so conversation history is dropped.
- `edit_file` is a strict literal match and can fail on CRLF or whitespace
  drift; `glob` does not support `**`.

See [ROADMAP.md](https://github.com/dedomorozoff/dmcode/blob/main/ROADMAP.md)
for what is planned next.
