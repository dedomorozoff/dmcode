// Package tools implements the agent's instruments and the workspace boundary
// every one of them is confined to.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

const maxReadBytes = 256 * 1024
const maxWalkFiles = 2000

// root is the directory the session is scoped to. It is empty until SetRoot
// runs, which is the "no boundary configured" case every tool tolerates: the
// agent still works, it just may reach outside.
var (
	rootMu sync.RWMutex
	root   string
)

// SetRoot scopes the tools to dir. Every subsequent path argument is resolved
// against the process working directory first and then checked against this
// root, so a relative path and the absolute path it denotes are treated alike.
//
// A non-nil error means the session carries no boundary, and the reason why —
// the caller decides whether that is fatal.
func SetRoot(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("cannot resolve the working directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("cannot use the working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}
	// Follow symlinks on the root itself so a link in the path does not make
	// every check below compare against a different spelling of the same tree.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rootMu.Lock()
	root = abs
	rootMu.Unlock()
	return nil
}

// Root reports the directory the tools are confined to, or "" when no boundary
// is configured.
func Root() string {
	rootMu.RLock()
	defer rootMu.RUnlock()
	return root
}

// ErrOutsideRoot is returned when a path argument leaves the workspace. The
// message names both directories so the user can see where the agent tried to
// go, and the fix is obvious from the text alone.
var ErrOutsideRoot = fmt.Errorf("outside the working directory")

// resolve validates a tool path argument and returns the absolute path to use.
// An empty p means the working directory itself.
//
// A relative path is interpreted against the workspace root rather than against
// the process directory. The two are the same in a normal session, but resolving
// against the root is what makes /cd and the boundary impossible to disagree: a
// path means the same thing no matter which directory the process happens to be
// sitting in.
//
// The check is lexical on purpose: resolve() must decide before the file is
// touched, and a path that does not exist yet (write_file) has no symlink to
// resolve. A symlink inside the tree is therefore the known soft spot — the
// alternative is refusing to create any file that does not exist yet, which
// would break write_file outright.
func resolve(p string) (string, error) {
	if p == "" {
		p = "."
	}
	base := Root()
	if base != "" && !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %w", p, err)
	}
	if base == "" {
		return abs, nil
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		// Different volumes on Windows, so there is no relative path at all;
		// that can only mean the two are unrelated trees.
		return "", fmt.Errorf("%s is %w: %s", p, ErrOutsideRoot, base)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is %w: %s", p, ErrOutsideRoot, base)
	}
	return abs, nil
}

// checkPatternRoot validates the directory a glob pattern is rooted at. Only the
// part before the first wildcard is a real path — "src/**/*.go" roots at "src",
// "*.go" and "../*.go" root at the working directory and one level up — so the
// segment is trimmed to the last separator and handed to resolve.
func checkPatternRoot(pattern string) error {
	// A pattern anchored at a filesystem root is never inside the workspace.
	// It has to be caught here rather than left to resolve: on Windows
	// filepath.IsAbs treats "\etc\passwd" as *relative* (a rooted path still
	// needs a volume name to count as absolute), so resolve would join it onto
	// the root and wave it through as workspace-local.
	if strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, `\`) {
		return fmt.Errorf("%s is %w: %s", pattern, ErrOutsideRoot, Root())
	}
	cleaned := filepath.Clean(filepath.FromSlash(pattern))
	cut := strings.IndexAny(cleaned, "*?[")
	if cut < 0 {
		cut = len(cleaned)
	}
	root := cleaned[:cut]
	i := strings.LastIndexAny(root, `/\`)
	switch {
	case i < 0:
		// No separator at all ("*.go"): the pattern sits in the working
		// directory itself.
		root = "."
	case i == 0:
		// The pattern is anchored at the filesystem root ("/etc/passwd"). Keep
		// the separator: dropping it would leave an empty root, which resolve
		// reads as "." and would wave the pattern through as workspace-local.
		root = cleaned[:1]
	default:
		root = root[:i]
	}
	_, err := resolve(root)
	return err
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset_line"`
	Limit  int    `json:"limit_lines"`
}
type readFileResult struct {
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
	Truncated  bool   `json:"truncated"`
}

func readFile(ctx agent.Context, in readFileArgs) (readFileResult, error) {
	path, err := resolve(in.Path)
	if err != nil {
		return readFileResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return readFileResult{}, err
	}
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
	}
	lines := strings.Split(string(data), "\n")
	total := len(lines)
	start := max(in.Offset, 0)
	if start >= total {
		return readFileResult{Content: "", TotalLines: total}, nil
	}
	end := total
	trunc := false
	if in.Limit > 0 && start+in.Limit < total {
		end = start + in.Limit
		trunc = true
	}
	if len(data) == maxReadBytes {
		trunc = true
	}
	return readFileResult{
		Content:    strings.Join(lines[start:end], "\n"),
		TotalLines: total,
		Truncated:  trunc,
	}, nil
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type writeFileResult struct {
	BytesWritten int `json:"bytes_written"`
}

func writeFile(ctx agent.Context, in writeFileArgs) (writeFileResult, error) {
	path, err := resolve(in.Path)
	if err != nil {
		return writeFileResult{}, err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return writeFileResult{}, err
		}
	}
	if err := writeFileAtomic(path, []byte(in.Content)); err != nil {
		return writeFileResult{}, err
	}
	return writeFileResult{BytesWritten: len(in.Content)}, nil
}

// writeFileAtomic writes data through a temporary file in the target directory
// and a rename, so an unexpected exit cannot leave a truncated or half-written
// file behind. os.Rename replaces an existing destination on all platforms
// dmcode supports.
//
// This is also the single point every write passes through — write_file and
// edit_file both land here — so it is where the session's change tally is kept.
// Doing it here rather than in each tool means no write can reach disk without
// being counted.
func writeFileAtomic(path string, data []byte) error {
	// What is being replaced, read before the rename. A read failure other than
	// "not there" is not fatal to the write: the tool's job is to write, and
	// refusing to overwrite a file that cannot be read would be a worse outcome
	// than a tally that undercounts. The failure is therefore swallowed and the
	// write counted as wholly additive.
	before, _ := readIfExists(path)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".dmcode-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	recordChange(path, before, string(data))
	return nil
}

type editFileArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}
type editFileResult struct {
	Replacements int `json:"replacements"`
}

func editFile(ctx agent.Context, in editFileArgs) (editFileResult, error) {
	path, err := resolve(in.Path)
	if err != nil {
		return editFileResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return editFileResult{}, err
	}
	src := string(data)
	if in.OldString == "" {
		return editFileResult{}, fmt.Errorf("old_string is empty")
	}

	// 1. Try exact match
	write := func(content string) (editFileResult, error) {
		if err := writeFileAtomic(path, []byte(content)); err != nil {
			return editFileResult{}, err
		}
		return editFileResult{}, nil
	}
	if in.ReplaceAll {
		n := strings.Count(src, in.OldString)
		if n > 0 {
			if _, err := write(strings.ReplaceAll(src, in.OldString, in.NewString)); err != nil {
				return editFileResult{}, err
			}
			return editFileResult{Replacements: n}, nil
		}
	} else {
		idx := strings.Index(src, in.OldString)
		if idx >= 0 {
			if strings.Index(src[idx+1:], in.OldString) >= 0 {
				return editFileResult{}, fmt.Errorf("old_string matches multiple locations in %s; include more context or set replace_all", in.Path)
			}
			if _, err := write(src[:idx] + in.NewString + src[idx+len(in.OldString):]); err != nil {
				return editFileResult{}, err
			}
			return editFileResult{Replacements: 1}, nil
		}
	}

	// 2. Fallback: normalize newlines (\r\n <-> \n) for Windows/Unix compatibility
	hasCRLF := strings.Contains(src, "\r\n")
	normSrc := strings.ReplaceAll(src, "\r\n", "\n")
	normOld := strings.ReplaceAll(in.OldString, "\r\n", "\n")
	normNew := strings.ReplaceAll(in.NewString, "\r\n", "\n")

	if in.ReplaceAll {
		n := strings.Count(normSrc, normOld)
		if n > 0 {
			normSrc = strings.ReplaceAll(normSrc, normOld, normNew)
			if hasCRLF {
				normSrc = strings.ReplaceAll(normSrc, "\n", "\r\n")
			}
			if _, err := write(normSrc); err != nil {
				return editFileResult{}, err
			}
			return editFileResult{Replacements: n}, nil
		}
	} else {
		idx := strings.Index(normSrc, normOld)
		if idx >= 0 {
			if strings.Index(normSrc[idx+1:], normOld) >= 0 {
				return editFileResult{}, fmt.Errorf("old_string matches multiple locations in %s; include more context or set replace_all", in.Path)
			}
			normSrc = normSrc[:idx] + normNew + normSrc[idx+len(normOld):]
			if hasCRLF {
				normSrc = strings.ReplaceAll(normSrc, "\n", "\r\n")
			}
			if _, err := write(normSrc); err != nil {
				return editFileResult{}, err
			}
			return editFileResult{Replacements: 1}, nil
		}
	}

	// 3. Fallback: whitespace-normalized, line-granular match. Models habitually
	// reproduce a snippet with the file's tabs swapped for spaces or an
	// indentation level off by one; the words are right, the spacing is not.
	// The window whose lines agree once whitespace runs are collapsed is taken
	// as the match, and new_string's indentation is replaced with the file's
	// own, so the edit lands the way the file was formatted.
	count, out, err := fuzzyReplace(src, in.OldString, in.NewString, in.ReplaceAll)
	if err != nil {
		return editFileResult{}, err
	}
	if count == 1 {
		if _, err := write(out); err != nil {
			return editFileResult{}, err
		}
		return editFileResult{Replacements: 1}, nil
	}
	if count > 1 && in.ReplaceAll {
		if _, err := write(out); err != nil {
			return editFileResult{}, err
		}
		return editFileResult{Replacements: count}, nil
	}
	if count > 1 {
		return editFileResult{}, fmt.Errorf("old_string matches multiple locations in %s; include more context or set replace_all", in.Path)
	}
	return editFileResult{}, fmt.Errorf("old_string not found in %s", in.Path)
}

// leadingWS returns the indentation prefix of a single line.
func leadingWS(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

// fuzzyReplace finds matches of oldS in src where every line agrees once
// whitespace runs are collapsed, and returns the file with each match replaced
// by newS. Matching is whole-line: a window of consecutive file lines must
// correspond line for line to old_string, which keeps surrounding text on the
// first and last lines intact. Returned count is the number of matches found;
// out is meaningful only when count is 1 or replaceAll was requested (count > 1).
func fuzzyReplace(src, oldS, newS string, replaceAll bool) (int, string, error) {
	if strings.TrimSpace(oldS) == "" {
		return 0, "", fmt.Errorf("old_string is empty")
	}
	crlf := strings.Contains(src, "\r\n")
	lines := strings.Split(src, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	split := func(s string) []string {
		return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
	}
	oldLines := split(oldS)
	newLines := split(newS)

	norm := make([]string, len(lines))
	for i, l := range lines {
		norm[i] = strings.Join(strings.Fields(l), " ")
	}
	normOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		normOld[i] = strings.Join(strings.Fields(l), " ")
	}

	type span struct{ start, end int }
	var spans []span
	for i := 0; i+len(normOld) <= len(norm); i++ {
		ok := true
		for k := range normOld {
			if norm[i+k] != normOld[k] {
				ok = false
				break
			}
		}
		if ok {
			spans = append(spans, span{i, i + len(normOld)})
			if !replaceAll {
				if len(spans) > 1 {
					break
				}
				i += len(normOld) - 1
			} else {
				i += len(normOld) - 1
			}
		}
	}
	if len(spans) == 0 {
		return 0, "", nil
	}

	var out []string
	prev := 0
	for _, sp := range spans {
		out = append(out, lines[prev:sp.start]...)
		window := lines[sp.start:sp.end]
		block := make([]string, len(newLines))
		for k, nl := range newLines {
			if strings.Join(strings.Fields(nl), " ") == "" {
				block[k] = ""
				continue
			}
			ws := ""
			if k < len(window) {
				ws = leadingWS(window[k])
			}
			if ws == "" {
				// Expanding one file line into several: give the followers the
				// indentation of the line they grow out of.
				ws = leadingWS(window[0])
			}
			block[k] = ws + strings.TrimLeft(nl, " \t")
		}
		out = append(out, block...)
		prev = sp.end
	}
	out = append(out, lines[prev:]...)
	joined := strings.Join(out, "\n")
	if crlf {
		joined = strings.ReplaceAll(joined, "\n", "\r\n")
	}
	return len(spans), joined, nil
}

// isIgnoredDir returns true if directory should be skipped (VCS / vendor / caches).
func isIgnoredDir(name string) bool {
	switch name {
	case ".git", ".crush", ".hg", ".svn", "node_modules", ".idea", ".vscode", "vendor":
		return true
	default:
		return false
	}
}

type listDirArgs struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}
type listDirResult struct {
	Entries string `json:"entries"`
}

func listDir(ctx agent.Context, in listDirArgs) (listDirResult, error) {
	dir, err := resolve(in.Path)
	if err != nil {
		return listDirResult{}, err
	}
	var b strings.Builder
	if !in.Recursive {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return listDirResult{}, err
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return entries[i].Name() < entries[j].Name()
		})
		for _, e := range entries {
			if e.IsDir() && isIgnoredDir(e.Name()) {
				continue
			}
			if e.IsDir() {
				b.WriteString(e.Name() + "/\n")
			} else {
				b.WriteString(e.Name() + "\n")
			}
		}
		return listDirResult{Entries: b.String()}, nil
	}
	count := 0
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count > maxWalkFiles {
			b.WriteString("... (truncated)\n")
			return filepath.SkipAll
		}
		b.WriteString(filepath.ToSlash(path) + "\n")
		return nil
	})
	if err != nil {
		return listDirResult{}, err
	}
	return listDirResult{Entries: b.String()}, nil
}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Glob    string `json:"glob"`
}
type grepResult struct {
	Matches string `json:"matches"`
}

func grep(ctx agent.Context, in grepArgs) (grepResult, error) {
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return grepResult{}, fmt.Errorf("invalid regex: %w", err)
	}
	root, err := resolve(in.Path)
	if err != nil {
		return grepResult{}, err
	}
	var fileFilter *regexp.Regexp
	if in.Glob != "" {
		if fileFilter, err = regexp.Compile(globToRegex(in.Glob)); err != nil {
			return grepResult{}, fmt.Errorf("invalid glob: %w", err)
		}
	}
	var b strings.Builder
	count := 0
	skip := false
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if skip {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if fileFilter != nil && !fileFilter.MatchString(d.Name()) {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil || len(data) > maxReadBytes {
			return nil
		}
		if strings.ContainsRune(string(data[:min(4096, len(data))]), 0) {
			return nil
		}
		for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if re.MatchString(line) {
				count++
				b.WriteString(fmt.Sprintf("%s:%d: %s\n", filepath.ToSlash(path), i+1, truncate(line, 200)))
				if count >= 200 {
					b.WriteString("... (more matches truncated)\n")
					skip = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return grepResult{}, err
	}
	return grepResult{Matches: b.String()}, nil
}

func globToRegex(g string) string {
	var b strings.Builder
	b.WriteString("^")
	// Normalize slashes
	g = strings.ReplaceAll(g, "\\", "/")
	runes := []rune(g)
	n := len(runes)
	for i := 0; i < n; i++ {
		// Handle "/**/"
		if i+3 < n && runes[i] == '/' && runes[i+1] == '*' && runes[i+2] == '*' && runes[i+3] == '/' {
			b.WriteString("(?:/|/.*/)")
			i += 3
			continue
		}
		// Handle leading "**/"
		if i == 0 && i+2 < n && runes[0] == '*' && runes[1] == '*' && runes[2] == '/' {
			b.WriteString("(?:.*/)?")
			i += 2
			continue
		}
		// Handle trailing "/**"
		if i+2 < n && runes[i] == '/' && runes[i+1] == '*' && runes[i+2] == '*' && i+3 == n {
			b.WriteString("(?:/.*)?")
			i += 2
			continue
		}
		// Handle standalone "**"
		if i+1 < n && runes[i] == '*' && runes[i+1] == '*' {
			b.WriteString(".*")
			i++
			continue
		}
		// Handle standalone "*"
		if runes[i] == '*' {
			b.WriteString("[^/\\\\]*")
			continue
		}
		if runes[i] == '?' {
			b.WriteString("[^/\\\\]")
			continue
		}
		switch runes[i] {
		case '.', '(', ')', '[', ']', '{', '}', '^', '$', '+', '|', '\\':
			b.WriteString("\\" + string(runes[i]))
		case '/':
			b.WriteString("[/\\\\]")
		default:
			b.WriteRune(runes[i])
		}
	}
	b.WriteString("$")
	return b.String()
}

type globArgs struct {
	Pattern string `json:"pattern"`
}
type globResult struct {
	Files string `json:"files"`
}

func glob(ctx agent.Context, in globArgs) (globResult, error) {
	pattern := in.Pattern
	// The pattern's leading directory is a real path argument and must clear the
	// workspace boundary, but the *result* stays relative: the agent reads these
	// back to pass to read_file, and "."-relative output keeps that round trip
	// working whichever directory the session was started in.
	if err := checkPatternRoot(pattern); err != nil {
		return globResult{}, err
	}
	// If pattern contains ** or path separators, perform recursive walk matching
	if strings.Contains(pattern, "**") || strings.Contains(pattern, "/") || strings.Contains(pattern, "\\") {
		rePattern, err := regexp.Compile(globToRegex(filepath.ToSlash(pattern)))
		if err != nil {
			return globResult{}, fmt.Errorf("invalid glob pattern: %w", err)
		}
		var matches []string
		err = filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if isIgnoredDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			slashPath := filepath.ToSlash(path)
			trimmed := strings.TrimPrefix(slashPath, "./")
			if rePattern.MatchString(slashPath) || rePattern.MatchString(trimmed) {
				matches = append(matches, slashPath)
			}
			return nil
		})
		if err != nil {
			return globResult{}, err
		}
		sort.Strings(matches)
		if len(matches) > maxWalkFiles {
			matches = append(matches[:maxWalkFiles], "... (truncated)")
		}
		return globResult{Files: strings.Join(matches, "\n")}, nil
	}

	matches, err := filepath.Glob(in.Pattern)
	if err != nil {
		return globResult{}, err
	}
	sort.Strings(matches)
	if len(matches) > maxWalkFiles {
		matches = append(matches[:maxWalkFiles], "... (truncated)")
	}
	return globResult{Files: strings.Join(matches, "\n")}, nil
}

func detectShell() (string, []string) {
	if runtime.GOOS == "windows" {
		// Prefer pwsh (PowerShell 7) if installed
		if p, err := exec.LookPath("pwsh"); err == nil {
			return p, []string{"-NoProfile", "-NonInteractive", "-Command"}
		}
		// Check for Git Bash or Cygwin bash explicitly
		for _, bashPath := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\cygwin64\bin\bash.exe`,
			`C:\msys64\usr\bin\bash.exe`,
		} {
			if _, err := os.Stat(bashPath); err == nil {
				return bashPath, []string{"-c"}
			}
		}
		// Fallback to Windows PowerShell
		if p, err := exec.LookPath("powershell"); err == nil {
			return p, []string{"-NoProfile", "-NonInteractive", "-Command"}
		}
		return "cmd.exe", []string{"/C"}
	}

	// Unix / POSIX
	if s := os.Getenv("SHELL"); s != "" {
		return s, []string{"-c"}
	}
	if _, err := os.Stat("/bin/sh"); err == nil {
		return "/bin/sh", []string{"-c"}
	}
	return "sh", []string{"-c"}
}

type runCommandArgs struct {
	Command  string `json:"command"`
	WorkDir  string `json:"work_dir"`
	TimeoutS int    `json:"timeout_seconds"`
}
type runCommandResult struct {
	Output string `json:"output"`
}

func runCommand(ctx agent.Context, in runCommandArgs) (runCommandResult, error) {
	timeout := time.Duration(in.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	workDir := in.WorkDir
	if workDir == "" {
		workDir = "."
	}
	dir, err := resolve(workDir)
	if err != nil {
		return runCommandResult{}, err
	}
	shell, flags := detectShell()

	parentCtx := context.Background()
	if ctx != nil {
		parentCtx = ctx
	}
	cmdCtx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	args := append(append([]string{}, flags...), in.Command)
	cmd := exec.CommandContext(cmdCtx, shell, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := runCommandResult{Output: truncate(string(out), 20000)}
	if cmdCtx.Err() != nil {
		if parentCtx.Err() != nil {
			result.Output += "\n[command cancelled by user]"
			return result, nil
		}
		result.Output += fmt.Sprintf("\n[command timed out after %s]", timeout)
		return result, nil
	}
	if err != nil {
		result.Output += fmt.Sprintf("\n[command failed: %v]", err)
	}
	return result, nil
}

func MakeTools() ([]tool.Tool, error) {
	readFileTool, err := functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Reads a file. Optional offset_line and limit_lines for large files. Returns content, total line count and whether it was truncated.",
	}, readFile)
	if err != nil {
		return nil, err
	}
	writeFileTool, err := functiontool.New(functiontool.Config{
		Name:        "write_file",
		Description: "Creates or overwrites a file with the given content. Creates parent directories.",
	}, writeFile)
	if err != nil {
		return nil, err
	}
	editFileTool, err := functiontool.New(functiontool.Config{
		Name:        "edit_file",
		Description: "Replaces old_string with new_string in a file. old_string must be unique unless replace_all is true; otherwise returns an error. If the exact text is absent, a whitespace-tolerant match is tried: indentation and spacing differences are ignored and new_string adopts the file's indentation.",
	}, editFile)
	if err != nil {
		return nil, err
	}
	listDirTool, err := functiontool.New(functiontool.Config{
		Name:        "list_dir",
		Description: "Lists directory entries, skipping hidden files. Set recursive=true to walk the tree.",
	}, listDir)
	if err != nil {
		return nil, err
	}
	grepTool, err := functiontool.New(functiontool.Config{
		Name:        "grep",
		Description: "Regex search across files under path (default cwd). Optional filename glob filter like '*.go'. Returns path:line: match.",
	}, grep)
	if err != nil {
		return nil, err
	}
	globTool2, err := functiontool.New(functiontool.Config{
		Name:        "glob",
		Description: "Lists files matching a shell glob pattern like 'src/*.go' or '*.md'.",
	}, glob)
	if err != nil {
		return nil, err
	}
	runCommandTool, err := functiontool.New(functiontool.Config{
		Name:        "run_command",
		Description: "Runs a shell command and returns combined stdout/stderr (truncated). Timeout in seconds, default 60, capped at 600.",
	}, runCommand)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{readFileTool, writeFileTool, editFileTool, listDirTool, grepTool, globTool2, runCommandTool}, nil
}

// readOnlyNames are the tools a planning turn may use. run_command is absent on
// purpose: a shell can write a file through >, Out-File, tee or touch, so
// "read-only" cannot be enforced from the prompt alone and the tool is withheld
// instead of trusted.
var readOnlyNames = map[string]bool{
	"read_file": true,
	"list_dir":  true,
	"grep":      true,
	"glob":      true,
}

// MakeReadOnlyTools returns the subset MakeTools builds that only inspects the
// workspace. The functions themselves are the same ones MakeTools uses, so the
// plan mode is the same agent with less reach — not a different implementation
// that could drift from the real one.
func MakeReadOnlyTools() ([]tool.Tool, error) {
	all, err := MakeTools()
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(readOnlyNames))
	for _, t := range all {
		if readOnlyNames[t.Name()] {
			out = append(out, t)
		}
	}
	return out, nil
}

// ToolNames lists the names of ts, in order, for the sidebar and the plan check.
func ToolNames(ts []tool.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

// truncate shortens s to at most n bytes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
