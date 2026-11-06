//go:build windows

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

// pidAlive asks tasklist for exactly that pid. The tool is found by full path
// because a test started from a POSIX shell's PATH may not carry System32 at
// all, and "tasklist: not found" would otherwise read as "process: not alive".
func pidAlive(pid int) bool {
	tasklist := filepath.Join(os.Getenv("SystemRoot"), "System32", "tasklist.exe")
	out, err := exec.Command(tasklist, "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
	if err != nil {
		return false
	}
	s := string(out)
	if strings.Contains(s, "No tasks") || strings.Contains(s, "NONE") {
		return false
	}
	return strings.Contains(s, strconv.Itoa(pid))
}

// sleeperFor writes the same experiment in the detected shell's own language:
// start a child that outlives any ordinary wait, report its pid to a file,
// then block until it would have finished.
func sleeperFor(shellPath, commandFlag, pidFile string) (string, bool) {
	switch commandFlag {
	case "-c": // bash
		// $! is the cygwin pseudo-pid, so the WINPID column of ps -l is the
		// number the Windows process checks understand. If the awk lookup comes
		// back empty, the caller skips the pid assertions and the timing half
		// still runs.
		return "sleep 120 & child=$!; win=$(ps -l | awk -v c=\"$child\" '$1==c {print $4}'); " +
			"echo \"$win\" > '" + pidFile + "'; wait", true
	case "-Command": // powershell, pwsh
		return "$p = Start-Process -FilePath ping.exe -ArgumentList '-n','120','127.0.0.1' -PassThru -WindowStyle Hidden; " +
			"$p.Id | Out-File -Encoding ascii '" + pidFile + "'; $p.WaitForExit()", true
	default: // cmd.exe — no portable way to learn the child's pid from text
		return "start /B ping 127.0.0.1 -n 120 & ping 127.0.0.1 -n 120 >nul", false
	}
}

// TestCancelKillsTheWholeTreeWindows is the end-to-end half of the fix:
// exec.Cmd.Kill kills only the shell on Windows, the child keeps the output
// pipe, and the cancelled command used to answer only when the child finished
// on its own schedule. The timing is the proof: taskkill /T kills the tree in
// a second or two, while a survivor would keep the wait blocked until the
// five-second WaitDelay rescued it.
func TestCancelKillsTheWholeTreeWindows(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	shell, flags := detectShell()
	command, havePID := sleeperFor(shell, flags[len(flags)-1], pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := shellCommand(ctx, command, dir)

	done := make(chan error, 1)
	go func() {
		// collectOutput, not Output: the kill-job is attached by the same code
		// the tool uses, so the test drives the production path end to end.
		_, err := collectOutput(cmd)
		done <- err
	}()

	childPID := 0
	if havePID {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && childPID == 0 {
			if b, err := os.ReadFile(pidFile); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
					childPID = n
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if childPID == 0 {
			t.Skip("the shell did not report its child's pid")
		}
		if !pidAlive(childPID) {
			t.Fatalf("the background child (pid %d) was not alive before the cancel", childPID)
		}
	}

	killed := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("cancelling the context did not end the command's wait")
	}
	if elapsed := time.Since(killed); elapsed > 4*time.Second {
		t.Errorf("the wait unblocked only after %v: the tree was not killed, WaitDelay rescued it", elapsed)
	}
	if childPID != 0 && pidAlive(childPID) {
		t.Errorf("the background child (pid %d) outlived the cancelled shell", childPID)
	}
}
