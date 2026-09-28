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

- No cancellation yet: `Esc` interrupts a turn, but a running shell command is
  not cancellable.
- Switching models starts a new session, so conversation history is dropped.
- The transcript is re-rendered on every streamed token, so a long session can
  lag. The fix is an incremental per-line cache.
- The workspace boundary is a lexical check. A symlink *inside* the directory can
  still reach outside it, because `write_file` must be allowed to create files
  that do not exist yet and cannot be resolved first.
- The change tally counts what passed through the tools. A file changed by
  `run_command` is not counted, and neither is a change made outside the session.

See [ROADMAP.md](https://github.com/dedomorozoff/dmcode/blob/main/ROADMAP.md)
for what is planned next.
