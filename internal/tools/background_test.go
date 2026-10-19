package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Commands that run behind the turn.
//
// The shape of the thing is what these tests are about: a background command
// starts at once, keeps running after the call that started it has returned,
// and can be asked about afterwards — while it runs and after it has
// finished.

// TestABackgroundCommandStartsAtOnceAndKeepsRunning: the whole point is that
// the call returns before the work is done.
func TestABackgroundCommandStartsAtOnceAndKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	// The command is still holding the directory when the test ends, so it
	// has to be stopped before the temporary directory is removed.
	defer StopBackground()
	// A command that takes long enough to still be running when the call
	// returns, and that leaves a file behind when it ends.
	marker := filepath.Join(dir, "finished.txt")
	start, err := startBackground("sleep 2 && echo done > "+marker, dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if start.Handle == "" {
		t.Fatal("the command was started without a handle")
	}
	if start.Status != "running" {
		t.Errorf("status = %q, want running", start.Status)
	}

	// The call has returned; the command has not.
	status, err := readBackground(start.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "running" {
		t.Errorf("status = %q, want running", status.Status)
	}
	if status.Output != "" {
		t.Errorf("the output is %q, want nothing yet", status.Output)
	}

	// And it is still running a moment later, which is what "background" means.
	time.Sleep(200 * time.Millisecond)
	again, err := readBackground(start.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != "running" {
		t.Errorf("status = %q, want running", again.Status)
	}
}

// TestABackgroundCommandIsAskedAboutAfterItFinishes: the second half of the
// feature. The agent comes back later and wants to know what happened.
func TestABackgroundCommandIsAskedAboutAfterItFinishes(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	// A single word, because the shells disagree about the rest: PowerShell's
	// `echo` is Write-Output, and it prints three arguments on three lines.
	start, err := startBackground("echo buildfailed", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	var status backgroundStatus
	for range 100 {
		status, err = readBackground(start.Handle)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status.Status != "done" {
		t.Fatalf("status = %q, want done", status.Status)
	}
	if !strings.Contains(status.Output, "buildfailed") {
		t.Errorf("the output is %q, want what the command printed", status.Output)
	}
	if status.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", status.ExitCode)
	}
}

// TestAFailedBackgroundCommandSaysSo: a command that fails is not a command
// that printed nothing.
func TestAFailedBackgroundCommandSaysSo(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	start, err := startBackground("exit 3", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var status backgroundStatus
	for range 100 {
		status, err = readBackground(start.Handle)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status.Status != "failed" {
		t.Errorf("status = %q, want failed", status.Status)
	}
	if status.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", status.ExitCode)
	}
}

// TestTheTailIsKeptAndTheDropIsCounted: a build prints thousands of lines and
// the failure is at the end, so the tail is the part worth having — and what
// was dropped is counted rather than being invisible.
func TestTheTailIsKeptAndTheDropIsCounted(t *testing.T) {
	// A cap of 24 bytes: the failure at the end is 23 of them, and the head
	// does not fit in what is left.
	buf := newTailBuffer(24)
	_, _ = buf.Write([]byte("the head of the output\n"))
	_, _ = buf.Write([]byte("the failure at the end\n"))
	text, dropped := buf.text()
	if !strings.Contains(text, "the failure at the end") {
		t.Errorf("the tail was dropped: %q", text)
	}
	if strings.Contains(text, "the head") {
		t.Errorf("the head was kept: %q", text)
	}
	if dropped == 0 {
		t.Error("the dropped bytes are not counted, so a reader cannot tell that the output is partial")
	}
}

// TestAnUnknownHandleIsRefusedWithTheShape: the model has to be able to
// recover from a wrong handle, and the answer says what a handle looks like.
func TestAnUnknownHandleIsRefusedWithTheShape(t *testing.T) {
	_, err := readBackground("cmd-999")
	if err == nil {
		t.Fatal("an unknown handle was accepted")
	}
	if !strings.Contains(err.Error(), "cmd-1") {
		t.Errorf("the error is %q, want it to show what a handle looks like", err)
	}
}

// TestTheBackgroundCommandIsRefusedToo: the gate in front of the shell does
// not open for a command that runs behind the turn.
func TestTheBackgroundCommandIsRefusedToo(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(nil, runCommandArgs{Command: "rm -rf /", Background: true})
	if err == nil {
		t.Fatal("the command was not refused")
	}
	if !strings.Contains(err.Error(), "will not run") {
		t.Errorf("the error is %q, want it to say the command was refused", err)
	}
}

// TestTheBackgroundCommandRunsInTheWorkspace: a background command is a
// command, and it is held by the same boundary as the rest.
func TestTheBackgroundCommandRunsInTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "here.txt")
	start, err := startBackground("echo hi > "+marker, dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		status, err := readBackground(start.Handle)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the command did not run in the workspace: %v", err)
	}
}
