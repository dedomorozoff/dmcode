package discover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain makes this binary usable as a stand-in for llama-server.
//
// The load has to be tested against a real child process — that is the only way
// to prove the caller is not blocked on it, and that a child is actually killed
// when the load is abandoned. Re-executing the test binary is the one fake
// available on every platform without shipping a build: it is a real process
// that starts, stays alive and can be killed.
//
// The switch has to sit in TestMain rather than in a test, because llama-server
// is invoked with arguments the flag package would reject outright, and the
// rejection would come with an exit before any test function runs.
func TestMain(m *testing.M) {
	switch os.Getenv("DMCODE_FAKE_LLAMA") {
	case "sleep":
		// A model that never finishes loading: alive, silent, no port.
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "llama_model_loader: failed to load model")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// fakeLlama points the launch at this test binary running in mode, with a real
// .gguf beside it. The file's contents are never read — the fake does not open
// it — but startGGUF stats it, and a launch that fails on a missing file never
// reaches the part under test.
func fakeLlama(t *testing.T, mode string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, []byte("not a real gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DMCODE_GGUF", path)
	t.Setenv("DMCODE_LLAMA_SERVER", os.Args[0])
	t.Setenv("DMCODE_FAKE_LLAMA", mode)
	// ggufState is package-level: one test's server must not be the next one's.
	t.Cleanup(StopGGUF)
	StopGGUF()
}

// The whole change is this one assertion. Before it, StartGGUF's caller sat in
// waitReady for the load — up to three minutes — with nothing on screen but two
// lines on stderr. The load is now behind the interface, and the only way that is
// true is if starting it returns while the model is still coming in.
func TestStartingTheServerDoesNotWaitForTheModel(t *testing.T) {
	fakeLlama(t, "sleep")
	t.Setenv("DMCODE_GGUF_STARTUP", "60")

	start := time.Now()
	l, err := StartGGUF()
	if err != nil {
		t.Fatalf("StartGGUF: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("StartGGUF blocked for %s; the model is still loading", d)
	}
	// And it really is still loading, rather than fast: a fake that answered
	// immediately would pass the test above while proving nothing.
	select {
	case <-l.done:
		t.Fatal("the model reports itself ready against a server that never listened")
	case <-time.After(50 * time.Millisecond):
	}
}

// A load that cannot be abandoned is a wait the user has to sit through, and
// sitting through it is exactly what moving the load behind the interface was
// supposed to stop. StopGGUF while the weights are still being read has to end
// the wait at once — and, because the state is registered at spawn, it has to
// actually reach the child.
func TestGivingUpOnALoadStopsWaitingAndKillsTheServer(t *testing.T) {
	fakeLlama(t, "sleep")
	// A minute the test must not sit through: if the cancellation does not work,
	// the failure is this test taking a minute and reporting it, not hanging.
	t.Setenv("DMCODE_GGUF_STARTUP", "60")

	l, err := StartGGUF()
	if err != nil {
		t.Fatalf("StartGGUF: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- l.Wait() }()

	StopGGUF()
	select {
	case err := <-waited:
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("Wait() = %v, want a cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StopGGUF did not end the wait; the load is still running")
	}
}

// A cancellation is not a failure, and the two need opposite reactions from a
// caller: a failure belongs in the transcript with the log path to look at,
// while a cancelled load is something the user asked for. Collapsing them would
// make the UI report a timeout the user themselves caused.
func TestACancelledLoadIsNotAFailedLoad(t *testing.T) {
	if !errors.Is(ErrCancelled, ErrCancelled) {
		t.Fatal("ErrCancelled does not match itself")
	}
	// The distinction only works if a caller can ask, which is what errors.Is is
	// for: a wrapped cancellation must still be recognisable.
	wrapped := errors.New("llama-server did not answer: " + ErrCancelled.Error())
	if errors.Is(wrapped, ErrCancelled) {
		t.Error("a cancellation turned into a plain string is no longer one")
	}
}

// Two callers must share one load. The /setup picker and the start-up path can
// both ask for a model, and the second one used to block on a mutex for the
// whole load — on the UI's event loop, with no way to cancel it and nothing on
// screen to say why.
func TestASecondLaunchSharesTheFirstOne(t *testing.T) {
	fakeLlama(t, "sleep")
	t.Setenv("DMCODE_GGUF_STARTUP", "60")

	first, err := StartGGUF()
	if err != nil {
		t.Fatalf("StartGGUF: %v", err)
	}
	second, err := StartGGUF()
	if err != nil {
		t.Fatalf("second StartGGUF: %v", err)
	}
	if first != second {
		t.Fatal("a second launch started a second llama-server on the same file")
	}
	if first.Prov.BaseURL != second.Prov.BaseURL {
		t.Errorf("the shared launch has two endpoints: %q and %q", first.Prov.BaseURL, second.Prov.BaseURL)
	}
}

// What can be wrong in a second has to be reported in a second. A user who typed
// a path that does not exist should not watch a spinner for three minutes to
// learn it, and they should learn it before the interface is even drawn.
func TestTheFastFailuresAreReportedWithoutWaiting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DMCODE_LLAMA_SERVER", os.Args[0])
	t.Cleanup(StopGGUF)
	StopGGUF()

	t.Run("no env var", func(t *testing.T) {
		t.Setenv("DMCODE_GGUF", "")
		if _, err := StartGGUF(); err == nil {
			t.Error("a launch with nothing configured was accepted")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		t.Setenv("DMCODE_GGUF", filepath.Join(dir, "nope.gguf"))
		start := time.Now()
		if _, err := StartGGUF(); err == nil {
			t.Error("a missing .gguf was accepted")
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("a missing file took %s to report", d)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		path := filepath.Join(dir, "there.gguf")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DMCODE_GGUF", path)
		t.Setenv("DMCODE_LLAMA_SERVER", filepath.Join(dir, "no-such-server"))
		if _, err := StartGGUF(); err == nil {
			t.Error("a missing llama-server was accepted")
		}
	})
}

// llama.cpp draws its load progress with carriage returns on one line, so the
// last line of the log file is one enormous run of them. The note a waiting user
// reads has to be the last progress line a person would recognise, which is what
// this measures — and the same reader has to cope with a file it cannot open.
func TestTheLoadTailIsTheLastProgressLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "llama-server.log")
	body := "llama_model_loader: loaded meta data\rload_tensors:  10%\rload_tensors: 100%\r\nmain: server is listening\n"
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Launch{logPath: log}
	if got := l.Tail(); got != "main: server is listening" {
		t.Errorf("Tail() = %q, want the last complete line", got)
	}

	// An unopenable file is silence, not an error the user has to read.
	if got := (&Launch{logPath: filepath.Join(dir, "nope.log")}).Tail(); got != "" {
		t.Errorf("Tail() on a missing file = %q, want empty", got)
	}
	if got := (&Launch{}).Tail(); got != "" {
		t.Errorf("Tail() with no log = %q, want empty", got)
	}
}
