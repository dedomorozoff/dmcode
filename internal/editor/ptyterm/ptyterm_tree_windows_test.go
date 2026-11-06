//go:build windows

package ptyterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCloseKillsWhatTheShellStarted is the half of Close that killing the
// shell by pid never covered: a child the shell backgrounded outlived the
// closed tab, because ConPTY hands back the shell's pid and the shell's
// children are not under that pid by the time anyone looks. Git Bash
// launches external commands through a helper that exits, re-parenting them,
// so the only mechanism that reaches them is the kill-on-close job attached
// at start.
//
// The measurement is the same one the chat's run_command tests use: the
// child must be gone within a bounded wait of Close returning, and the wait
// is short enough that a rescue by anything slower fails the test rather
// than passing it eventually.
func TestCloseKillsWhatTheShellStarted(t *testing.T) {
	bash := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(bash); err != nil {
		t.Skip("no Git Bash to be the shell that re-parents")
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "winpid.txt")

	// Background a sleep, look up its Windows pid (the $! Cygwin reports is
	// a posix pid ps -l maps to a WINPID column), write it out, then keep the
	// shell alive so Close — not the shell's own exit — is what ends this.
	//
	// Two things make this a loop rather than a one-shot read, both learned
	// from a failure under full-suite load: the ps -l lookup can race the
	// child's own registration and answer empty or stale, and a pid read once
	// can be recycled by an unrelated process before the Go side checks it.
	// So the fixture re-derives the winpid, refuses to publish it until
	// tasklist confirms a process with that pid exists right now, and keeps
	// republishing every quarter second — a file that exists names a child
	// that was alive at that moment, which is what the alive-before-Close
	// assertion is allowed to trust.
	//
	// tasklist is reached by full path because a bash started under the test
	// runner has no System32 on its PATH, and the /FI switch is escaped as //FI
	// because MSYS rewrites a single-slash argument that looks like a path.
	script := `sleep 120 & child=$!; ` +
		`( TL="$SYSTEMROOT/System32/tasklist.exe"; while :; do ` +
		`win=$(ps -l | awk -v c="$child" '$1==c {print $4}'); ` +
		`if [ -n "$win" ] && "$TL" //FI "PID eq $win" //NH 2>/dev/null | grep -q "$win"; then ` +
		`echo "$win" > "` + filepath.ToSlash(pidFile) + `"; fi; sleep 0.25; done ) & ` +
		`while :; do sleep 0.2; done`

	term, err := Start(Options{
		Command: bash,
		Args:    []string{"-c", script},
		Dir:     dir,
		Width:   80, Height: 24,
	})
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}

	pid := 0
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
				pid = n
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pid == 0 {
		_ = term.Close()
		t.Fatal("the shell never reported the background sleep's pid")
	}
	if !pidAlive(pid) {
		_ = term.Close()
		t.Fatalf("child pid %d is not even running; the fixture proves nothing", pid)
	}

	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The sleep is the point of the test: the shell dying is what the old
	// Close already did. Bounded because "eventually" is the failure this
	// whole seam exists to remove.
	deadline = time.Now().Add(5 * time.Second)
	for pidAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(pid) {
		t.Errorf("the child the shell started (pid %d) is running after Close — the terminal killed the shell and orphaned its tree", pid)
	}
}

// pidAlive asks the same thing tasklist answers: is there a process with
// this pid right now. Full path because a test started from bash may not
// have System32 in its PATH.
func pidAlive(pid int) bool {
	out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "tasklist.exe"),
		"/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
	if err != nil {
		return true // cannot ask; assume alive so the test says so rather than passing blind
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
