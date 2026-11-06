//go:build !windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// procAlive asks ps whether the pid still has a live (not zombie) process.
// A zombie is dead for the test's purposes: the signal reached it, and only
// the reaper has not arrived yet.
func procAlive(t *testing.T, pid int) bool {
	t.Helper()
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "state=").Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	return state != "" && !strings.HasPrefix(state, "Z")
}

// TestCancelKillsTheWholeTreeUnix is the end-to-end half of the cancellation
// fix: a shell backgrounds a long sleep, the context is cancelled, and the
// sleep must die with the shell.
//
// Two assertions carry the proof, and the timing one is load-bearing: if only
// the shell died, the orphan would keep the output pipe open and the wait
// would return only after WaitDelay — five or more seconds. A group kill
// closes the pipes itself, so returning well under that means the tree died.
func TestCancelKillsTheWholeTreeUnix(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep on PATH")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := shellCommand(ctx, "sleep 60 & echo $! > "+pidFile+"; wait", dir)

	started := make(chan error, 1)
	go func() {
		// collectOutput, not Output: the test drives the production path.
		_, err := collectOutput(cmd)
		started <- err
	}()

	childPID := waitForFilePID(t, pidFile)
	if childPID <= 0 {
		t.Skip("the shell did not report its child in time")
	}
	if !procAlive(t, childPID) {
		t.Fatalf("the background sleep (pid %d) was not alive before the cancel", childPID)
	}

	killed := time.Now()
	cancel()
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("cancelling the context did not end the command's wait")
	}
	if elapsed := time.Since(killed); elapsed > 4*time.Second {
		t.Errorf("the wait unblocked only after %v: the tree was not killed, WaitDelay rescued it", elapsed)
	}
	if procAlive(t, childPID) {
		t.Errorf("the background sleep (pid %d) outlived the cancelled shell", childPID)
	}
}

func waitForFilePID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
				return n
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}
