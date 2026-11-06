package tools

import (
	"os/exec"

	"github.com/dedomorozoff/dmcode/internal/proctree"
)

// The process-tree seam. The mechanism — Unix groups, Windows job objects —
// lives in internal/proctree, shared with the editor's PTY so a tree means
// the same thing in both places; these are the exec.Cmd-shaped handles the
// command runners use. See internal/proctree/proctree.go for why the naive
// kill is not enough.

// processTree prepares a command to be killable as a tree. Call before Start.
func processTree(cmd *exec.Cmd) { proctree.Group(cmd) }

// attachKillJob binds an already-started process to its tree. Windows needs
// it because the job is the mechanism there; on Unix it is a no-op.
func attachKillJob(cmd *exec.Cmd) {
	if cmd.Process != nil {
		proctree.Attach(cmd.Process.Pid)
	}
}

// releaseKillJob forgets the started process once it exited on its own.
func releaseKillJob(cmd *exec.Cmd) {
	if cmd.Process != nil {
		proctree.Release(cmd.Process.Pid)
	}
}

// killTree kills the command and its descendants.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return proctree.Kill(cmd.Process.Pid)
}
