package tools

import (
	"path/filepath"
	"regexp"
	"strings"
)

// The commands that are not run.
//
// `run_command` is the one instrument that is not confined to the workspace:
// the shell it starts can reach anything the user can, and the boundary that
// `resolve` enforces for every other tool stops at the arguments those tools
// take. That is a real hole, and closing it properly needs a sandbox rather
// than a list.
//
// What this file is: a speed bump in front of the commands that are
// *catastrophic* rather than merely wrong. `rm -rf /` is not a mistake an
// agent should be able to make by accident, and the cost of refusing it is
// one round trip asking the user to run it themselves.
//
// What this file is not: a guarantee. A shell can delete a tree in a hundred
// ways this list does not name, and a list that pretends otherwise is worse
// than no list at all, because a list is read as a wall. The tool's own
// description says so, and so does the code that uses it.

// refusal is why a command line was not run.
type refusal struct {
	// what names the pattern in words, so the model can say what it wanted to
	// do and why it did not happen.
	what string
	// instead is what it can do instead. A refusal that only says "no" leaves
	// the model to invent a workaround, and the workaround is usually worse
	// than the thing that was refused.
	instead string
}

// refuseCommand reports why a command line must not run, if it must not.
//
// The line is split on the shell's own separators and each piece is judged by
// its *command word*, not by the text it contains. That distinction is the
// whole of it: `grep -r "rm -rf /" .` is a search and `echo "rm -rf /"` is a
// sentence, and neither of them deletes anything, while `cd build && rm -rf /`
// does. A matcher that looked at the text would refuse the first two and
// might still let the third through.
func refuseCommand(line string) (refusal, bool) {
	for _, seg := range splitSegments(line) {
		if r, ok := refuseSegment(seg); ok {
			return r, true
		}
	}
	return refusal{}, false
}

// splitSegments breaks a command line on the shell's separators.
var segmentSep = regexp.MustCompile(`(?m)\s*(?:;|&&|\|\||\||\n)\s*`)

func splitSegments(line string) []string {
	parts := segmentSep.Split(line, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// commandWords returns the words of a segment: the command, then its
// arguments.
//
// The words are read as the shell would read them — quotes stripped, so
// `rm "-rf" /` is the same command as `rm -rf /` — and the prefixes that
// wrap another command are skipped, because `sudo rm -rf /` is the same
// command with a ticket attached.
func commandWords(seg string) []string {
	fields := splitFields(seg)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f)
	}
	// Skip the wrappers: a privilege escalation, an environment, and any
	// variable assignment, none of which is the command.
	for len(out) > 0 {
		head := baseName(out[0])
		switch {
		case head == "sudo" || head == "doas" || head == "env" || head == "nohup" || head == "time":
			out = out[1:]
		case strings.Contains(out[0], "=") && !strings.HasPrefix(out[0], "-") && !strings.Contains(out[0], "/"):
			out = out[1:]
		default:
			return out
		}
	}
	return out
}

// splitFields splits a segment into words, honouring the quotes a shell
// honours.
func splitFields(seg string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote byte
		open  bool
	)
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			open = true
		case c == ' ' || c == '\t':
			if cur.Len() > 0 || open {
				out = append(out, cur.String())
				cur.Reset()
				open = false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 || open {
		out = append(out, cur.String())
	}
	return out
}

// baseName is the command's own name, without the directory it sits in.
func baseName(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	return filepath.Base(s)
}

// refuseSegment judges one piece of a command line.
func refuseSegment(seg string) (refusal, bool) {
	words := commandWords(seg)
	if len(words) == 0 {
		return refusal{}, false
	}
	cmd := baseName(words[0])
	args := words[1:]

	// The fork bomb is not a command word, it is a whole line, and it is
	// matched before anything else because it is the one that hangs the
	// machine rather than emptying it.
	if isForkBomb(seg) {
		return refusal{
			what:    "a fork bomb",
			instead: "there is nothing to undo about this one, and no dmcode tool can do it for you",
		}, true
	}

	switch cmd {
	case "rm", "rmdir", "del", "erase":
		if isRecursive(args) && hasRootTarget(args) {
			return refusal{
				what:    "a recursive delete of a root directory",
				instead: "delete the files one by one with delete_file, which counts them in the session's changes and undoes them with ctrl+z",
			}, true
		}
	case "mkswap", "newfs":
		return refusal{
			what:    "a filesystem format",
			instead: "there is no undo for this in dmcode; if you meant it, ask the user to run it",
		}, true
	case "dd":
		if writesToDevice(args) {
			return refusal{
				what:    "a raw write to a device",
				instead: "there is no undo for this in dmcode; if you meant it, ask the user to run it",
			}, true
		}
	case "git":
		return refuseGit(args)
	case "format", "diskpart":
		return refusal{
			what:    "a disk format",
			instead: "there is no undo for this in dmcode; if you meant it, ask the user to run it",
		}, true
	}
	// mkfs.ext4, mkfs.xfs and the rest of the family: the command word carries
	// a suffix, and a match on the bare name would let the same format through
	// under a different one.
	if strings.HasPrefix(cmd, "mkfs") {
		return refusal{
			what:    "a filesystem format",
			instead: "there is no undo for this in dmcode; if you meant it, ask the user to run it",
		}, true
	}

	// A redirection into a device is a raw write whatever command wrote it.
	if writesToDevice(args) {
		return refusal{
			what:    "a raw write to a device",
			instead: "there is no undo for this in dmcode; if you meant it, ask the user to run it",
		}, true
	}
	return refusal{}, false
}

// isRecursive reports whether the arguments ask for a recursive delete.
func isRecursive(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		flags := strings.ToLower(a)
		// A lone "-" is a file name, not a flag, and "--" ends the flags.
		if flags == "--" {
			break
		}
		if strings.Contains(flags, "r") {
			return true
		}
	}
	return false
}

// rootTargets are the places a recursive delete must never be aimed at.
//
// The list is deliberately short and deliberately literal: a path that *looks*
// like a home directory but is not one — `/home/alexl/build` — is fine, and a
// rule that tried to be clever about it would refuse ordinary work.
var rootTargets = map[string]bool{
	"/":       true,
	"/*":      true,
	"//":      true,
	"~":       true,
	"~/":      true,
	"$HOME":   true,
	"${HOME}": true,
	".":       true,
	"./":      true,
	"..":      true,
	"../":     true,
	"*":       true,
	".*":      true,
}

// systemRoots are the machine's own directories, and a recursive delete must
// never be aimed at one of them.
//
// They are the machine's, not the user's: `/home/alexl/build` is a build
// directory and `/etc` is the system's configuration, and the difference is
// one path segment.
var systemRoots = map[string]bool{
	"/applications": true, "/bin": true, "/boot": true, "/dev": true,
	"/etc": true, "/home": true, "/lib": true, "/library": true,
	"/opt": true, "/proc": true, "/program files": true, "/programdata": true,
	"/root": true, "/sbin": true, "/srv": true, "/sys": true, "/system": true,
	"/users": true, "/usr": true, "/var": true, "/windows": true,
}

// isSystemTarget reports whether a path names one of the machine's own
// directories, or the person's home.
//
// The home is one segment further down: `/home/alexl` is the user's life and
// `/home/alexl/build` is a build directory, and the rule has to tell them
// apart.
func isSystemTarget(t string) bool {
	p := strings.ToLower(strings.TrimRight(strings.ReplaceAll(t, "\\", "/"), "/"))
	if p == "" {
		return false
	}
	if systemRoots[p] {
		return true
	}
	// A home directory: one segment under /home, /Users or C:/Users.
	for _, home := range []string{"/home", "/users", "c:/users"} {
		if !strings.HasPrefix(p, home+"/") {
			continue
		}
		rest := strings.TrimPrefix(p, home+"/")
		if rest != "" && !strings.Contains(rest, "/") {
			return true
		}
	}
	return false
}

// hasRootTarget reports whether one of the arguments is a root-ish target.
//
// A path that is *under* a root is not one: `rm -rf /home/alexl/build` names
// a directory, and a rule that refused it would make the tool useless for
// cleaning up. The system's own directories are the exception, and they are
// named one by one rather than guessed at.
func hasRootTarget(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		t := strings.TrimSpace(a)
		if rootTargets[t] {
			return true
		}
		// A drive root: C:\ or C:/.
		if len(t) == 3 && t[1] == ':' && (t[2] == '\\' || t[2] == '/') {
			return true
		}
		// A glob that reaches the root: /*, ~/*, $HOME/*.
		if strings.HasSuffix(t, "/*") {
			base := strings.TrimSuffix(t, "/*")
			if base == "" || base == "~" || base == "$HOME" || base == "${HOME}" {
				return true
			}
		}
		if isSystemTarget(t) {
			return true
		}
	}
	return false
}

// isDevicePath reports whether a path names a raw device.
func isDevicePath(p string) bool {
	low := strings.ToLower(p)
	if strings.HasPrefix(low, "/dev/") && !strings.HasPrefix(low, "/dev/null") {
		return true
	}
	if strings.HasPrefix(low, "\\\\.\\physicaldrive") {
		return true
	}
	return false
}

// writesToDevice reports whether the arguments write to a raw device.
//
// A block device is `/dev/sda` and a Windows disk is `\\.\PhysicalDrive0`;
// both are the shape that takes a whole disk with it. A redirection counts:
// `cat image.iso > /dev/sda` is a raw write whatever command wrote it.
func writesToDevice(args []string) bool {
	for i, a := range args {
		low := strings.ToLower(a)
		if strings.HasPrefix(low, "of=/dev/") && !strings.HasPrefix(low, "of=/dev/null") {
			return true
		}
		if strings.HasPrefix(low, "of=\\.\\physicaldrive") {
			return true
		}
		// A redirection: ">" and then the path it writes to.
		if low == ">" || low == ">>" || low == "1>" || low == "2>" {
			if i+1 < len(args) && isDevicePath(args[i+1]) {
				return true
			}
		}
		if strings.HasPrefix(low, ">") && isDevicePath(strings.TrimLeft(low, ">")) {
			return true
		}
	}
	return false
}

// refuseGit judges a git command.
//
// Only three of them are refused, and each for a different reason: a force
// push reaches a remote the workspace does not own, a forced clean takes the
// untracked files with it, and a hard reset throws away the work in the tree
// — which dmcode can undo, but only through its own journal.
func refuseGit(args []string) (refusal, bool) {
	if len(args) == 0 {
		return refusal{}, false
	}
	switch args[0] {
	case "push":
		for _, a := range args[1:] {
			if a == "--force" || a == "-f" {
				return refusal{
					what:    "a force push",
					instead: "ask the user to run it; a remote is not the workspace, and dmcode does not push for them",
				}, true
			}
		}
	case "clean":
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "-") && strings.Contains(strings.ToLower(a), "f") {
				return refusal{
					what:    "a forced clean",
					instead: "delete the files with delete_file, so the change is counted and can be undone",
				}, true
			}
		}
	case "reset":
		for _, a := range args[1:] {
			if a == "--hard" {
				return refusal{
					what:    "a hard reset",
					instead: "ctrl+z rewinds the files a turn changed, or ask the user to reset by hand",
				}, true
			}
		}
	}
	return refusal{}, false
}

// isForkBomb reports whether a segment is the shell fork bomb.
//
// It is matched loosely on purpose: the classic form is `:(){ :|:& };:`, and
// the spaces and the semicolons move around.
func isForkBomb(seg string) bool {
	s := strings.Join(strings.Fields(seg), "")
	return strings.Contains(s, ":(){") || strings.Contains(s, ":|:&") || strings.Contains(s, "(){")
}
