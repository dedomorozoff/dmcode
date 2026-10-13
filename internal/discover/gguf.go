// GGUF support: dmcode can drive llama.cpp's llama-server directly on a .gguf
// file instead of asking the user to run a server by hand. The server speaks
// the OpenAI chat wire on 127.0.0.1, so the rest of dmcode sees an ordinary
// provider; what this file adds is the process around it — picking a port,
// waiting for the model to load, and taking the server down when dmcode exits.
package discover

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// ggufState remembers the server this process started, so a second launch
// (a /setup pick while the startup one is still loading) reuses it instead of
// spawning a second llama-server on the same file.
//
// It is set when the process is *spawned*, not when it is ready. That is the
// difference between a load that can be abandoned and one that cannot: with the
// state registered at spawn, quitting mid-load takes the child down with it,
// where a state registered after the wait left an orphan llama-server holding a
// large model in memory with nothing left to serve.
var (
	ggufMu    sync.Mutex
	ggufState *Launch
)

// Launch is a llama-server that has been started and is not necessarily ready
// yet. Everything that can be known before the model is in memory is known here:
// the port, the URL, the log file. Only the model's own name waits for the
// server, because llama-server is what knows it.
//
// The two-step shape is the whole point. A 7B quant off a cold disk takes tens
// of seconds and a 70B on CPU takes minutes, and blocking a caller on that is a
// caller with nothing to show until it ends. Spawning is instant; only the wait
// is long, so only the wait is somebody else's problem.
type Launch struct {
	// Prov is the provider to reach the server through. Its model name is the
	// file's stem until the server reports its own; Provider returns the
	// corrected one once it has.
	Prov config.Provider
	// LogPath is where llama-server's output goes, or "" when the filesystem
	// refused to open it.
	LogPath string

	done      chan struct{}
	err       error
	finalProv config.Provider
	logPath   string
	stop      func()
}

// ErrCancelled says a load was abandoned rather than failed. It is its own
// value because the two need opposite reactions from a caller: a failure belongs
// in the transcript with the log path to look at, while a cancellation is
// something the user asked for and repeating it would be noise.
var ErrCancelled = errors.New("the model load was cancelled")

// Provider is the provider to build a runner against: the server's own model id
// if it has answered, the file's stem if it has not.
func (l *Launch) Provider() config.Provider {
	select {
	case <-l.done:
		return l.finalProv
	default:
		return l.Prov
	}
}

// Wait blocks until the model is loaded and answering, or the load failed. It is
// safe to call from several goroutines and safe to call after the launch has
// already finished.
func (l *Launch) Wait() error {
	<-l.done
	return l.err
}

// Tail is the last thing llama-server printed, for a UI that has to wait and
// should say why.
//
// llama.cpp draws its load progress with carriage returns on a single line, so
// the last line of the file is one enormous run of them. Splitting on both
// endings and keeping the last non-empty segment is what turns that into the
// one line a person would recognise.
func (l *Launch) Tail() string {
	if l.logPath == "" {
		return ""
	}
	f, err := os.Open(l.logPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	// Only the tail is read: the file grows for the life of the process, and a
	// progress bar redraws into it hundreds of times.
	const tailBytes = 4096
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return ""
	}
	n := min(tailBytes, st.Size())
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, st.Size()-int64(n)); err != nil && err != io.EOF {
		return ""
	}
	parts := strings.FieldsFunc(string(buf), func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(parts) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(parts[i]); line != "" {
			return line
		}
	}
	return ""
}

// StartGGUF starts llama-server on the .gguf file DMCODE_GGUF names and returns
// as soon as the process is running. It does not wait for the model to load: the
// caller decides who does, and a UI hands that to a goroutine and shows progress
// while it happens.
//
// The failures this reports are the instant ones — DMCODE_GGUF unset, the file
// missing, llama-server not on disk — which is exactly what should stay
// synchronous. A user who pointed at a path that does not exist finds out in
// milliseconds instead of after the load timeout, and finding out before the
// interface appears is worth more than finding out after.
//
// The server is a child of this process: StopGGUF takes it down, including
// mid-load, and main defers that call so the model never outlives the session.
func StartGGUF() (*Launch, error) {
	ggufMu.Lock()
	defer ggufMu.Unlock()
	if ggufState != nil {
		return ggufState, nil
	}
	l, err := spawnGGUF()
	if err != nil {
		return nil, err
	}
	ggufState = l
	return l, nil
}

// LaunchGGUF starts llama-server and waits for the model to load, returning the
// provider to reach it through. It is the blocking form, for callers with no
// user waiting on them; a UI should use StartGGUF and Wait on its own goroutine.
//
// A launch already in flight is shared rather than restarted: the second caller
// waits on the first one's result. Serialising on a mutex instead would have
// been the obvious implementation and is what this used to do, which meant a
// /setup pick during the startup load froze the UI's event loop for the whole
// load and could not be cancelled.
func LaunchGGUF() (config.Provider, error) {
	l, err := StartGGUF()
	if err != nil {
		return config.Provider{}, err
	}
	if err := l.Wait(); err != nil {
		return config.Provider{}, err
	}
	return l.Provider(), nil
}

// StopGGUF shuts down the llama-server this process started, if any. Safe to
// call more than once, safe to call when nothing was ever started, and safe to
// call while the model is still loading — which is the case that leaves an
// otherwise unreachable process behind.
func StopGGUF() {
	ggufMu.Lock()
	l := ggufState
	ggufState = nil
	ggufMu.Unlock()
	if l != nil {
		l.stop()
	}
}

// spawnGGUF validates, claims a port and starts the process. It does not wait.
func spawnGGUF() (*Launch, error) {
	ggufPath := os.Getenv("DMCODE_GGUF")
	if ggufPath == "" {
		return nil, errors.New("DMCODE_GGUF is not set")
	}
	abs, err := filepath.Abs(ggufPath)
	if err != nil {
		return nil, fmt.Errorf("bad DMCODE_GGUF %q: %w", ggufPath, err)
	}
	if info, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("the .gguf file is missing: %s", abs)
	} else if info.IsDir() {
		return nil, fmt.Errorf("DMCODE_GGUF points at a directory, not a .gguf file: %s", abs)
	}

	binPath, err := ggufBinary()
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("no free port for llama-server: %w", err)
	}
	args := []string{"-m", abs, "--host", "127.0.0.1", "--port", strconv.Itoa(port)}
	// DMCODE_LLAMA_ARGS carries llama-server's own flags (context size, GPU
	// layers, …) verbatim, split on spaces — the same convention run_command
	// tools use for simple invocations.
	args = append(args, strings.Fields(os.Getenv("DMCODE_LLAMA_ARGS"))...)

	logPath := ggufLogPath()
	var logFile io.WriteCloser
	if f, err := openLog(logPath); err == nil {
		logFile = f
	} else {
		logPath, logFile = "", discardCloser{}
	}

	cmd := exec.Command(binPath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("could not start %s: %w", binPath, err)
	}

	died := make(chan error, 1)
	go func() { died <- cmd.Wait() }()

	base := ggufBaseURL(port)
	stem := fileStem(abs)
	// Closing this ends the wait below without killing anything: it is how a
	// cancellation stops the poll from running on until the timeout and then
	// reporting a failure for a model the user chose to give up on.
	cancelled := make(chan struct{})
	var cancelOnce, stopOnce sync.Once

	l := &Launch{
		// The file's stem is the best name available before the server answers.
		// llama-server serves one model per invocation, so the name it is sent
		// with is not what decides anything — and a caller that needs the server's
		// own id can wait and ask.
		Prov:      ggufProvider(base, stem, 0),
		LogPath:   logPath,
		done:      make(chan struct{}),
		logPath:   logPath,
		finalProv: ggufProvider(base, stem, 0),
		stop: func() {
			stopOnce.Do(func() {
				cancelOnce.Do(func() { close(cancelled) })
				if err := cmd.Process.Kill(); err != nil {
					logFile.Close()
					return
				}
				select {
				case <-died:
				case <-time.After(5 * time.Second):
				}
				logFile.Close()
			})
		},
	}

	go func() {
		err := waitReady(base, ggufStartupTimeout(), died, cancelled)
		switch {
		case errors.Is(err, ErrCancelled):
			// Reported as itself, so a caller can stay quiet about a load the user
			// walked away from.
		case err == nil:
			// Now the server can be asked what it is serving: the model id it
			// reports, and the context window it was started with, which the
			// meter and the compaction threshold run on.
			model, context := ggufModelName(abs, base)
			l.finalProv = ggufProvider(base, model, context)
		default:
			if logPath != "" {
				err = fmt.Errorf("%w; the server's output is in %s", err, logPath)
			}
		}
		l.err = err
		close(l.done)
	}()
	return l, nil
}

func ggufProvider(base, model string, context int) config.Provider {
	return config.Provider{
		BaseURL: base,
		Model:   model,
		Context: context,
		API:     config.APIChat,
		Label:   i18n.T("llama.cpp GGUF"),
	}
}

func ggufBaseURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/v1", port)
}

// ggufBinary resolves the llama-server executable, in order of explicitness:
// DMCODE_LLAMA_SERVER, then a copy kept inside the project's llama/ folder,
// then PATH. The project folder exists so a user can drop a release archive
// next to their models and never touch PATH or .env; the whole folder matters,
// not just the exe — llama-server loads its ggml and CUDA dlls from beside it.
func ggufBinary() (string, error) {
	if bin := os.Getenv("DMCODE_LLAMA_SERVER"); bin != "" {
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
		if _, err := os.Stat(bin); err == nil {
			return bin, nil
		}
		return "", fmt.Errorf("DMCODE_LLAMA_SERVER points at %q, which does not exist; the binary can also live in .%c or on PATH", bin, filepath.Separator)
	}
	for _, name := range localServerNames() {
		local := filepath.Join("llama", name)
		if _, err := os.Stat(local); err == nil {
			return local, nil
		}
	}
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("llama-server not found: put it into .%cllama%c, set DMCODE_LLAMA_SERVER, or install llama.cpp on PATH", filepath.Separator, filepath.Separator)
}

// localServerNames covers the two shapes a dropped-in binary takes: with the
// .exe suffix on Windows and without one elsewhere.
func localServerNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"llama-server.exe"}
	}
	return []string{"llama-server"}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitReady polls the server until /v1/models answers, the process dies, the load
// is cancelled, or the deadline passes.
//
// Two of those four are reported at once rather than after a wait, and both
// matter for the same reason: a missing model file should fail in seconds, not
// after three minutes of silence. A dead process is one, and a cancelled load is
// the other — the user already knows they stopped it, and a failure report for
// it would be the tool arguing with them.
func waitReady(base string, timeout time.Duration, died <-chan error, cancelled <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := fetchModels(base, "", 800*time.Millisecond); err == nil {
			return nil
		}
		select {
		case err := <-died:
			return fmt.Errorf("llama-server exited before it was ready: %v", err)
		case <-cancelled:
			return ErrCancelled
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("llama-server did not answer within %s — a large model may need DMCODE_GGUF_STARTUP raised", timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// ggufModelName prefers the id the server itself reports — llama-server knows
// the model's real name — and falls back to the file's stem, which is what a
// /v1/models that answers unusually would leave us with. The context length the
// server stated travels back too, so a GGUF session's meter and compaction
// threshold use the cap llama-server was actually started with.
func ggufModelName(ggufPath, base string) (string, int) {
	if served, err := fetchModels(base, "", 2*time.Second); err == nil && len(served) > 0 && served[0].ID != "" {
		return served[0].ID, served[0].Context
	}
	return fileStem(ggufPath), 0
}

// fileStem strips a model file's directory and extension. The path may name a
// Windows file on a Linux machine and the other way round — a config is
// carried between machines — so both separator flavors are split, not just
// the one the running platform uses: filepath.Base on Linux sees no
// directory in "C:\models\qwen.gguf" and hands the whole line back.
func fileStem(p string) string {
	p = filepath.Base(p)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(p, filepath.Ext(p))
}

// ggufStartupTimeout is how long the wait for the model runs. DMCODE_GGUF_STARTUP
// sets it in seconds; the default of 180 covers a 7B quant from a cold disk, and
// a 70B on CPU needs the override.
func ggufStartupTimeout() time.Duration {
	v, err := strconv.Atoi(os.Getenv("DMCODE_GGUF_STARTUP"))
	if err != nil || v <= 0 {
		return 180 * time.Second
	}
	return time.Duration(v) * time.Second
}

// ggufLogPath keeps llama-server's chatter next to the session store, so a
// failed start can be diagnosed without rerunning it in a terminal.
func ggufLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".dmcode", "llama-server.log")
}

func openLog(path string) (io.WriteCloser, error) {
	if path == "" {
		return nil, errors.New("no home directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

// discardCloser stands in for a log the filesystem refused to give: Close
// must exist on the type, but closing nothing is fine.
type discardCloser struct{}

func (discardCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardCloser) Close() error                { return nil }
