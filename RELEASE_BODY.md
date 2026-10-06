## dmCode v0.2.0 — the chat and an editor, in one window

`ctrl+e` opens the editor, `ctrl+q` puts it back. One window instead of two: the transcript fills the main area when the editor is closed, and the panels work in both modes.

What came across is the editor body — project tree, git panel with inline diffs and blame, a real PTY, splits, bookmarks, LSP. Its AI panel, ghost text and debug panel are gone along with the agent that had them: dmcode has its own.

### The status bar

Four clickable icons: editor, tree, git, terminal. The editor one is first, because it changes the whole screen rather than opening a panel, so that is where it belongs. It is also the only icon whose state is a mode rather than a panel's.

In chat mode the bar carries the icons and the git branch, and nothing else. `Ln`, `Col`, the encoding, the language tag and the F1 hint are gone: they described a file nobody can see on that screen, and they took the space next to the only part of that row a user can act on. In the editor mode all of it comes back — a file's own state belongs to the screen the file is open on.

### Also in this release

- **Pictures.** Drag a screenshot into the prompt, paste one from the clipboard (`ctrl+v`, Windows), or attach one with `/image`. The preview is drawn in colour before you send it. Reduction to 1568px happens before the drawing, so what goes on the wire is exactly what is on screen.
- **Rewind and sessions.** `ctrl+z` brings the last prompt back and takes the answer with it; `/sessions` switches between conversations.
- **Yolo mode** — `shift+tab`: the agent stops stopping to ask.
- **Mouse selection**, copied on release, with `/mouse` to turn it off.
- **Two tools that did nothing.** `ask_user` and `todo_write` compiled, passed their tests and never ran: the argument schema is generated from the Go type, so a nested object meant every shape a model sent was refused. Both are flat now, every field described.

### Fixes

- The chat frame was one row short with the terminal docked, so the status bar sat a character above the bottom edge.
- The tests wrote to your clipboard: every copy and cut test left its own text there. The clipboard is now swapped for the duration of a test, and a test fails if anything writes to it directly again.
- The `F1` help had rotted — a duplicated row, a pointer to a key that does not exist, and `F12`, `Ctrl+Space` and `Ctrl+/` missing. A test now holds it against the catalog.

### What is not verified

Stated plainly: the editor's keys and panels are covered by tests on frames and mouse handling but have not been driven for hours by a person, and a picture still has not been through a real vision endpoint.

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
```
```powershell
irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
```

Full notes: [RELEASE_NOTES.md](RELEASE_NOTES.md)
