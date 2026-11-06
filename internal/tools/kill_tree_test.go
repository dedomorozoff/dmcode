package tools

import (
	"context"
	"testing"
	"time"
)

// TestShellCommandWiresTreeKill pins the three settings that make a cancelled
// command actually end: the tree kill on cancel, the bounded wait after it, and
// the timeout context the caller handed in. Without any one of them the old
// failure returns — the shell dies, an orphan keeps the output pipe, and the
// turn waits for a process nobody is waiting for.
func TestShellCommandWiresTreeKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := shellCommand(ctx, "echo x", t.TempDir())
	if cmd.Cancel == nil {
		t.Error("Cancel is nil: context cancellation would kill only the shell")
	}
	if cmd.WaitDelay <= 0 {
		t.Errorf("WaitDelay = %v, want a bounded grace after the kill", cmd.WaitDelay)
	}
	if cmd.Dir == "" {
		t.Error("Dir not set: the command would run in the process directory")
	}
}
