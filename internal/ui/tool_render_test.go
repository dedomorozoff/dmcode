package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Four bugs that were visible only by looking at what the renderer draws, and
// that a "does it contain JSON" assertion passed straight through. Each one is a
// shape collision or a flag nobody read, which is the kind of mistake the eye
// catches and a property check cannot.

// glob answers under "files" as a newline-joined list and project_map answers
// under "files" as a count, so whichever case is checked first claims the
// other's result. project_map carries a "map" key nothing else has, and it is
// the one that has to go first.
func TestProjectMapIsNotRenderedAsAFileList(t *testing.T) {
	got := renderToolResult(map[string]any{
		"map":       "dmcode/\n  main.go",
		"root":      "/abs/dmcode",
		"languages": "go 98%",
		"markers":   "go.mod",
		"files":     float64(143),
		"dirs":      float64(31),
		"truncated": false,
	})
	for _, want := range []string{"143 files", "31 directories", "go 98%", "main.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("project_map lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no files") {
		t.Errorf("project_map was rendered as an empty glob listing:\n%s", got)
	}
}

// A page of a long file has to say which part of it this is. The tool hands the
// model the line the page begins at and the offset of the next one; the user
// reading the same page in the transcript is asking the same question.
func TestAPageOfAFileSaysWhichLinesItIs(t *testing.T) {
	got := renderToolResult(map[string]any{
		"content":     "b\nc",
		"total_lines": float64(900),
		"truncated":   true,
		"start_line":  float64(300),
		"next_offset": float64(302),
	})
	if !strings.Contains(got, "300–301") {
		t.Errorf("the page does not name its lines:\n%s", got)
	}
	if !strings.Contains(got, "900") {
		t.Errorf("the page does not say how long the file is:\n%s", got)
	}
}

// plural already carries the number, so prefixing one counted twice.
func TestSubAgentDoesNotCountItsFilesTwice(t *testing.T) {
	got := renderToolResult(map[string]any{
		"report":     "found it",
		"files_read": []any{"a.go", "b.go"},
	})
	if strings.Contains(got, "2 2") {
		t.Errorf("the file count is doubled:\n%s", got)
	}
	if !strings.Contains(got, "read 2 files") {
		t.Errorf("the file count is missing:\n%s", got)
	}
}

// A page the agent did not finish has to say so. The flag was in every result
// and was never read, so a partial grep read as a complete one — which is the
// difference between "there are three matches" and "there are seventeen".
func TestAPartialPageSaysItIsPartial(t *testing.T) {
	full := map[string]any{
		"matches": "a.go:1: x", "offset": float64(0), "returned": float64(1),
		"total": float64(1), "next_offset": float64(0), "truncated": false,
	}
	if got := renderToolResult(full); strings.Contains(got, "truncated") || strings.Contains(got, "offset") {
		t.Errorf("a complete page claims to be partial: %q", got)
	}

	part := map[string]any{
		"matches": "a.go:1: x", "offset": float64(0), "returned": float64(1),
		"total": float64(17), "next_offset": float64(1), "truncated": true,
	}
	got := renderToolResult(part)
	if !strings.Contains(got, "1 of 17") {
		t.Errorf("the page position is not stated: %q", got)
	}
	if !strings.Contains(got, "truncated") && !strings.Contains(got, "offset") {
		t.Errorf("a partial page does not say it is partial: %q", got)
	}
}

// run_command reports its verdict as a marker appended to the output, and that
// is the only place a failure, a timeout or a cancellation is ever stated. Left
// in the stream it is a bracketed aside under twenty lines of build output.
func TestCommandOutcomeIsItsOwnLine(t *testing.T) {
	for _, tc := range []struct {
		name, out, want string
	}{
		{"failed", "ok  a\nok  b\n[command failed: exit status 1]", "failed"},
		{"cancelled", "building...\n[command cancelled by user]", "cancelled"},
		{"timed out", "waiting\n[command timed out after 1m0s]", "timed out"},
		// A command that only printed has no verdict of its own to report.
		{"no verdict", "ok  a\nok  b", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, outcome := splitCommandOutcome(tc.out)
			if outcome != tc.want {
				t.Errorf("outcome = %q, want %q (body %q)", outcome, tc.want, body)
			}
			if strings.Contains(body, "[command") {
				t.Errorf("the marker is still in the output: %q", body)
			}
		})
	}

	// A command that printed the marker itself is not reporting a verdict.
	printed := "log: [command failed: my own words]\nmore"
	if _, outcome := splitCommandOutcome(printed); outcome != "" {
		t.Errorf("a marker the command printed was read as dmcode's verdict: %q", outcome)
	}
}

// The call line is the thing a person reads to know what the agent is doing, and
// `grep({"limit":20,"path":"","pattern":"Вася"})` makes them parse JSON to find
// out. Every tool gets a check that the answer to the obvious question is on the
// line.
func TestTheCallLineNamesWhatTheCallWasAbout(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"grep", map[string]any{"limit": float64(20), "path": "", "pattern": "Вася"}, `grep "Вася"`},
		{"grep", map[string]any{"pattern": "TODO", "path": "internal"}, `grep "TODO"  in internal`},
		{"glob", map[string]any{"pattern": "**/*.go"}, `glob "**/*.go"`},
		{"read_file", map[string]any{"path": "main.go"}, "read_file main.go"},
		{"read_file", map[string]any{"path": "main.go", "offset_line": float64(120)}, "read_file main.go:120"},
		{"write_file", map[string]any{"path": "a.go", "content": "x"}, "write_file a.go"},
		{"edit_file", map[string]any{"path": "a.go", "edits": []any{1, 2}}, "edit_file a.go  (2 edits)"},
		{"run_command", map[string]any{"command": "go test ./..."}, `run_command "go test ./..."`},
		{"list_dir", map[string]any{}, "list_dir"},
		{"list_dir", map[string]any{"path": "internal"}, "list_dir internal"},
		{"move_file", map[string]any{"source": "a.go", "destination": "b.go"}, "move_file a.go → b.go"},
		{"delete_file", map[string]any{"path": "a.go"}, "delete_file a.go"},
		{"web_search", map[string]any{"query": "adk go"}, `web_search "adk go"`},
		{"fetch_url", map[string]any{"url": "https://e/x"}, `fetch_url "https://e/x"`},
		{"todo_set", map[string]any{"id": float64(2), "status": "done"}, "todo_set #2 done"},
		{"todo_write", map[string]any{"items": []any{1, 2, 3}}, "todo_write  (3 steps)"},
		{"switch_mode", map[string]any{"mode": "act"}, "switch_mode act"},
		{"project_map", map[string]any{}, "project_map"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			got := toolLine(tc.tool, tc.args)
			if got != tc.want {
				t.Errorf("toolLine = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "{}") {
				t.Errorf("the call line is still JSON: %q", got)
			}
		})
	}
}

// A tool dmcode has never heard of — which today means every MCP tool — still
// has to be readable, and the argument names are the server's, not dmcode's.
func TestAnUnknownToolIsStillReadable(t *testing.T) {
	got := toolLine("mcp_query_docs", map[string]any{
		"query":   "how to",
		"limit":   float64(5),
		"verbose": true,
		"skip":    "",
	})
	for _, want := range []string{"mcp_query_docs", "query=\"how to\"", "limit=5", "verbose"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q: %q", want, got)
		}
	}
	if strings.Contains(got, "skip") {
		t.Errorf("an argument left at its default took a cell: %q", got)
	}
	if strings.ContainsAny(got, "{}") {
		t.Errorf("an unknown tool is still JSON: %q", got)
	}
}

// A long payload is cut with a count of what was left out. An ellipsis on its own
// is not a number a reader can check, and "the rest is below" is not true.
func TestALongPayloadSaysHowMuchWasCut(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("line of output that goes on\n")
	}
	got := renderToolResult(map[string]any{"output": b.String()})
	if !strings.Contains(got, "more lines") {
		t.Fatalf("a 400-line result was cut without saying so:\n%s", got[:200])
	}
	rows := strings.Split(got, "\n")
	if len(rows) > toolResMaxLines+2 {
		t.Errorf("the result is %d rows, want at most %d plus a header and a footer",
			len(rows), toolResMaxLines+2)
	}
	if cells := ansi.StringWidth(got); cells > toolResMaxCells+200 {
		t.Errorf("the result is %d cells wide, want at most about %d", cells, toolResMaxCells+200)
	}
}

// ask_user is the one tool whose result is a bare string rather than a struct,
// and it arrives wrapped by the ADK under a key nothing else uses.
func TestAskUserAnswersInWords(t *testing.T) {
	got := renderToolResult(map[string]any{"result": "chosen: the second one"})
	if strings.Contains(got, "chosen:") && !strings.Contains(got, "answered") {
		t.Errorf("the answer has no cue saying who gave it: %q", got)
	}
	if strings.ContainsAny(got, "{}") {
		t.Errorf("the answer is JSON: %q", got)
	}
}
