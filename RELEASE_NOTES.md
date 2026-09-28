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

- No cancellation yet: `Esc` interrupts a turn, but a running shell command is
  not cancellable.
- Switching models starts a new session, so conversation history is dropped.
- The transcript is re-rendered on every streamed token, so a long session can
  lag. The fix is an incremental per-line cache.
- The workspace boundary is a lexical check. A symlink *inside* the directory can
  still reach outside it, because `write_file` must be allowed to create files
  that do not exist yet and cannot be resolved first.
- `tab` is overloaded: it completes a suggestion inside a `/` command and
  switches mode everywhere else.

See [ROADMAP.md](https://github.com/dedomorozoff/dmcode/blob/main/ROADMAP.md)
for what is planned next.
