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

// ggupState remembers the server this process started, so a second launch
// (a /setup pick while the startup one is already loading) reuses it instead of
// spawning a second llama-server on the same file.
var (
	ggufMu    sync.Mutex
	ggufState *ggufServer
)

type ggufServer struct {
	prov    config.Provider
	stop    func()
	logPath string
}

// LaunchGGUF starts llama-server on the .gguf file DMCODE_GGUF names and
// returns the provider to reach it through. It blocks until the server answers
// /v1/models — a model load can take minutes on a slow disk, so callers running
// on a UI thread should call this from a goroutine.
//
// The server is a child of this process: StopGGUF takes it down, and main
// defers that call so the model never outlives the session.
func LaunchGGUF() (config.Provider, error) {
	ggufMu.Lock()
	defer ggufMu.Unlock()
	if ggufState != nil {
		return ggufState.prov, nil
	}
	srv, err := startGGUF()
	if err != nil {
		return config.Provider{}, err
	}
	ggufState = srv
	return srv.prov, nil
}

// StopGGUF shuts down the llama-server this process started, if any. Safe to
// call more than once and safe to call when nothing was ever started.
func StopGGUF() {
	ggufMu.Lock()
	srv := ggufState
	ggufState = nil
	ggufMu.Unlock()
	if srv != nil {
		srv.stop()
	}
}

func startGGUF() (*ggufServer, error) {
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
		return nil, fmt.Errorf("could not start %s: %w", binPath, err)
	}

	died := make(chan error, 1)
	go func() { died <- cmd.Wait() }()

	timeout := ggufStartupTimeout()
	if err := waitReady(ggufBaseURL(port), timeout, died); err != nil {
		cmd.Process.Kill()
		<-died
		return nil, fmt.Errorf("%w; the server's output is in %s", err, logPath)
	}

	model := ggufModelName(abs, ggufBaseURL(port))
	prov := config.Provider{
		BaseURL: ggufBaseURL(port),
		Model:   model,
		API:     config.APIChat,
		Label:   i18n.T("llama.cpp GGUF"),
	}
	return &ggufServer{
		prov:    prov,
		logPath: logPath,
		stop: func() {
			cmd.Process.Kill()
			<-died
			logFile.Close()
		},
	}, nil
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

// waitReady polls the server until /v1/models answers, the process dies, or
// the deadline passes. A dead process is reported immediately rather than
// after the full timeout: a missing model file should fail in seconds, not
// after three minutes of silence.
func waitReady(base string, timeout time.Duration, died <-chan error) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := fetchModels(base, "", 800*time.Millisecond); err == nil {
			return nil
		}
		select {
		case err := <-died:
			return fmt.Errorf("llama-server exited before it was ready: %v", err)
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
// /v1/models that answers unusually would leave us with.
func ggufModelName(ggufPath, base string) string {
	if served, err := fetchModels(base, "", 2*time.Second); err == nil && len(served) > 0 && served[0] != "" {
		return served[0]
	}
	return strings.TrimSuffix(filepath.Base(ggufPath), filepath.Ext(ggufPath))
}

// ggufStartupTimeout is how long LaunchGGUF waits for the model to load.
// DMCODE_GGUF_STARTUP sets it in seconds; the default of 180 covers a 7B
// quant from a cold disk, and a 70B on CPU needs the override.
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
