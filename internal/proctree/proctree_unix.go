//go:build !windows

package proctree

import (
	"os/exec"
	"syscall"
)

// Group puts the command in a process group of its own.
//
// The group is what makes the kill whole: a signal sent to -pid reaches
// every process the shell started, and the children it started, without
// anyone having to walk a tree and hope nothing forks between the walking
// and the killing. Setpgid makes the shell the group leader the moment it
// starts, so the group exists exactly as long as any member of it can be
// alive.
func Group(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Attach has nothing to do on Unix: the group set before the start already
// reaches every descendant, and a signal needs no bookkeeping afterwards.
func Attach(pid int) {}

// Kill signals the whole group. The fallback to the bare pid covers the race
// where the leader exited (so the group may have dissolved) but the caller
// still means the signal — the group signal already reached every member
// while it was in the group; the bare kill then simply says the leader is
// gone.
func Kill(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

// Release has nothing to release on Unix.
func Release(pid int) {}
