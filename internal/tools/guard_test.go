package tools

import (
	"strings"
	"testing"
)

// The gate in front of the shell.
//
// The list is a speed bump, not a wall, and these tests are about the two
// halves of that: the commands that must never run, and the commands that
// look like them and must.

// TestTheCatastrophicCommandsAreRefused pins the list itself. A pattern that
// quietly stops matching is a gate that has been left open, and nothing else
// in the build would notice.
func TestTheCatastrophicCommandsAreRefused(t *testing.T) {
	for _, cmd := range []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -fr /",
		"rm --recursive --force /",
		"rm -rf ~",
		"rm -rf $HOME",
		"rm -rf .",
		"rm -rf *",
		"rm -rf C:\\",
		"rm -rf / --no-preserve-root",
		"sudo rm -rf /",
		"rm -rf /usr",
		"mkfs.ext4 /dev/sda1",
		"mkswap /dev/sda2",
		"dd if=/dev/zero of=/dev/sda",
		"dd if=image.iso of=/dev/sdb",
		"cat image.iso > /dev/sda",
		"git push --force origin main",
		"git push -f",
		"git clean -fdx",
		"git clean -fd",
		"git reset --hard HEAD~1",
		"format c:",
		"diskpart",
		":(){ :|:& };:",
		"cd build && rm -rf /",
		"echo hi; rm -rf /",
	} {
		if _, ok := refuseCommand(cmd); !ok {
			t.Errorf("the command %q was not refused", cmd)
		}
	}
}

// TestTheOrdinaryCommandsStillRun is the other half, and the more important
// one: a gate that refuses ordinary work is worse than no gate, because the
// agent stops trusting it and starts looking for a way around it.
func TestTheOrdinaryCommandsStillRun(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...",
		"rm -rf node_modules",
		"rm -rf ./dist",
		"rm -rf /home/alexl/build",
		"rm file.txt",
		"git status",
		"git commit -m 'a message'",
		"git push origin main",
		"git reset --soft HEAD~1",
		"git clean -n",
		"grep -r 'rm -rf /' .",
		"echo 'rm -rf /'",
		"cat notes.md",
		"mkdir -p a/b && cd a/b",
		"dd if=in.iso of=out.iso",
		"sudo apt install -y something",
		"FOO=bar make build",
	} {
		if r, ok := refuseCommand(cmd); ok {
			t.Errorf("the command %q was refused as %s", cmd, r.what)
		}
	}
}

// TestTheRefusalSaysWhatToDoInstead: a refusal that only says "no" leaves the
// model to invent a workaround, and the workaround is usually worse than the
// thing that was refused.
func TestTheRefusalSaysWhatToDoInstead(t *testing.T) {
	r, ok := refuseCommand("rm -rf /")
	if !ok {
		t.Fatal("the command was not refused")
	}
	if r.what == "" {
		t.Error("the refusal does not say what it refused")
	}
	if r.instead == "" {
		t.Error("the refusal does not say what to do instead")
	}
	if !strings.Contains(r.instead, "delete_file") {
		t.Errorf("the refusal does not point at the reversible instrument: %q", r.instead)
	}
}

// TestTheCommandWordIsWhatIsJudged: the shell's own separators split a line
// into commands, and only the command word decides what a piece is. A search
// for a dangerous string is a search, and a sentence about one is a sentence.
func TestTheCommandWordIsWhatIsJudged(t *testing.T) {
	for _, seg := range []string{
		"grep -r 'rm -rf /' .",
		"echo \"rm -rf /\"",
		"rg 'mkfs' .",
		"cat docs/rm -rf.md",
	} {
		if r, ok := refuseCommand(seg); ok {
			t.Errorf("the segment %q was refused as %s", seg, r.what)
		}
	}
}

// TestTheRefusalHappensBeforeTheShellRuns: a gate that runs after the command
// is a gate that runs too late. The tool returns an error and nothing is
// executed.
func TestTheRefusalHappensBeforeTheShellRuns(t *testing.T) {
	dir := t.TempDir()
	if err := SetRoot(dir); err != nil {
		t.Fatal(err)
	}
	// A command that would leave a file behind, if it ran.
	_, err := runCommand(nil, runCommandArgs{Command: "rm -rf / && echo ran > " + dir + "/ran.txt"})
	if err == nil {
		t.Fatal("the command was not refused")
	}
	if !strings.Contains(err.Error(), "will not run") {
		t.Errorf("the error is %q, want it to say the command was refused", err)
	}
}
