//go:build linux || darwin

package clipimg

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	// The same decoder list every other half of dmcode uses, so "does this
	// clipboard tool's answer decode" is answered identically here and in
	// imgprev.
	_ "github.com/dedomorozoff/dmcode/internal/imagedec"
)

// runCommand is how a candidate's command is executed. It is a variable
// rather than a call because the real one reaches the system clipboard — a
// global, shared resource no test may own, which is the rule the Windows half
// of this package follows with injected format readers. A test swaps this and
// the whole tool ladder runs against synthesised bytes.
var runCommand = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		// The reason a tool refused is on stderr, and "exit status 1" is not
		// it; a wl-clipboard without an image says "Data offer: image/png not
		// available", which is the difference between "no picture" and "no
		// tool".
		return out, fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// commandExists is the tool ladder's gate: a candidate whose binary is absent
// is not a failure, it is a platform without that clipboard owner. Also a
// seam, for the same reason runCommand is.
var commandExists = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// imageCandidate is one way a platform offers the clipboard's picture: a
// command whose stdout is image bytes, or — when tempFile is set — a command
// told to write the picture to that exact path.
type imageCandidate struct {
	name     string
	args     []string
	tempFile string
	label    string
}

// candidates is this platform's ladder, in the order they are tried — a
// var so a test can stand the platform's tools up and knock them down
// without a real clipboard, which is the rule the package lives by.
var candidates = realCandidates

// realCandidates builds the ladder. A function rather than a table because
// the macOS shape needs a fresh temp path per attempt, and no test may write
// into a fixed one.
func realCandidates() []imageCandidate {
	var out []imageCandidate
	switch runtime.GOOS {
	case "linux":
		// Wayland first: on a Wayland session the X11 clipboards are mirrors
		// at best, and wl-clipboard is the tool that owns the selection.
		out = append(out,
			imageCandidate{name: "wl-paste", args: []string{"--type", "image/png"}, label: "wl-paste"},
			// xclip answers the CLIPBOARD selection, not PRIMARY: ctrl+c puts
			// a picture where ctrl+v looks, same as text.
			imageCandidate{name: "xclip", args: []string{"-selection", "clipboard", "-t", "image/png", "-o"}, label: "xclip"},
		)
	case "darwin":
		// The pasteboard is asked to write its PNG representation to a path
		// this side chose — never to a path the script reports back, because
		// then the read would follow whatever stdout said.
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("dmcode-clip-%d.png", os.Getpid()))
		script := `on run argv` + "\n" +
			`set t to item 1 of argv` + "\n" +
			`set f to open for access POSIX file t with write permission` + "\n" +
			`write (the clipboard as «class PNGf») to f` + "\n" +
			`close access f` + "\n" +
			`end run`
		out = append(out, imageCandidate{
			name: "osascript", args: []string{"-e", script, tmp}, tempFile: tmp, label: "osascript",
		})
	}
	return out
}

// read walks the platform's candidates and returns the first one that
// decodes.
//
// "First that decodes", not "first that ran": the same lesson firstImage took
// on Windows. A clipboard tool can answer a format it cannot render — an
// empty stdout, a one-byte newline, a zero-length file — and each of those is
// "no image here" rather than a failure worth reporting. Only bytes that
// decode are a picture.
func read() (image.Image, error) {
	var firstErr error
	for _, c := range candidates() {
		if !commandExists(c.name) {
			continue
		}
		data, err := run(c)
		if err == nil && len(bytes.TrimSpace(data)) == 0 {
			err = ErrNoImage
		}
		if err != nil {
			if firstErr == nil && err != ErrNoImage {
				firstErr = err
			}
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			// The tool handed over bytes that are not a picture. Worth
			// remembering, because a later candidate may still work.
			if firstErr == nil {
				firstErr = fmt.Errorf("the clipboard image is not something dmcode can read: %w", err)
			}
			os.Remove(c.tempFile)
			continue
		}
		// Removed here rather than deferred: read returns through several of
		// these loops, and the file is the clipboard's picture, not something
		// to keep.
		os.Remove(c.tempFile)
		return img, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNoImage
}

// run executes one candidate and returns its image bytes.
func run(c imageCandidate) ([]byte, error) {
	if c.tempFile == "" {
		data, err := runCommand(c.name, c.args...)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.label, err)
		}
		return data, nil
	}
	// A stale picture from an earlier paste must not pass as a fresh one: the
	// file is removed before the command runs, so what is read back afterwards
	// was written by this attempt.
	os.Remove(c.tempFile)
	if _, err := runCommand(c.name, c.args...); err != nil {
		return nil, fmt.Errorf("%s: %w", c.label, err)
	}
	data, err := os.ReadFile(c.tempFile)
	if os.IsNotExist(err) {
		// The pasteboard had no PNG to write and the script said so by not
		// creating the file.
		return nil, ErrNoImage
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.label, err)
	}
	return data, nil
}
