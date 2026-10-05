// Package pathnorm gives one file exactly one spelling.
//
// A path is not an identifier. The same file answers to several at once: /var
// and /private/var on macOS, C:\Users\RUNNER~1 and C:\Users\runneradmin on
// Windows, a symlinked checkout and the directory it points at. Code that
// compares, keys or bounds-checks a path has to put both sides into the same
// spelling first, or it answers a question nobody asked: the workspace check
// decides a file the user just created is outside the workspace, the editor
// opens the same file in a second tab, and go-git is handed a path that walks
// out of the repository with ../../.. and refuses it.
//
// The failures are invisible on a developer machine, where the temp directory,
// the checkout and the home directory are all spelled the one way the process
// already spells them. They appear the moment the two spellings come from
// different places — t.TempDir against os.Getwd, %TEMP% against an expanded
// short name, a runner's TMPDIR against the repository it walks up into.
package pathnorm

import (
	"os"
	"path/filepath"
)

// Canonical returns p in the spelling this machine agrees on: links followed,
// and on Windows the 8.3 short name and the letter case corrected.
//
// A path that does not exist has no links to follow, so the deepest ancestor
// that does is resolved and the missing tail appended unchanged. That is what
// makes the answer usable for the case that matters most here — a file about to
// be created, which every write-style tool is handed on every call.
//
// A path nothing can be resolved for is returned as it arrived. A canonical form
// that is wrong is worse than an unresolved one, because it looks authoritative.
func Canonical(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	base := existingAncestor(filepath.Dir(p))
	if base == "" {
		return p
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return filepath.Join(resolved, rel)
}

// Same reports whether two paths name the same file, whichever way each of them
// is spelled. Two empty paths are the same: that is the standing empty buffer.
func Same(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	return Canonical(a) == Canonical(b)
}

// existingAncestor returns the deepest directory at or above dir that exists,
// or "" when even the volume root cannot be found.
func existingAncestor(dir string) string {
	for {
		if _, err := os.Lstat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
