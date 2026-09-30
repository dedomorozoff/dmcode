package discover

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGGUFBinaryResolvesTheProjectFolder pins the lookup order: an explicit
// DMCODE_LLAMA_SERVER wins, then a binary dropped into ./llama, then PATH.
// With nothing anywhere the error has to name the project folder, since that
// is the option a user can fix without touching any configuration.
func TestGGUFBinaryResolvesTheProjectFolder(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DMCODE_LLAMA_SERVER", "")
	t.Setenv("PATH", "")

	_, err := ggufBinary()
	if err == nil || !strings.Contains(err.Error(), "llama") {
		t.Fatalf("nothing anywhere gave %v, want a complaint naming llama", err)
	}

	name := localServerNames()[0]
	if err := os.MkdirAll(filepath.Join(dir, "llama"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "llama", name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ggufBinary()
	if err != nil {
		t.Fatalf("a binary in ./llama was not picked up: %v", err)
	}
	if want := filepath.Join("llama", name); got != want {
		t.Errorf("ggufBinary = %q, want %q", got, want)
	}

	explicit := filepath.Join(dir, "elsewhere", name)
	if err := os.MkdirAll(filepath.Dir(explicit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DMCODE_LLAMA_SERVER", explicit)
	got, err = ggufBinary()
	if err != nil || got != explicit {
		t.Errorf("DMCODE_LLAMA_SERVER = %q (err %v), want the explicit path", got, err)
	}
}

// LaunchGGUF refuses to guess: with nothing configured there is no file to
// run, and the error has to say which variable is missing.
func TestLaunchGGUFRequiresTheEnvVar(t *testing.T) {
	t.Setenv("DMCODE_GGUF", "")
	_, err := LaunchGGUF()
	if err == nil || !strings.Contains(err.Error(), "DMCODE_GGUF") {
		t.Errorf("LaunchGGUF with no DMCODE_GGUF gave %v, want a complaint about DMCODE_GGUF", err)
	}
}

func TestLaunchGGUFRejectsMissingAndDirectoryPaths(t *testing.T) {
	t.Setenv("DMCODE_GGUF", "does-not-exist.gguf")
	if _, err := LaunchGGUF(); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("a missing file gave %v, want a complaint about the missing file", err)
	}

	t.Setenv("DMCODE_GGUF", ".")
	if _, err := LaunchGGUF(); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("a directory gave %v, want a complaint about a directory", err)
	}
}

func TestWaitReadyAnswersWhenTheServerIsUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"qwen.gguf"}]}`))
	}))
	defer srv.Close()

	if err := waitReady(srv.URL, 2*time.Second, make(chan error, 1)); err != nil {
		t.Errorf("a live server timed out: %v", err)
	}
}

func TestWaitReadyReportsADeadProcessAtOnce(t *testing.T) {
	died := make(chan error, 1)
	died <- http.ErrServerClosed // any non-nil: the process is gone

	start := time.Now()
	err := waitReady("http://127.0.0.1:1/v1", 30*time.Second, died)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("a dead process gave %v, want an exited complaint", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a dead process was reported after %s; it must not wait out the timeout", d)
	}
}

func TestWaitReadyTimesOutOnSilence(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if err := waitReady(srv.URL, 300*time.Millisecond, make(chan error, 1)); err == nil {
		t.Error("a server that never answers /models was accepted")
	}
}

// ggufModelName prefers what the server reports and falls back to the file
// stem, so an endpoint with an unusual /models still gets a usable id.
func TestGGUFModelNameFallsBackToTheFileStem(t *testing.T) {
	if got := ggufModelName(`C:\models\Qwen2.5-Coder-7B-Q4_K_M.gguf`, "http://127.0.0.1:1/v1"); got != "Qwen2.5-Coder-7B-Q4_K_M" {
		t.Errorf("stem fallback gave %q", got)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"model-id-from-server"}]}`))
	}))
	defer srv.Close()
	if got := ggufModelName("whatever.gguf", srv.URL); got != "model-id-from-server" {
		t.Errorf("server id gave %q, want model-id-from-server", got)
	}
}
