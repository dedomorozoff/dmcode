# dmCode v0.2.0 — commit and tag texts

Release `v0.2.0` → commit `6fc0a93`, on branch `main`.

Contents of this file:

- [Release commit — 6fc0a93](#release-commit--6fc0a93) — files: `PKGBUILD`, `RELEASE_NOTES.md`, `ROADMAP.md`
- [Tag message — v0.2.0](#tag-message--v020) — annotated, tagger: Loparev Alexander <dedomorozoff@gmail.com>
- [Previous commit — 459c5bc](#previous-commit--459c5bc) — files: the workspace/status-bar/help/clipboard work
- [Publishing](#publishing) — the two push commands and what CI does

---

## Release commit — 6fc0a93

```text
Release v0.2.0: the chat and an editor, in one window

The release notes for v0.2.0, which is everything since the last tag,
v0.1.7 — so it covers both the picture work and the editor, and the fixes
found while wiring them together.

There was never a v0.1.8 tag: the notes for it were written and the
version bumped in PKGBUILD, but the tag was never cut, so nothing was
published under that number. The v0.1.8 section stays where it is, as the
record of what was in that work, and v0.2.0 is what ships it plus the
workspace.

The headline is the editor joining the chat in one screen rather than one
window each, with ctrl+e and ctrl+q as the two directions and a status-bar
icon that does the same thing by mouse. What is left of the old editor is
its body — tree, git, terminal, splits, bookmarks, LSP — with its AI
panel, ghost text and debug panel left behind with the agent that had
them.

The notes state what is not proven, because two of the parts that took the
longest to get right are the parts with the thinnest evidence behind them:
the editor's keys and the terminal panel are asserted by tests on row
counts and hit-testing but have not been driven for hours by a person, and
a picture still has not been through a real vision endpoint. Both say so
at the top rather than being discovered by whoever tries them.

PKGBUILD moves to 0.2.0, and the roadmap's status block says the same
thing about the editor that the release notes do.
```

---

## Tag message — v0.2.0

Annotated tag, created with `git tag -a v0.2.0 -F tagmsg.txt`. Pushing this
tag is what triggers `.github/workflows/release.yml` — the workflow listens
on `push: tags: v*` and on nothing else.

```text
dmCode v0.2.0 — the chat and an editor, in one window

dmcode is a workspace now: the same window holds the agent you know and a
real code editor, and ctrl+e switches between them. What came across is
the editor body — project tree, git panel with inline diffs and blame, a
real PTY, splits, bookmarks, LSP completion and go-to-definition — with
its AI panel, ghost text and debug panel left behind with the agent that
had them.

The bottom row is the workspace's and carries four clickable icons, with
the editor toggle first because it is the one that changes what the whole
screen is. In chat mode it holds the icons and the git branch and nothing
else; the file's own state comes back in the editor mode, on the screen
that file is actually on.

This release also covers the picture work, the rewind and sessions, the
tools that used to do nothing at all, and the fixes found while wiring the
two halves together — including a chat frame that was one row short with
the terminal docked, and a test suite that was writing to the user's
clipboard.

Two things are stated plainly in the notes because they are the parts with
the thinnest evidence behind them: the editor's keys and panels are
asserted by tests on row counts and hit-testing but have not been driven
for hours by a person, and a picture has still not been through a real
vision endpoint.

Install:
  curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
  irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex

Full notes: RELEASE_NOTES.md
```

---

## Previous commit — 459c5bc

```text
The workspace gets an editor toggle in its status bar

The status bar's icon strip gained a fourth glyph at its head, the editor
one, and it switches the workspace between the chat and the editor in both
directions. It is first deliberately: every other icon opens a panel, and
this one changes what the whole screen is, so it belongs where the eye
lands before a row of toggles has been read. It lights up while the editor
is up, and it is the only icon whose state is a mode rather than a panel.

The mode is the host's field, so the icon does not flip anything itself: it
raises ToggleEditorMsg and internal/ui answers it, reading the current mode
off the same flag that decides what the main area renders. The editor also
keeps its own traffic (terminal output, file watches, LSP and git
transfers) flowing in chat mode, or the listener chains that keep the
panels alive would die as soon as the chat came back.

That trim made the rest of the row free, and this is what it was spent
on: Ln/Col, the encoding, the language tag and the F1 hint are gone from
the chat's status bar. They described a file nobody can see on that screen,
and they competed with the only part of that row a user can act on. The
editor mode keeps all of it; the git branch stays in both, being the
workspace's rather than the file's.

The help had drifted in the other direction. It listed a duplicate git
diff row, a tree-ops row pointing at help.tree_ops, a key that does not
exist and so rendered as the raw key, and it left out F12, Ctrl+Space and
Ctrl+/, all of which work. It also had no entry for the way back to the
chat, which is now first and therefore above the fold. The F1 title still
said dmed.

README grows an editor section and the badges stop pointing at the dmed
repo. TestHelpEntriesAreTranslatedAndUnique now holds the help list to the
catalog and to itself, which is how help.tree_ops was found.
```

---

## Publishing

```bash
git push origin main        # code to GitHub; nothing is built by this
git push origin v0.2.0      # the tag — this is what starts the release
```

or in one step:

```bash
git push origin main --follow-tags
```

The workflow cross-compiles for eight platforms (linux-amd64/arm64, freebsd,
openbsd, netbsd, darwin-amd64/arm64, windows-amd64), then builds deb, rpm,
termux, win-zip and Arch packages, then runs:

```bash
gh release create "$GITHUB_REF_NAME" "${assets[@]}" --title "$GITHUB_REF_NAME" --generate-notes
```

Two things worth knowing about that last step: the GitHub Release body is
generated from the commit list, not from `RELEASE_NOTES.md`, so the notes file
is not what users will read on the release page; and `gh release delete` runs
first, so re-pushing the same tag rebuilds and replaces the release rather
than failing.