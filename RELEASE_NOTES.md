# dmCode v0.1.2

The interface now speaks English by default, with Russian one keystroke away.
Also adds `make install`, and replaces the previous `v0.1.1` release.

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

## What's new since v0.1.1

- **English is the default interface.** The TUI, sidebar, status bar, setup
  wizard and provider labels are all English now. English strings are the
  source language, so an untranslated string falls back to readable English
  rather than rendering blank.
- **Russian via `/lang`.** A command in the palette switches the language
  immediately and saves the choice to `~/.dmcode/settings.json`, so the next
  start comes up in the same language. `DMCODE_LANG=ru` overrides it for a
  single run.
- **`make install` and `make uninstall`.** Install into `$(go env GOBIN)`,
  falling back to `GOPATH/bin` — both writable without root and both present on
  Windows. Pass `PREFIX=/usr/local` for a system-wide install.
- **README in English**, covering install, the tools, the hotkeys and the
  language setting.

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
