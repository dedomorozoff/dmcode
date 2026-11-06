//go:build !windows

package ptyterm

import (
	"os"
	"os/exec"

	"github.com/creack/pty"

	"github.com/dedomorozoff/dmcode/internal/proctree"
)

func start(opts Options) (*Terminal, error) {
	shell := opts.Command
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	cmd := exec.Command(shell, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	// The shell gets a process group of its own so Close can kill what the
	// shell started, not only the shell: a build left running in a closed
	// terminal tab is the same orphan the chat's run_command had.
	proctree.Group(cmd)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(opts.Width), Rows: uint16(opts.Height)})
	if err != nil {
		return nil, err
	}
	return &Terminal{
		rw: f,
		resize: func(cols, rows int) error {
			return pty.Setsize(f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		},
		wait: cmd.Wait,
		close: func() error {
			if cmd.Process != nil {
				_ = proctree.Kill(cmd.Process.Pid)
			}
			return f.Close()
		},
	}, nil
}
