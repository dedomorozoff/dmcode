package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// ---------------------------------------------------------------------------
// read_files

type readFilesArgs struct {
	Paths      []string `json:"paths" jsonschema:"The files to read, relative to the workspace root. At most 20 per call."`
	LimitLines int      `json:"limit_lines,omitempty" jsonschema:"How many lines to return from each file. Default 400; use read_file when one file needs paging."`
}

type readFilesFile struct {
	Path       string `json:"path"`
	Content    string `json:"content,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	TotalLines int    `json:"total_lines,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
	Error      string `json:"error,omitempty"`
}

type readFilesResult struct {
	Files []readFilesFile `json:"files"`
}

// maxReadFilesBytes keeps one batch from turning into a second context window.
// read_file already caps a single page; this is the cap that the batch shape
// added and the single-file shape did not have.
const maxReadFilesBytes = 512 << 10

func readFiles(ctx agent.Context, in readFilesArgs) (readFilesResult, error) {
	if len(in.Paths) == 0 {
		return readFilesResult{}, fmt.Errorf("paths is required")
	}
	if len(in.Paths) > 20 {
		return readFilesResult{}, fmt.Errorf("at most 20 files per call; split the list")
	}
	limit := in.LimitLines
	if limit <= 0 {
		limit = 400
	}
	out := readFilesResult{Files: make([]readFilesFile, 0, len(in.Paths))}
	used := 0
	for _, p := range in.Paths {
		if used >= maxReadFilesBytes {
			out.Files = append(out.Files, readFilesFile{Path: p, Error: "not read: this batch reached its total size limit; call read_files again for this file"})
			continue
		}
		f, err := readFile(ctx, readFileArgs{Path: p, Limit: limit})
		if err != nil {
			out.Files = append(out.Files, readFilesFile{Path: p, Error: err.Error()})
			continue
		}
		if used+len(f.Content) > maxReadFilesBytes {
			out.Files = append(out.Files, readFilesFile{Path: p, Error: "not read: this batch reached its total size limit; call read_files again for this file"})
			continue
		}
		used += len(f.Content)
		out.Files = append(out.Files, readFilesFile{
			Path: p, Content: f.Content, StartLine: f.StartLine,
			TotalLines: f.TotalLines, Truncated: f.Truncated, NextOffset: f.NextOffset,
		})
	}
	return out, nil
}

func makeReadFilesTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "read_files",
		Description: "Reads several files in one call. Each file gets up to limit_lines (default 400) and carries the same start_line, total_lines and next_offset facts read_file returns. " +
			"A file that cannot be read reports its error in its own entry instead of failing the whole batch.",
	}, readFiles)
}

// ---------------------------------------------------------------------------
// apply_patch

type applyPatchArgs struct {
	Patch string `json:"patch" jsonschema:"A unified diff, the format git diff produces. Include the ---/+++ headers and @@ hunks. Supports new files, modifications and deletions; renames and binary patches are refused."`
}

type applyPatchResult struct {
	Files []string `json:"files"`
	Diffs string   `json:"diffs,omitempty"`
}

type patchPlan struct {
	path    string
	rel     string
	old     []byte
	new     []byte
	deleted bool
}

func applyPatch(ctx agent.Context, in applyPatchArgs) (applyPatchResult, error) {
	files, _, err := gitdiff.Parse(strings.NewReader(in.Patch))
	if err != nil {
		return applyPatchResult{}, fmt.Errorf("cannot parse patch: %w", err)
	}
	if len(files) == 0 {
		return applyPatchResult{}, fmt.Errorf("the patch contains no file changes")
	}

	plans := make([]patchPlan, 0, len(files))
	for _, f := range files {
		if f.IsBinary {
			return applyPatchResult{}, fmt.Errorf("%s: binary patches are not supported; use write_file", f.NewName)
		}
		if f.IsRename || f.IsCopy {
			return applyPatchResult{}, fmt.Errorf("%s: rename and copy patches are not supported; use move_file and edit_file", f.NewName)
		}
		name := f.NewName
		if f.IsDelete {
			name = f.OldName
		}
		rel, err := patchPath(name)
		if err != nil {
			return applyPatchResult{}, err
		}
		if rel == "" {
			return applyPatchResult{}, fmt.Errorf("the patch names no file")
		}
		abs, err := resolve(rel)
		if err != nil {
			return applyPatchResult{}, err
		}
		if err := withinRootAfterLinks(abs); err != nil {
			return applyPatchResult{}, err
		}
		plan := patchPlan{path: abs, rel: filepath.ToSlash(rel), deleted: f.IsDelete}
		if f.IsNew {
			// A patch that says "new file" must not silently replace one that
			// is already there. The model can delete it first if that is what
			// it meant, and that deletion is a separate, undoable fact.
			if _, err := os.Stat(abs); err == nil {
				return applyPatchResult{}, fmt.Errorf("%s already exists; a new-file patch will not overwrite it", plan.rel)
			}
		}
		if !f.IsNew {
			data, err := os.ReadFile(abs)
			if err != nil {
				return applyPatchResult{}, err
			}
			plan.old = data
		}
		if !f.IsDelete {
			var buf bytes.Buffer
			if err := gitdiff.Apply(&buf, bytes.NewReader(plan.old), f); err != nil {
				return applyPatchResult{}, fmt.Errorf("%s: %w", plan.rel, err)
			}
			plan.new = buf.Bytes()
		}
		plans = append(plans, plan)
	}

	out := applyPatchResult{}
	var diffs strings.Builder
	for _, p := range plans {
		if p.deleted {
			if _, err := deleteFile(ctx, deleteFileArgs{Path: p.rel}); err != nil {
				return out, fmt.Errorf("%s: %w", p.rel, err)
			}
			out.Files = append(out.Files, p.rel)
			diffs.WriteString(DiffBlock(p.rel, string(p.old), ""))
			continue
		}
		if !sameBytes(p.old, p.new) {
			if err := prepareWritePath(p.path); err != nil {
				return out, err
			}
			if _, err := writeFileAtomic(p.path, p.new); err != nil {
				return out, fmt.Errorf("%s: %w", p.rel, err)
			}
		}
		out.Files = append(out.Files, p.rel)
		diffs.WriteString(DiffBlock(p.rel, string(p.old), string(p.new)))
	}
	out.Diffs = diffs.String()
	return out, nil
}

func makeApplyPatchTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "apply_patch",
		Description: "Applies a unified diff to one or more workspace files in one call. The patch is parsed and applied in memory first, so a conflict in any file is reported before anything is written. " +
			"Counted in the session's changes and undone by ctrl+z, like write_file and edit_file.",
	}, applyPatch)
}

func patchPath(name string) (string, error) {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" || name == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
		name = name[2:]
	}
	if filepath.IsAbs(filepath.FromSlash(name)) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%s: patch paths must be relative to the workspace root", name)
	}
	return filepath.FromSlash(name), nil
}

func prepareWritePath(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := withinRootAfterLinks(existingAncestor(dir)); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return withinRootAfterLinks(dir)
}

func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// git_status / git_diff

type gitStatusArgs struct {
	Path string `json:"path,omitempty" jsonschema:"Limit the status to one path inside the workspace. Leave it out for the whole repository."`
}

type gitStatusResult struct {
	Output string `json:"output"`
	Clean  bool   `json:"clean"`
}

func gitStatus(ctx agent.Context, in gitStatusArgs) (gitStatusResult, error) {
	args := []string{"--no-pager", "status", "--short", "--branch"}
	if strings.TrimSpace(in.Path) != "" {
		abs, err := resolve(in.Path)
		if err != nil {
			return gitStatusResult{}, err
		}
		rel, err := filepath.Rel(Root(), abs)
		if err != nil {
			return gitStatusResult{}, err
		}
		args = append(args, "--", filepath.ToSlash(rel))
	}
	out, err := runGit(ctx, args...)
	if err != nil {
		return gitStatusResult{Output: out}, err
	}
	clean := strings.TrimSpace(out) == ""
	if !clean {
		// --branch prints a header even on a clean tree, so "clean" is decided
		// by the absence of a status line rather than by the output being empty.
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "##") {
				clean = false
				break
			}
			clean = true
		}
	}
	return gitStatusResult{Output: out, Clean: clean}, nil
}

type gitDiffArgs struct {
	Staged       bool   `json:"staged,omitempty" jsonschema:"Show the staged diff (what git commit would record) instead of unstaged changes."`
	Path         string `json:"path,omitempty" jsonschema:"Limit the diff to one path inside the workspace."`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Lines of context around each hunk. Default 3, maximum 20."`
}

type gitDiffResult struct {
	Output string `json:"output"`
	Staged bool   `json:"staged"`
}

func gitDiff(ctx agent.Context, in gitDiffArgs) (gitDiffResult, error) {
	contextLines := in.ContextLines
	if contextLines <= 0 {
		contextLines = 3
	}
	if contextLines > 20 {
		contextLines = 20
	}
	args := []string{"--no-pager", "diff", "--no-color", fmt.Sprintf("--unified=%d", contextLines)}
	if in.Staged {
		args = append(args, "--cached")
	}
	if strings.TrimSpace(in.Path) != "" {
		abs, err := resolve(in.Path)
		if err != nil {
			return gitDiffResult{}, err
		}
		rel, err := filepath.Rel(Root(), abs)
		if err != nil {
			return gitDiffResult{}, err
		}
		args = append(args, "--", filepath.ToSlash(rel))
	}
	out, err := runGit(ctx, args...)
	if err != nil {
		return gitDiffResult{Output: out, Staged: in.Staged}, err
	}
	return gitDiffResult{Output: truncate(out, 20000), Staged: in.Staged}, nil
}

func runGit(ctx agent.Context, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is not on PATH: %w", err)
	}
	parent := context.Context(ctx)
	if parent == nil {
		parent = context.Background()
	}
	cmd := exec.CommandContext(parent, "git", args...)
	cmd.Dir = Root()
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "PAGER=cat")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		if strings.TrimSpace(text) != "" {
			return text, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, text)
		}
		return text, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return text, nil
}

func makeGitStatusTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_status",
		Description: "Shows the repository's short status, including the current branch. Read-only; it never stages, commits or changes the tree.",
	}, gitStatus)
}

func makeGitDiffTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_diff",
		Description: "Shows a unified diff of the repository's unstaged changes, or the staged changes when staged=true. Read-only; it never applies or stages anything.",
	}, gitDiff)
}
