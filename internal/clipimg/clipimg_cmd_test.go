//go:build linux || darwin

package clipimg

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"testing"
)

// pngBytes encodes a picture for the fakes to hand back.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeTools swaps the ladder and the two command seams for the duration of a
// test. Nothing here may reach a real clipboard: wl-paste, xclip and osascript
// are the user's own tools, and a test that runs them reads whatever the user
// copied last (or pastes a picture into their session) — the same rule the
// Windows half of this package follows with injected format readers.
func fakeTools(t *testing.T, ladder []imageCandidate, out map[string]func(args ...string) ([]byte, error), have map[string]bool) {
	t.Helper()
	oldC, oldRun, oldHave := candidates, runCommand, commandExists
	t.Cleanup(func() { candidates, runCommand, commandExists = oldC, oldRun, oldHave })
	candidates = func() []imageCandidate { return ladder }
	runCommand = func(name string, args ...string) ([]byte, error) {
		f, ok := out[name]
		if !ok {
			return nil, errors.New("test forgot to answer for " + name)
		}
		return f(args...)
	}
	commandExists = func(name string) bool { return have[name] }
}

// TestLadderTakesTheFirstToolThatDecodes: two tools are present, the first
// one has no picture and the second does — the answer is the second's.
func TestLadderTakesTheFirstToolThatDecodes(t *testing.T) {
	want := pngBytes(t, 4, 4)
	ladder := []imageCandidate{
		{name: "wl-paste", args: []string{"--type", "image/png"}, label: "wl-paste"},
		{name: "xclip", args: []string{"-o"}, label: "xclip"},
	}
	fakeTools(t, ladder,
		map[string]func(args ...string) ([]byte, error){
			"wl-paste": func(...string) ([]byte, error) { return nil, errors.New("Data offer: image/png not available") },
			"xclip":    func(...string) ([]byte, error) { return want, nil },
		},
		map[string]bool{"wl-paste": true, "xclip": true})

	img, err := read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if img.Bounds().Dx() != 4 {
		t.Errorf("decoded %v, want the 4px picture", img.Bounds())
	}
}

// TestLadderEmptyStdoutIsNoImageNotAFailure: a tool that answers zero bytes
// said "no picture", and the first remembered error must not be that.
func TestLadderEmptyStdoutIsNoImageNotAFailure(t *testing.T) {
	ladder := []imageCandidate{
		{name: "wl-paste", label: "wl-paste"},
		{name: "xclip", label: "xclip"},
	}
	fakeTools(t, ladder,
		map[string]func(args ...string) ([]byte, error){
			"wl-paste": func(...string) ([]byte, error) { return []byte{}, nil },
			"xclip":    func(...string) ([]byte, error) { return nil, errors.New("target image/png not available") },
		},
		map[string]bool{"wl-paste": true, "xclip": true})

	if _, err := read(); !errors.Is(err, ErrNoImage) {
		t.Errorf("read err = %v, want ErrNoImage", err)
	}
}

// TestLadderAbsentToolsAreNoImage: a machine with no clipboard tool is not a
// machine with a broken paste — the UI falls through to the text paste on
// ErrNoImage, which is what keeps ctrl+v typing as if nothing was added.
func TestLadderAbsentToolsAreNoImage(t *testing.T) {
	fakeTools(t, []imageCandidate{{name: "wl-paste", label: "wl-paste"}},
		map[string]func(args ...string) ([]byte, error){},
		map[string]bool{})

	if _, err := read(); !errors.Is(err, ErrNoImage) {
		t.Errorf("read err = %v, want ErrNoImage", err)
	}
}

// TestLadderGarbageIsReportedNotSwallowed: a tool that hands back bytes that
// are not a picture is a failure worth naming, because the user pasted
// something and dmcode is about to ignore it.
func TestLadderGarbageIsReportedNotSwallowed(t *testing.T) {
	fakeTools(t, []imageCandidate{{name: "wl-paste", label: "wl-paste"}},
		map[string]func(args ...string) ([]byte, error){
			"wl-paste": func(...string) ([]byte, error) { return []byte("not a png"), nil },
		},
		map[string]bool{"wl-paste": true})

	_, err := read()
	if err == nil || errors.Is(err, ErrNoImage) {
		t.Errorf("read err = %v, want a reported decode failure", err)
	}
}

// TestTempFileCandidateReadsWhatTheToolWrote: the macOS shape — the command
// writes the picture to the fixed path, and that is what decodes.
func TestTempFileCandidateReadsWhatTheToolWrote(t *testing.T) {
	tmp := t.TempDir() + "/clip.png"
	want := pngBytes(t, 3, 3)
	fakeTools(t, []imageCandidate{{name: "osascript", tempFile: tmp, label: "osascript"}},
		map[string]func(args ...string) ([]byte, error){
			"osascript": func(...string) ([]byte, error) {
				// The fake is the AppleScript: it writes the file.
				if err := os.WriteFile(tmp, want, 0o600); err != nil {
					t.Error(err)
				}
				return nil, nil
			},
		},
		map[string]bool{"osascript": true})

	img, err := read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if img.Bounds().Dx() != 3 {
		t.Errorf("decoded %v, want the 3px picture", img.Bounds())
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("the temp picture was left behind: %v", err)
	}
}

// TestTempFileIsFreshPerAttempt: a stale picture from an earlier paste must
// not pass as a fresh one, so the file is gone before the command runs.
func TestTempFileIsFreshPerAttempt(t *testing.T) {
	tmp := t.TempDir() + "/clip.png"
	if err := os.WriteFile(tmp, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeTools(t, []imageCandidate{{name: "osascript", tempFile: tmp, label: "osascript"}},
		map[string]func(args ...string) ([]byte, error){
			"osascript": func(...string) ([]byte, error) {
				// The tool "fails to write": the only way this test passes is
				// if the stale bytes were erased first, making the answer "no
				// file" rather than "old file".
				return nil, nil
			},
		},
		map[string]bool{"osascript": true})

	if _, err := read(); !errors.Is(err, ErrNoImage) {
		t.Errorf("read err = %v, want ErrNoImage over a stale temp file", err)
	}
}
