package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/pathnorm"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// dirPickModel builds a session model in a scratch directory that holds two
// folders to navigate and one file the browser must not offer.
func dirPickModel(t *testing.T) (*uiModel, string) {
	t.Helper()
	dir := inTempDir(t)
	for _, sub := range []string{"src", "docs", "src/nested"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dmtools.SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	m := modeModel(t)
	m.workDir = dmtools.Root()
	return m, dir
}

// TestBareCDOpensTheFolderBrowser drives the real dispatch path: typing /cd and
// pressing enter must open the browser at the session's current folder, list
// its subdirectories, and never offer a file — the destination of /cd is always
// a directory.
func TestBareCDOpensTheFolderBrowser(t *testing.T) {
	m, _ := dirPickModel(t)

	setupKeys{m: m}.type_("/cd").enter()
	if !m.dirPick.open {
		t.Fatal("bare /cd did not open the folder browser")
	}
	if !pathnorm.Same(m.dirPick.dir, dmtools.Root()) {
		t.Errorf("the browser opened at %q, want the current folder %q", m.dirPick.dir, dmtools.Root())
	}
	view := m.View().Content
	if !strings.Contains(view, "src"+string(filepath.Separator)) {
		t.Errorf("the browser does not list the subdirectory:\n%s", view)
	}
	if strings.Contains(view, "readme.md") {
		t.Errorf("the browser offered a file:\n%s", view)
	}
	if !strings.Contains(view, "use this folder") {
		t.Errorf("the browser offers no way to pick the folder it is showing:\n%s", view)
	}
}

// TestDirPickDescendsAndPicks drives the browser the way a user does: enter
// descends into a directory, then the pick row hands the chosen folder to
// changeDir — the same path a hand-typed /cd target takes, so the boundary
// moves with the session.
func TestDirPickDescendsAndPicks(t *testing.T) {
	m, dir := dirPickModel(t)
	m.openDirPick()

	// Navigate to src/ by its entry rather than by counting rows: the folder
	// order is alphabetical, so a hardcoded arrow count would depend on which
	// folders the fixture happens to name.
	idx := -1
	for i, e := range m.dirPick.entries {
		if strings.HasSuffix(filepath.ToSlash(e.path), "/src") {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("the browser does not list src/: %v", m.dirPick.entries)
	}
	d := setupKeys{m: m}
	for m.dirPick.selected < idx {
		d = d.down()
	}
	d = d.enter()
	if !pathnorm.Same(m.dirPick.dir, filepath.Join(dir, "src")) {
		t.Fatalf("the browser is at %q, want the src folder in %q", m.dirPick.dir, dir)
	}

	// The highlight is back on the pick row after a load: enter now chooses src.
	d = d.enter()
	if m.dirPick.open {
		t.Fatal("a pick did not close the browser")
	}
	if !pathnorm.Same(m.workDir, filepath.Join(dir, "src")) {
		t.Errorf("the session moved to %q, want %q", m.workDir, filepath.Join(dir, "src"))
	}
	if filepath.Base(dmtools.Root()) != "src" {
		t.Errorf("the tool boundary is %q, want it moved into src", dmtools.Root())
	}
}

// TestDirPickEscClosesWithoutChanging is the cheap undo: escaping the browser
// leaves both the dialog and the folder alone, so a wrong arrow costs nothing.
func TestDirPickEscClosesWithoutChanging(t *testing.T) {
	m, dir := dirPickModel(t)
	m.openDirPick()

	setupKeys{m: m}.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.dirPick.open {
		t.Fatal("esc did not close the folder browser")
	}
	if !pathnorm.Same(m.workDir, dmtools.Root()) || filepath.Base(m.workDir) != filepath.Base(dir) {
		t.Errorf("esc changed the session folder to %q", m.workDir)
	}
}
