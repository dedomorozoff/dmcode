package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// The embedded editor's frame is the one thing in this package that depends on
// the machine it is drawn on, and ensureEditor applies the terminal's
// capabilities on every session (ui.go). Two of them decide what comes out:
//
//   - The glyph set follows DetectCompat, which falls back to ASCII when TERM is
//     unset or dumb. A CI shell has neither, so the split icons arrive as V and
//     H where the Unicode set draws boxes, and a test looking for the box is
//     really asking about the runner's environment.
//   - A Go file needs gopls. Without it the editor swaps the status bar's Ln/Col
//     segment for an install hint, so a jump test that reads Ln to check where
//     the cursor landed finds nothing on a machine that has no language server
//     installed - and finds it on the developer's.
//
// Both fallbacks are correct product behaviour and both are covered in the editor
// package, by DetectCompat's own cases and by the glyph table's. What is left for
// this package is the host's half: that a click in the transcript lands on the
// right line, and that the editor's chrome is on screen when the workspace is.
func init() {
	// Same shape as editor_test.go's pin for DMCODE_LANG: the whole package
	// asserts one look, so it is set once for the binary rather than per test.
	_ = os.Setenv("TERM", "xterm-256color")
}

// pinEditorFrame puts the config that silences the LSP install hint into dir, so
// the status bar keeps its Ln/Col segment whether or not this machine has a
// language server. The editor loads it from its own root, which the host sets to
// the workspace directory.
func pinEditorFrame(t *testing.T, dir string) {
	t.Helper()
	conf := "[lsp]\nenabled = false\n"
	if err := os.WriteFile(filepath.Join(dir, ".dmcode.conf"), []byte(conf), 0o644); err != nil {
		t.Fatalf("pin the editor frame: %v", err)
	}
}

// editorWorkDir is the workspace for a test that opens the embedded editor: a
// fresh directory already carrying that config.
func editorWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pinEditorFrame(t, dir)
	return dir
}
