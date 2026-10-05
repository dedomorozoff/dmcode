// Package tools implements the agent's instruments and the workspace boundary
// every one of them is confined to.
package tools

import (
	"context"
	"errors"
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

	"github.com/dedomorozoff/dmcode/internal/pathnorm"
	"github.com/dedomorozoff/dmcode/internal/todo"
)

const maxReadBytes = 256 * 1024

// Paging bounds for the tools that walk a tree. defaultPageItems is what a
// caller gets without asking for a size; maxPageItems is the most one call may
// return however it asks.
const (
	defaultPageItems = 200
	maxPageItems     = 2000
)

// maxScannedItems bounds how many results a walk will collect before it gives
// up counting. It exists to bound memory, not to keep responses small — a
// caller paging through a result set never allocates more than one page, but
// the scan behind it does have to remember every hit it counted.
//
// At a hundred thousand hits this is a pattern that matched nearly every line
// of the repository, which is a request no agent makes on purpose. Past the
// ceiling `total` reports what the scan saw, which is why the tool
// descriptions call the ceiling out rather than leaving it implicit.
const maxScannedItems = 100000

// maxContextLines bounds grep's context_lines. Each match may drag 2n+1 lines
// into the response, so an unbounded n turns one page into the whole file.
const maxContextLines = 20

// maxGrepRenderedLines bounds the rendered output of one grep page. Matches are
// capped by the page size, but context is not, so this is the guard that keeps
// a generous context setting from producing an unbounded answer.
const maxGrepRenderedLines = 4000

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
//
// A *failed* lexical check is not final, because the root is stored with its
// links followed and the argument may not be: SetRoot canonicalises its side and
// nothing canonicalises this one, so a path spelled /var/... against a root
// spelled /private/var/... — or an 8.3 short name against its long form — reads
// as a stranger to a boundary that is looking at the very same directory. So a
// refusal is retried with both sides in one spelling, which is pathnorm's whole
// job. The path handed back is still the caller's: canonical is only ever
// something to compare, never something to open.
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
	if err := insideRoot(base, abs); err != nil {
		if real := pathnorm.Canonical(abs); real != abs && insideRoot(base, real) == nil {
			return abs, nil
		}
		return "", fmt.Errorf("%s is %w: %s", p, ErrOutsideRoot, base)
	}
	return abs, nil
}

// insideRoot reports whether abs sits under base. It is the lexical half of
// resolve, kept separate so the symlink-aware re-check below can reuse it.
func insideRoot(base, abs string) error {
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		// Different volumes on Windows, so there is no relative path at all;
		// that can only mean the two are unrelated trees.
		return fmt.Errorf("%s is %w: %s", abs, ErrOutsideRoot, base)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is %w: %s", abs, ErrOutsideRoot, base)
	}
	return nil
}

// withinRootAfterLinks re-checks an existing filesystem entry against the
// boundary after following its symlinks. resolve() is lexical on purpose — it
// must decide before the file is touched, and a path that does not exist yet
// has no link to resolve — so the tools that actually open something call this
// to close the gap the lexical check leaves open: a symlink inside the tree
// that points out of it.
func withinRootAfterLinks(abs string) error {
	base := Root()
	if base == "" {
		return nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Nothing to follow: a missing path is write_file's normal case, and
		// the lexical check already covered what exists of it.
		return nil
	}
	if err := insideRoot(base, real); err != nil {
		return fmt.Errorf("%s is %w: %s", abs, ErrOutsideRoot, base)
	}
	return nil
}

// ResolveExisting validates a path a *user* named rather than one a tool was
// called with, and returns the absolute path of an existing file inside the
// workspace.
//
// It is exported for the image attachments, which are chosen by the user typing
// or dropping a path rather than by the agent. The boundary applies to them for
// the same reason it applies to read_file: a path that leaves the workspace
// turns a preview of "a screenshot" into a preview of anything on the disk, and
// the picture then goes out to a model. Both halves of resolve's job are here —
// the lexical check and the re-check after symlinks — because unlike
// write_file this path must exist, so the second check has nothing to excuse.
//
// The workspace can be left behind deliberately: screenshots live in
// ~/Pictures or on the Desktop as often as in the project. That is what
// AllowOutside is for.
func ResolveExisting(p string, allowOutside bool) (string, error) {
	abs, err := resolve(p)
	if err != nil {
		if !allowOutside || !errors.Is(err, ErrOutsideRoot) {
			return "", err
		}
		// Re-resolve without the boundary rather than reaching past resolve:
		// filepath.Abs on its own is the whole of what resolve adds when no root
		// is set, and going through it keeps the two paths agreeing on what a
		// relative path means.
		if !filepath.IsAbs(p) {
			p = filepath.Join(Root(), p)
		}
		if abs, err = filepath.Abs(p); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a folder, not an image", p)
	}
	if err := withinRootAfterLinks(abs); err != nil && !allowOutside {
		return "", err
	}
	return abs, nil
}

// existingAncestor returns the deepest ancestor of dir, starting with dir
// itself, that exists on disk. A parent chain can be longer than what resolve
// saw, and every existing element of it is a place a symlink may hide.
func existingAncestor(dir string) string {
	for {
		if _, err := os.Lstat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
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

// The argument structs below mark optional fields with `omitempty` because
// jsonschema-go infers a struct's schema from its fields: without it every
// field becomes a *required* one, and the model is told it must pass
// limit_lines and recursive on every call. That is a contract it cannot honour
// — it does not know the defaults — so it either invents values or repeats the
// call. The zero value is the documented default for each of these.

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset_line,omitempty"`
	Limit  int    `json:"limit_lines,omitempty"`
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
	if err := withinRootAfterLinks(path); err != nil {
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
	BytesWritten int    `json:"bytes_written"`
	Diff         string `json:"diff,omitempty"`
}

func writeFile(ctx agent.Context, in writeFileArgs) (writeFileResult, error) {
	path, err := resolve(in.Path)
	if err != nil {
		return writeFileResult{}, err
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		// The check runs on the deepest ancestor that exists, and again on the
		// directory itself once MkdirAll has created the rest: a middle element
		// that is a symlink would otherwise move the write outside the root
		// without resolve ever seeing it.
		if err := withinRootAfterLinks(existingAncestor(dir)); err != nil {
			return writeFileResult{}, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return writeFileResult{}, err
		}
		if err := withinRootAfterLinks(dir); err != nil {
			return writeFileResult{}, err
		}
	}
	before, err := writeFileAtomic(path, []byte(in.Content))
	if err != nil {
		return writeFileResult{}, err
	}
	return writeFileResult{BytesWritten: len(in.Content), Diff: DiffBlock(in.Path, before, in.Content)}, nil
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
func writeFileAtomic(path string, data []byte) (string, error) {
	// What is being replaced, read before the rename. A read failure other than
	// "not there" is not fatal to the write: the tool's job is to write, and
	// refusing to overwrite a file that cannot be read would be a worse outcome
	// than a tally that undercounts. The failure is therefore swallowed and the
	// write counted as wholly additive.
	before, _ := readIfExists(path)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".dmcode-*")
	if err != nil {
		return before, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return before, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return before, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return before, err
	}
	recordChange(path, before, string(data))
	return before, nil
}

type editFileArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}
type editFileResult struct {
	Replacements int    `json:"replacements"`
	Diff         string `json:"diff,omitempty"`
}

func editFile(ctx agent.Context, in editFileArgs) (editFileResult, error) {
	path, err := resolve(in.Path)
	if err != nil {
		return editFileResult{}, err
	}
	if err := withinRootAfterLinks(path); err != nil {
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
		before, err := writeFileAtomic(path, []byte(content))
		if err != nil {
			return editFileResult{}, err
		}
		return editFileResult{Diff: DiffBlock(in.Path, before, content)}, nil
	}
	if in.ReplaceAll {
		n := strings.Count(src, in.OldString)
		if n > 0 {
			res, err := write(strings.ReplaceAll(src, in.OldString, in.NewString))
			if err != nil {
				return editFileResult{}, err
			}
			res.Replacements = n
			return res, nil
		}
	} else {
		idx := strings.Index(src, in.OldString)
		if idx >= 0 {
			if strings.Index(src[idx+1:], in.OldString) >= 0 {
				return editFileResult{}, fmt.Errorf("old_string matches multiple locations in %s; include more context or set replace_all", in.Path)
			}
			res, err := write(src[:idx] + in.NewString + src[idx+len(in.OldString):])
			if err != nil {
				return editFileResult{}, err
			}
			res.Replacements = 1
			return res, nil
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
			res, err := write(normSrc)
			if err != nil {
				return editFileResult{}, err
			}
			res.Replacements = n
			return res, nil
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
			res, err := write(normSrc)
			if err != nil {
				return editFileResult{}, err
			}
			res.Replacements = 1
			return res, nil
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
		res, err := write(out)
		if err != nil {
			return editFileResult{}, err
		}
		res.Replacements = 1
		return res, nil
	}
	if count > 1 && in.ReplaceAll {
		res, err := write(out)
		if err != nil {
			return editFileResult{}, err
		}
		res.Replacements = count
		return res, nil
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

// page is the shape by which every walk-based tool reports a slice of its
// result set.
//
// It exists because a truncated result with no way to continue is worse than
// no limit at all: the model is told "... (truncated)", has nothing to ask
// for with, and the only thing left to try is the same call again — which
// returns the same truncation, forever. NextOffset is the way out, so it is
// carried on every result rather than left to the caller to compute.
type page struct {
	// Offset is where this page starts in the result set, and the value to
	// pass back as `offset` to continue.
	Offset int `json:"offset"`
	// Returned is how many items this page holds.
	Returned int `json:"returned"`
	// Total is how many items exist in the whole set.
	Total int `json:"total"`
	// NextOffset is the offset of the following page. It is 0 when the set is
	// exhausted, so Truncated is the field to branch on.
	NextOffset int `json:"next_offset"`
	// Truncated is true when items remain after this page.
	Truncated bool `json:"truncated"`
}

// pageWindow resolves an offset/limit pair against a set of n items and
// describes the window that came out, so a caller cannot report a page it did
// not take.
func pageWindow(n, offset, limit int) (start, end int, p page) {
	if offset < 0 {
		offset = 0
	}
	if offset > n {
		offset = n
	}
	// A limit of zero or less means "no preference", not "nothing": the model
	// that did not think about paging should still get a page, not an empty
	// answer it cannot interpret.
	if limit <= 0 {
		limit = defaultPageItems
	}
	if limit > maxPageItems {
		limit = maxPageItems
	}
	end = offset + limit
	if end > n {
		end = n
	}
	p = page{Offset: offset, Returned: end - offset, Total: n, Truncated: end < n}
	if p.Truncated {
		p.NextOffset = end
	}
	return offset, end, p
}

type listDirArgs struct {
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}
type listDirResult struct {
	Entries string `json:"entries"`
	page
}

func listDir(ctx agent.Context, in listDirArgs) (listDirResult, error) {
	dir, err := resolve(in.Path)
	if err != nil {
		return listDirResult{}, err
	}
	// Both branches collect the whole set and then take a window out of it.
	// Counting first is what makes `total` and `next_offset` trustworthy; the
	// alternative — stop at the cap and say "truncated" — is what left the
	// agent with nothing to ask for.
	var items []string
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
				items = append(items, e.Name()+"/")
			} else {
				items = append(items, e.Name())
			}
		}
	} else {
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
			if len(items) >= maxScannedItems {
				return filepath.SkipAll
			}
			items = append(items, filepath.ToSlash(path))
			return nil
		})
		if err != nil {
			return listDirResult{}, err
		}
		// Only the recursive walk is unordered, and a paginated set nobody can
		// page predictably is a set that will be paged wrongly.
		sort.Strings(items)
	}

	start, end, p := pageWindow(len(items), in.Offset, in.Limit)
	return listDirResult{
		Entries: strings.Join(items[start:end], "\n"),
		page:    p,
	}, nil
}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Glob    string `json:"glob,omitempty"`
	// ContextLines asks for that many lines either side of each match. It is
	// what makes grep an answer rather than a list of line numbers: reading
	// what surrounds a hit is the whole point, and doing it one read_file per
	// hit is a round trip per line.
	ContextLines int `json:"context_lines,omitempty"`
	Offset       int `json:"offset,omitempty"`
	Limit        int `json:"limit,omitempty"`
}
type grepResult struct {
	Matches string `json:"matches"`
	page
}

// grepHit is a match found by the scan, kept as a position rather than as text:
// the scan has to count every hit to report an honest total, but only the page
// that comes back should ever be turned into a string.
type grepHit struct {
	path string // slash-separated, absolute
	line int    // 1-based
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

	context := min(max(in.ContextLines, 0), maxContextLines)

	var hits []grepHit
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		// WalkDir does not descend into symlinked directories, but it still
		// reports a symlinked file, and reading it would follow the link to
		// content outside the workspace. Such an entry is skipped, not matched.
		if d.Type()&os.ModeSymlink != 0 {
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
				hits = append(hits, grepHit{path: filepath.ToSlash(path), line: i + 1})
				if len(hits) >= maxScannedItems {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return grepResult{}, err
	}
	// The walk visits files in directory order, so two pages would not line up
	// on the same hits. Sorting is what makes offset mean "the same thing" on
	// every call.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].path != hits[j].path {
			return hits[i].path < hits[j].path
		}
		return hits[i].line < hits[j].line
	})

	start, end, p := pageWindow(len(hits), in.Offset, in.Limit)
	matches := renderGrepPage(hits[start:end], context)
	return grepResult{Matches: matches, page: p}, nil
}

// renderGrepPage writes the page's matches, each with its context window.
//
// Two details make the output readable rather than merely complete: a context
// line already shown for a neighbouring match is not repeated, so overlapping
// windows cost nothing; and context lines are marked with "-" where a match is
// marked with ":", which is what GNU grep does and what a reader can pattern
// on to tell what matched from what was merely nearby.
func renderGrepPage(hits []grepHit, context int) string {
	if len(hits) == 0 {
		return ""
	}
	// Only the files this page actually touches are read, and each is read
	// once however many of its lines the page covers.
	loaded := map[string][]string{}
	readFileLines := func(path string) []string {
		if lines, ok := loaded[path]; ok {
			return lines
		}
		var lines []string
		if data, err := os.ReadFile(filepath.FromSlash(path)); err == nil {
			lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		}
		loaded[path] = lines
		return lines
	}

	var b strings.Builder
	// printed is the last 1-based line already emitted per file, so a window
	// that overlaps the previous one starts where it left off.
	printed := map[string]int{}
	rendered := 0
	for _, h := range hits {
		lines := readFileLines(h.path)
		if len(lines) == 0 {
			continue
		}
		lo := max(h.line-1-context, 0)
		hi := min(h.line+context, len(lines))
		for i := lo; i < hi; i++ {
			n := i + 1
			if n <= printed[h.path] {
				continue
			}
			if rendered >= maxGrepRenderedLines {
				b.WriteString("... (output truncated: rerun with a smaller limit)\n")
				return b.String()
			}
			printed[h.path] = n
			rendered++
			sep := "-"
			if n == h.line {
				sep = ":"
			}
			fmt.Fprintf(&b, "%s%s%d%s %s\n", h.path, sep, n, sep, truncate(lines[i], 200))
		}
	}
	return b.String()
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
	Offset  int    `json:"offset,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
type globResult struct {
	Files string `json:"files"`
	page
}

func glob(ctx agent.Context, in globArgs) (globResult, error) {
	pattern := in.Pattern
	// The pattern's leading directory is a real path argument and must clear the
	// workspace boundary, but the *result* stays relative: the agent reads these
	// back to pass to read_file, and "-relative" output keeps that round trip
	// working whichever directory the session was started in.
	if err := checkPatternRoot(pattern); err != nil {
		return globResult{}, err
	}
	// Everything else in the package resolves against the workspace root, and
	// glob used to be the exception: it walked the process directory. The two
	// coincide only because /cd moves both, which is a coincidence to rely on
	// rather than a property to depend on — and it is why this tool alone could
	// not be tested without changing directory underneath it.
	root, err := resolve("")
	if err != nil {
		return globResult{}, err
	}
	var matches []string
	// If pattern contains ** or path separators, perform recursive walk matching
	if strings.Contains(pattern, "**") || strings.Contains(pattern, "/") || strings.Contains(pattern, "\\") {
		rePattern, err := regexp.Compile(globToRegex(filepath.ToSlash(pattern)))
		if err != nil {
			return globResult{}, fmt.Errorf("invalid glob pattern: %w", err)
		}
		prefix := filepath.ToSlash(root) + "/"
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
			relative := strings.TrimPrefix(slashPath, prefix)
			// Matched against both spellings so "src/*.go" and "./src/*.go"
			// behave the way the caller wrote them.
			if rePattern.MatchString(relative) || rePattern.MatchString("./"+relative) {
				if len(matches) >= maxScannedItems {
					return filepath.SkipAll
				}
				matches = append(matches, relative)
			}
			return nil
		})
		if err != nil {
			return globResult{}, err
		}
	} else {
		found, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return globResult{}, err
		}
		prefix := filepath.ToSlash(root) + "/"
		for _, p := range found {
			matches = append(matches, strings.TrimPrefix(filepath.ToSlash(p), prefix))
		}
	}
	sort.Strings(matches)
	start, end, p := pageWindow(len(matches), in.Offset, in.Limit)
	return globResult{Files: strings.Join(matches[start:end], "\n"), page: p}, nil
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
	WorkDir  string `json:"work_dir,omitempty"`
	TimeoutS int    `json:"timeout_seconds,omitempty"`
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

// pagingNote is the part of the contract every walk-based tool shares, kept in
// one place because it has to be said the same way in each description.
//
// The sentence that matters is the last one. A result that reports truncation
// while offering no offset to continue from is the shape that produces a stuck
// agent: there is nothing to do but call again, and calling again returns the
// same truncation.
const pagingNote = "Results are paginated: pass offset to continue where next_offset points, " +
	"and limit to choose a page size (default 200, max 2000). " +
	"When truncated is true, read the next page instead of repeating the same call."

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
		Description: pagingNote + "Lists one directory, subdirectories first. Directories are written with a trailing '/'. Dotfiles are listed; the .git, .idea, .vscode, node_modules and vendor directories are skipped. Set recursive=true to walk the whole tree and return file paths instead.",
	}, listDir)
	if err != nil {
		return nil, err
	}
	grepTool, err := functiontool.New(functiontool.Config{
		Name:        "grep",
		Description: pagingNote + "Regex search across files under path (default cwd). Optional filename glob filter like '*.go'. Set context_lines to also get the lines around each match, which is usually what you want instead of a bare line number. Returns path:line: match, with path-line- for context lines.",
	}, grep)
	if err != nil {
		return nil, err
	}
	globTool2, err := functiontool.New(functiontool.Config{
		Name:        "glob",
		Description: pagingNote + "Lists files matching a shell glob pattern like 'src/*.go' or '*.md'. Supports ** for a recursive match.",
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
	webSearchTool, err := MakeWebSearchTool()
	if err != nil {
		return nil, err
	}
	// The plan tools live in their own package but belong to the same set: the
	// agent publishes a plan through them and the user reads it with /todo, so
	// one without the other would show a user a plan the agent cannot keep.
	todoTools, err := todo.Default.MakeTools()
	if err != nil {
		return nil, err
	}
	out := []tool.Tool{
		readFileTool, writeFileTool, editFileTool, listDirTool,
		grepTool, globTool2, runCommandTool, webSearchTool,
	}
	return append(out, todoTools...), nil
}

// readOnlyNames are the tools a planning turn may use. run_command is absent on
// purpose: a shell can write a file through >, Out-File, tee or touch, so
// "read-only" cannot be enforced from the prompt alone and the tool is withheld
// instead of trusted.
//
// The plan tools are here for the same reason they are in the full set: a plan
// is a document, not an edit. Withholding them from plan mode would leave the
// mode whose entire output is a plan unable to publish one.
var readOnlyNames = map[string]bool{
	"read_file":  true,
	"list_dir":   true,
	"grep":       true,
	"glob":       true,
	"web_search": true,
	"todo_write": true,
	"todo_set":   true,
	"todo_read":  true,
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
