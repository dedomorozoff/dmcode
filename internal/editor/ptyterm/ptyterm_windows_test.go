//go:build windows

package ptyterm

import (
	"os"
	"testing"
)

func testOptions() Options {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return Options{Command: shell, Args: []string{"/C", "echo dmed-pty-ok"}, Width: 80, Height: 24}
}

// TestCloseReleasesTheWorkingDirectory is the bug the Windows build of the
// editor's shutdown test kept hitting. A terminal that is closed leaves its
// shell running, the shell holds its working directory open, and Windows will
// not remove a directory that something has open. The editor test that spawns a
// terminal therefore failed in t.TempDir's cleanup with "the process cannot
// access the file because it is being used by another process" — an error about
// a file, caused by a process, in a test about status icons, which is a
// question nobody asked.
//
// So this asks it directly instead of leaving it to another test's cleanup:
// close the terminal, then remove the directory the shell was sitting in. The
// removal is the assertion, and it happens in the test body where a failure is
// readable — not in a deferred cleanup, which is how this went unnoticed.
//
// The shell has to be one that stays put. The shell in testOptions runs /C echo
// and exits by itself, which is why every other test here passed while the
// editor's terminal, which stays open, did not.
func TestCloseReleasesTheWorkingDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("", "ptyterm-closedir")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	opts := testOptions()
	opts.Args = nil // no /C: the shell waits for input instead of exiting
	opts.Dir = dir
	term, err := Start(opts)
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// By the time Close returns, not a moment later: everything downstream of a
	// close — this test's own cleanup, a session directory somebody removes
	// after quitting — happens on that timeline, and a Close that only kills the
	// shell eventually is a Close that leaks.
	if err := os.RemoveAll(dir); err != nil {
		t.Errorf("the directory the shell was sitting in cannot be removed after Close: %v", err)
	}
}
