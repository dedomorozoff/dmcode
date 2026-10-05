//go:build windows

package ptyterm

import (
	"context"
	"os"
	"strings"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

func start(opts Options) (*Terminal, error) {
	shell := opts.Command
	if shell == "" {
		shell = os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
	}
	command := quoteWindows(shell)
	for _, arg := range opts.Args {
		command += " " + quoteWindows(arg)
	}
	cpty, err := conpty.Start(command,
		conpty.ConPtyDimensions(opts.Width, opts.Height),
		conpty.ConPtyWorkDir(opts.Dir),
		conpty.ConPtyEnv(append(os.Environ(), opts.Env...)),
	)
	if err != nil {
		return nil, err
	}
	return &Terminal{
		rw: cpty, resize: cpty.Resize,
		wait:  func() error { _, err := cpty.Wait(context.Background()); return err },
		close: func() error { return closeSession(cpty) },
	}, nil
}

// closeSession ends the session: the child first, then the pseudo console and
// the handles.
//
// cpty.Close on its own does not end it, and the reason is a comment in the
// library that is wrong. ClosePseudoConsole signals the console and returns;
// it does not terminate the attached process, and the process handle closed
// straight afterwards only drops our reference to something that is still
// running. The shell lives on, and a shell has its working directory open, and a
// directory with a live handle in it cannot be deleted. So an editor session
// leaves a cmd.exe behind holding the very directory the next test — or the next
// cleanup — wants to remove, and the failure lands in t.TempDir's cleanup as
// "the process cannot access the file because it is being used by another
// process", which says nothing at all about the shell that is still in it.
//
// Terminating by pid and waiting for the exit is the only part that makes Close
// mean what a caller closing a terminal expects. The wait is bounded: a child
// that somehow refuses to die must not hang the UI on shutdown, and a leftover
// process is a smaller failure than a frozen one.
func closeSession(cpty *conpty.ConPty) error {
	terminateChild(cpty.Pid())
	return cpty.Close()
}

func terminateChild(pid int) {
	if pid <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone, or not ours to touch
	}
	defer windows.CloseHandle(h)
	// A process that has already exited answers this with an error and is
	// already signalled, so the error is not worth reporting.
	if err := windows.TerminateProcess(h, 1); err != nil {
		return
	}
	if _, err := windows.WaitForSingleObject(h, uint32(childExitGraceMs)); err != nil {
		return
	}
}

// childExitGraceMs is how long Close waits for the shell to acknowledge the
// termination. Nothing about a shell takes this long; the number exists so that
// the wait is bounded rather than absent.
const childExitGraceMs = 2000

// quoteWindows applies the CommandLineToArgvW quoting rules for a single token.
func quoteWindows(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
