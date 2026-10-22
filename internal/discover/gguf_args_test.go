package discover

// gguf_args_test.go — what llama-server is actually started with. The default
// context window and the SetContextSize replacement are unit-tested on the
// builder, and one test runs the real spawn against the re-exec fake whose
// only job is to record its command line, since args assembled in-process are
// not yet args that reached the exec.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGGUFArgsAddsADefaultContext(t *testing.T) {
	got := ggufArgs("")
	want := []string{"-c", strconv.Itoa(DefaultContext)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ggufArgs(\"\") = %v, want %v", got, want)
	}
	// Flags the user keeps pass through beside the default.
	got = ggufArgs("-ngl 99")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-ngl 99") || !strings.Contains(joined, "-c") {
		t.Errorf("ggufArgs(\"-ngl 99\") = %v, want the user's flags and a -c", got)
	}
}

// TestGGUFArgsKeepsAnExplicitContext covers every spelling the user's own
// context size arrives in — a bare hour-glass flag pair, a long flag, and the
// one-token forms. Any of them must keep llama-server's own 4096 out.
func TestGGUFArgsKeepsAnExplicitContext(t *testing.T) {
	for _, args := range []string{
		"-c 8192",
		"--ctx-size 8192",
		"-c8192",
		"-c=8192",
		"--ctx-size=8192",
	} {
		fields := strings.Fields(args)
		if !hasContextFlag(fields) {
			t.Errorf("hasContextFlag(%v) = false, want true", fields)
		}
		got := ggufArgs(args)
		if strings.Join(got, " ") != args {
			t.Errorf("ggufArgs(%q) = %q, want it passed through whole", args, strings.Join(got, " "))
		}
	}
	// A flag that merely begins the same way is not a context size: it must
	// survive, and the default must still be supplied.
	got := ggufArgs("--cont-batching")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--cont-batching") {
		t.Errorf("ggufArgs(\"--cont-batching\") = %q, want the flag kept", joined)
	}
	if !hasContextFlag(got) {
		t.Errorf("the default context was not supplied alongside a kept flag")
	}
}

func TestSetContextSizeReplacesTheSize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"-c 4096 -ngl 99", "-ngl 99 -c 12288"},
		{"--ctx-size 4096", "-c 12288"},
		{"-c4096", "-c 12288"},
		{"--ctx-size=4096 -ngl 99", "-ngl 99 -c 12288"},
		{"", "-c 12288"},
	} {
		if got := SetContextSize(tc.in, 12288); got != tc.want {
			t.Errorf("SetContextSize(%q, 12288) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A trailing size flag with no value token must not swallow anything —
	// malformed args are the user's business, not something to mis-split.
	if got := SetContextSize("-c", 12288); got != "-c 12288" {
		t.Errorf("SetContextSize(\"-c\", 12288) = %q, want the dangling flag replaced", got)
	}
}

// TestSpawnRecordsTheDefaultContext drives the real exec: the fake binary in
// dump mode writes its own argv to disk, and the file must carry the default
// -c when the user's args named nothing. Args built in-process are not args
// that reached llama-server until a child has actually read them.
func TestSpawnRecordsTheDefaultContext(t *testing.T) {
	fakeLlama(t, "dump")
	dump := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("DMCODE_ARG_DUMP", dump)
	t.Setenv("DMCODE_LLAMA_ARGS", "-ngl 99")

	l, err := StartGGUF()
	if err != nil {
		t.Fatalf("StartGGUF: %v", err)
	}
	// The launch is not waited on: the fake exits immediately, its died event
	// lands in the launch's own goroutine, and fakeLlama's cleanup takes the
	// (already dead) state down.
	_ = l

	var text string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, rerr := os.ReadFile(dump)
		if rerr == nil {
			text = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if text == "" {
		t.Fatal("the fake llama-server never wrote its argv")
	}
	argv := strings.Split(strings.TrimSpace(text), "\n")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-ngl 99") {
		t.Errorf("the user's own args did not reach the exec: %s", joined)
	}
	// The default -c is what reached the exec, not just what the builder said.
	idx := -1
	for i, a := range argv {
		if a == "-c" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(argv) || argv[idx+1] != strconv.Itoa(DefaultContext) {
		t.Errorf("the exec did not carry -c %d: %s", DefaultContext, joined)
	}
}
