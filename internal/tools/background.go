package tools

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
)

// Commands that outlive the call that started them.
//
// `run_command` waits, and it waits at most ten minutes. A build on a cold
// cache, a full test suite on a big repository, a `go mod tidy` that has to
// reach the network — the wait runs out, the command is killed, and the turn
// learns nothing about what the build said.
//
// So a command can also be started in the background. It runs on, its output
// is collected as it comes, and the agent asks about it later with
// `command_output`: once to see how it is doing, and again when it has
// finished.
//
// Two things about the shape of it are deliberate.
//
// The process is not tied to the turn's context. A background command that
// died with the turn would be a slower version of the blocking one — the
// point is that the agent can start it, do something else, and come back.
//
// And it is tied to *dmcode*: leaving the program kills what it started,
// because a build that outlives the session that asked for it is a process
// nobody is waiting for.

// backgroundOutputCap is how much of a command's output is kept.
//
// The tail is the part worth having: a build that fails prints the failure
// last, after ten thousand lines of progress, and the head of the stream is
// the part nobody reads. What was dropped is counted, and the count is
// reported rather than being invisible.
const backgroundOutputCap = 256 * 1024

// backgroundMaxRuns is how many commands the registry remembers.
//
// A finished command stays readable for a while — the agent asks about it
// once it is done — and the oldest is dropped when the registry is full.
const backgroundMaxRuns = 32

// backgroundDefaultTimeout and backgroundMaxTimeout bound a background
// command. The default is long enough for a slow build and short enough that
// a forgotten command does not run for ever; the maximum is for the cases
// that really do take hours.
const (
	backgroundDefaultTimeout = time.Hour
	backgroundMaxTimeout     = 6 * time.Hour
)

// backgroundRun is one command running behind the turn.
type backgroundRun struct {
	// out collects what the command printed.
	out *tailBuffer
	// done is closed when the process has exited and its output is complete.
	done chan struct{}
	// mu guards the fields below.
	mu sync.Mutex
	// err is the process's own error, nil when it exited cleanly.
	err error
	// code is the exit status.
	code int
	// timedOut says the command was killed by its own ceiling.
	timedOut bool
	// ended is when the process exited.
	ended time.Time
	// started is when it began.
	started time.Time
	// kill stops the process. It is nil once the process is gone.
	kill func()
}

// The registry of background commands.
var (
	bgMu   sync.Mutex
	bgSeq  int
	bgRuns = map[string]*backgroundRun{}
)

// tailBuffer collects output and keeps the tail of it.
//
// The head is dropped from the front, and how much was dropped is remembered
// so that a reader can say "the first 40 KB is not here" rather than
// pretending the output is complete.
type tailBuffer struct {
	mu      sync.Mutex
	buf     []byte
	cap     int
	dropped int
}

func newTailBuffer(capacity int) *tailBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &tailBuffer{buf: make([]byte, 0, 4096), cap: capacity}
}

// Write implements io.Writer, so a pipe can be copied straight into it.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.cap {
		cut := len(t.buf) - t.cap
		t.dropped += cut
		t.buf = append(t.buf[:0], t.buf[cut:]...)
	}
	return len(p), nil
}

// text returns what is kept, and how much was dropped.
func (t *tailBuffer) text() (string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf), t.dropped
}

// backgroundStart is what a background launch answers with.
type backgroundStart struct {
	// Handle is what the agent passes to command_output.
	Handle string `json:"handle"`
	// Status is "running" — the command has started, and the answer says so
	// rather than pretending the work is done.
	Status string `json:"status"`
	// Command is the line that was started, so the transcript says what the
	// handle refers to.
	Command string `json:"command"`
	// Seconds is the ceiling the command runs under.
	Seconds int `json:"seconds"`
}

// backgroundStatus is what a question about a background command answers with.
type backgroundStatus struct {
	Handle string `json:"handle"`
	// Status is "running", "done", "failed" or "timed out".
	Status string `json:"status"`
	// Output is what the command has printed so far.
	Output string `json:"output"`
	// ExitCode is the status it exited with, when it has.
	ExitCode int `json:"exit_code,omitempty"`
	// Truncated says that the head of the output was dropped.
	Truncated bool `json:"truncated,omitempty"`
	// DroppedBytes is how much of the output was dropped.
	DroppedBytes int `json:"dropped_bytes,omitempty"`
	// Seconds is how long it has been running, or how long it ran.
	Seconds int `json:"seconds"`
}

// startBackground launches a command and returns without waiting for it.
func startBackground(command, dir string, timeout time.Duration) (backgroundStart, error) {
	shell, flags := detectShell()
	args := append(append([]string{}, flags...), command)
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return backgroundStart{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return backgroundStart{}, err
	}
	if err := cmd.Start(); err != nil {
		return backgroundStart{}, err
	}

	run := &backgroundRun{
		out:     newTailBuffer(backgroundOutputCap),
		done:    make(chan struct{}),
		started: time.Now(),
		kill:    func() { _ = cmd.Process.Kill() },
	}

	bgMu.Lock()
	bgSeq++
	handle := "cmd-" + strconv.Itoa(bgSeq)
	bgRuns[handle] = run
	bgMu.Unlock()
	pruneBackground()

	// The pipes are drained first and the process is waited for after: Wait
	// closes the pipes, so a Wait that came first would cut the output off
	// at the moment the command finished.
	var pipes sync.WaitGroup
	pipes.Add(2)
	go func() { defer pipes.Done(); _, _ = io.Copy(run.out, stdout) }()
	go func() { defer pipes.Done(); _, _ = io.Copy(run.out, stderr) }()
	go func() {
		pipes.Wait()
		err := cmd.Wait()
		run.mu.Lock()
		run.err = err
		run.ended = time.Now()
		run.kill = nil
		if run.timedOut {
			run.code = -1
		} else if err != nil {
			run.code = cmd.ProcessState.ExitCode()
		}
		run.mu.Unlock()
		close(run.done)
	}()

	// The ceiling is the command's own, not the turn's: a background command
	// is meant to outlive the turn, so the turn's cancellation must not
	// reach it.
	if timeout > 0 {
		time.AfterFunc(timeout, func() {
			run.mu.Lock()
			run.timedOut = true
			kill := run.kill
			run.mu.Unlock()
			if kill != nil {
				kill()
			}
		})
	}

	return backgroundStart{
		Handle:  handle,
		Status:  "running",
		Command: command,
		Seconds: int(timeout.Seconds()),
	}, nil
}

// readBackground answers a question about a command that is running, or ran.
func readBackground(handle string) (backgroundStatus, error) {
	bgMu.Lock()
	run, ok := bgRuns[handle]
	bgMu.Unlock()
	if !ok {
		return backgroundStatus{}, fmt.Errorf("no command with the handle %q — the handles are cmd-1, cmd-2 and so on, and the oldest ones are forgotten", handle)
	}

	out, dropped := run.out.text()
	status := backgroundStatus{
		Handle:       handle,
		Output:       out,
		Truncated:    dropped > 0,
		DroppedBytes: dropped,
	}

	run.mu.Lock()
	started, ended, timedOut, code, err := run.started, run.ended, run.timedOut, run.code, run.err
	run.mu.Unlock()

	switch {
	case timedOut:
		status.Status = "timed out"
		status.ExitCode = code
	case ended.IsZero():
		status.Status = "running"
		status.Seconds = int(time.Since(started).Seconds())
	default:
		status.Status = "done"
		if err != nil {
			status.Status = "failed"
		}
		status.ExitCode = code
		status.Seconds = int(ended.Sub(started).Seconds())
	}
	return status, nil
}

// pruneBackground forgets the oldest finished commands, so the registry does
// not grow for ever.
func pruneBackground() {
	bgMu.Lock()
	defer bgMu.Unlock()
	if len(bgRuns) <= backgroundMaxRuns {
		return
	}
	type aged struct {
		handle string
		at     time.Time
	}
	var done []aged
	for h, r := range bgRuns {
		r.mu.Lock()
		ended := r.ended
		r.mu.Unlock()
		if !ended.IsZero() {
			done = append(done, aged{h, ended})
		}
	}
	// The oldest first, so the ones that finished longest ago go.
	for i := 0; i < len(done); i++ {
		for j := i + 1; j < len(done); j++ {
			if done[j].at.Before(done[i].at) {
				done[i], done[j] = done[j], done[i]
			}
		}
	}
	for i := 0; i < len(done) && len(bgRuns) > backgroundMaxRuns; i++ {
		delete(bgRuns, done[i].handle)
	}
}

// StopBackground kills every command still running.
//
// It is called when dmcode exits. A build that outlives the session that
// asked for it is a process nobody is waiting for, and on Windows a child
// does not die with its parent.
func StopBackground() {
	bgMu.Lock()
	runs := make([]*backgroundRun, 0, len(bgRuns))
	for _, r := range bgRuns {
		runs = append(runs, r)
	}
	bgMu.Unlock()
	for _, r := range runs {
		r.mu.Lock()
		kill := r.kill
		r.mu.Unlock()
		if kill != nil {
			kill()
		}
	}
}

// backgroundTimeout is the ceiling for a background command.
func backgroundTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return backgroundDefaultTimeout
	}
	d := time.Duration(seconds) * time.Second
	if d > backgroundMaxTimeout {
		return backgroundMaxTimeout
	}
	return d
}

// commandOutputArgs is the tool's input.
type commandOutputArgs struct {
	Handle string `json:"handle" jsonschema:"The handle run_command gave you when it started the command, like \"cmd-1\"."`
}

// commandOutput answers a question about a command started in the background.
//
// Every outcome is text, and the text says what happened: a command that is
// still running, one that finished, one that failed, and one that ran out of
// time. The output is what the command printed, which for a build is where
// the failure is.
func commandOutput(ctx agent.Context, in commandOutputArgs) (backgroundStatus, error) {
	handle := strings.TrimSpace(in.Handle)
	if handle == "" {
		return backgroundStatus{}, fmt.Errorf("which command? pass the handle run_command gave you, like \"cmd-1\"")
	}
	return readBackground(handle)
}
