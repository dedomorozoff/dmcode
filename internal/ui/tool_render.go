package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"google.golang.org/genai"
)

// How a tool result reaches this file, which is the whole reason the shapes
// below are what they are.
//
// A tool result is whatever the ADK made of the Go value the handler returned:
// a struct is marshalled and read back into a map[string]any, so every number
// arrives as a float64 and a field tagged omitempty is simply absent. A handler
// returning a plain string is the one exception — it does not convert, and the
// ADK wraps it as {"result": …}. A handler that returned an error never reaches
// its own struct at all: the result is {"error": …} and nothing else, for every
// tool in the set.
//
// Two decisions follow from that, and they are the ones worth arguing about:
//
//   - Dispatch is on the SHAPE, not on the tool name. MCP tools are named by
//     whatever server they came from, so a name switch has no entry for them and
//     would fall through to "print the JSON" for every third-party tool — the
//     exact thing this file exists to stop. It would also mis-render a server that
//     happened to call itself `grep`. The key sets below are the tools' own JSON
//     contract, they are distinct, and they survive a tool being renamed.
//   - Absent is not zero. `diff` exists only when something changed, `hits` is
//     empty on a failed search, `files_read` is present-but-nil on every
//     sub-agent path that did not read anything. Each of those is asked about
//     with a presence check, because a missing key and a zero value mean
//     different things to a reader.

// toolResCaps bound what one result may put on screen. The transcript is a log
// the user reads while the agent works: enough to see what came back, never so
// much that one result pushes the rest of the turn off the screen. The old
// behaviour — one JSON blob cut at 600 cells — satisfied neither: it showed the
// envelope and hid the payload, which is the information and the noise the other
// way round.
const (
	toolResMaxLines  = 18
	toolResMaxCells  = 1400
	toolResHeadCells = 400 // the payload of a result that is mostly prose
)

// renderToolResponse turns a tool result into what the transcript shows: a line
// saying what happened, and then the part of the payload that a person would
// have asked for. The diff, when there is one, is returned separately because it
// is rendered as its own colourised block rather than as text.
func renderToolResponse(fr *genai.FunctionResponse) (summary, diff string) {
	resp := fr.Response
	if m := resp; m != nil {
		if d, ok := m["diff"].(string); ok && d != "" {
			diff = d
			resp = cloneWithout(resp, "diff")
		}
	}
	return renderToolResult(resp), diff
}

// renderToolResult is renderToolResponse without the FunctionResponse, so the
// formatting is testable and so a restored session can render the same way
// without an event in hand.
func renderToolResult(resp map[string]any) string {
	if len(resp) == 0 {
		return "no output"
	}

	// An error from any tool has the same shape and the same reading: it is not
	// a result, it is a refusal, and it is worth saying in the tool's own words
	// rather than under a key.
	if msg, ok := resp["error"].(string); ok {
		return lines("error", msg)
	}

	switch {
	case hasAll(resp, "from", "to"):
		return lines("moved", fmt.Sprintf("%s → %s", str(resp, "from"), str(resp, "to")))

	case hasAny(resp, "removed"):
		return lines("removed", str(resp, "removed"))

	case hasAny(resp, "bytes_written"):
		return writeOrEdit(resp, fmt.Sprintf("wrote %s", pluralBytes(resp)))

	case hasAny(resp, "replacements"):
		return writeOrEdit(resp, fmt.Sprintf("%s in the file", pluralReplacements(resp)))

	case hasAny(resp, "matches"):
		return paged(resp, "matches", "no matches")

	case hasAll(resp, "map", "root"):
		// Before glob, and the order is load-bearing: glob answers under "files"
		// as a newline-joined list and project_map answers under "files" as a
		// count, so whichever is checked first claims the other's result. project_map
		// goes first because it is the one that also carries a "map", which nothing
		// else does.
		return projectMap(resp)

	case hasAny(resp, "files"):
		return paged(resp, "files", "no files")

	case hasAny(resp, "entries"):
		return paged(resp, "entries", "nothing here")

	case hasAll(resp, "content", "total_lines"):
		return readFile(resp)

	case hasAny(resp, "url", "content") && hasAny(resp, "content_type", "title", "truncated"):
		return fetchURL(resp)

	case hasAny(resp, "hits"):
		return webSearch(resp)

	case hasAny(resp, "report"):
		return subAgent(resp)

	case hasAll(resp, "accepted", "applied"):
		return switchMode(resp)

	case hasAny(resp, "plan"):
		return todoResult(resp)

	case hasAny(resp, "handle"):
		return backgroundCommand(resp)

	case hasAny(resp, "result"):
		// ask_user is the one tool whose result is a bare string.
		return lines("answered", str(resp, "result"))

	case hasAny(resp, "output"):
		return outputKey(resp)
	}
	return generic(resp)
}

// ---------------------------------------------------------------------------
// One shape at a time
// ---------------------------------------------------------------------------

// paged renders the three tools that share a shape: grep, glob and list_dir all
// return one newline-joined string plus the same five page numbers. What differs
// is the word for a hit — a match, a file, an entry — and nothing else, so one
// function renders all three rather than three near-identical copies drifting.
func paged(resp map[string]any, key, empty string) string {
	body, _ := resp[key].(string)
	returned, total, truncated := pageNums(resp)

	var head string
	switch {
	case total == 0 || strings.TrimSpace(body) == "":
		head = empty
	case returned == 1 && total == 1:
		head = "1"
	case returned == 1:
		head = fmt.Sprintf("1 of %d", total)
	default:
		head = fmt.Sprintf("%d of %d", returned, total)
	}
	// A page the agent did not finish is the one fact about it that changes what
	// the reader should conclude, so it belongs on the header rather than in a
	// footer nobody scrolls to.
	if truncated {
		if next, ok := pageNext(resp); ok && next > 0 {
			head += fmt.Sprintf(" — more from offset %d", next)
		} else {
			head += " — truncated"
		}
	}
	return block(head, body)
}

// readFile shows the file, because the file is the result. The line count is
// there so a truncated read is visible as truncated rather than mistaken for the
// whole file, and the range is there so a page of a long file says which part
// of it this is — the same fact the tool hands the model, shown to the user
// who is reading the page.
func readFile(resp map[string]any) string {
	content, _ := resp["content"].(string)
	total := intOf(resp, "total_lines")
	trunc, _ := resp["truncated"].(bool)
	start := intOf(resp, "start_line")
	switch {
	case strings.TrimSpace(content) == "":
		if total == 0 {
			return lines("the file is empty", "")
		}
		return lines("nothing at that offset", fmt.Sprintf("the file has %s", pluralLines(total)))
	case trunc:
		head := fmt.Sprintf("showing part of %s", pluralLines(total))
		if start > 1 {
			// next_offset is the line after the page, so it names the page's
			// last line one short of itself. A page that ended on the cap has
			// no next line to name, and then the last line is the one it began
			// on.
			last := intOf(resp, "next_offset") - 1
			if last < start {
				last = start
			}
			head = fmt.Sprintf("lines %d–%d of %d", start, last, total)
		}
		return block(head, content)
	default:
		return block(pluralLines(total), content)
	}
}

// fetchURL shows what came back and what kind of thing it was. The content is
// prose, so it gets the tighter line budget — a page is long before it is
// interesting.
func fetchURL(resp map[string]any) string {
	if note := str(resp, "note"); note != "" {
		// A refusal carries no content, and the note says why. Showing a blank
		// page under a URL that answered would read as an empty document.
		return block(str(resp, "title"), note)
	}
	content, _ := resp["content"].(string)
	head := strings.TrimSpace(str(resp, "title"))
	if head == "" {
		head = str(resp, "url")
	} else {
		head += "  " + str(resp, "url")
	}
	if ct := str(resp, "content_type"); ct != "" {
		head += "  [" + ct + "]"
	}
	if trunc, _ := resp["truncated"].(bool); trunc {
		head += "  (cut short)"
	}
	return cappedBlock(head, content, toolResHeadCells/2, toolResMaxLines/3)
}

// webSearch renders hits as the list it is: a numbered title and its URL. The
// snippet is the part a person actually reads, so it goes under the title
// indented — but only for as many hits as fit, because twenty snippets is a
// different thing from a search result.
func webSearch(resp map[string]any) string {
	if note := str(resp, "note"); note != "" {
		return lines(fmt.Sprintf("nothing for %q", str(resp, "query")), note)
	}
	raw, _ := resp["hits"].([]any)
	if len(raw) == 0 {
		return lines(fmt.Sprintf("nothing for %q", str(resp, "query")), "the search engine returned no usable results")
	}

	var b strings.Builder
	noun := "results"
	if len(raw) == 1 {
		noun = "result"
	}
	fmt.Fprintf(&b, "%d %s for %q\n", len(raw), noun, str(resp, "query"))
	for i, h := range raw {
		m, ok := h.(map[string]any)
		if !ok {
			continue
		}
		title := str(m, "title")
		if title == "" {
			title = str(m, "url")
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, title)
		if u := str(m, "url"); u != "" && u != title {
			fmt.Fprintf(&b, "   %s\n", u)
		}
		if s := strings.TrimSpace(str(m, "snippet")); s != "" {
			b.WriteString("   " + firstLines(s, 2) + "\n")
		}
	}
	return clip(b.String())
}

// projectMap shows the map itself, which is the part that answers "what is this
// project", and the counts beside it rather than instead of it.
func projectMap(resp map[string]any) string {
	tree, _ := resp["map"].(string)
	head := fmt.Sprintf("%d files, %d directories", intOf(resp, "files"), intOf(resp, "dirs"))
	if langs := strings.TrimSpace(str(resp, "languages")); langs != "" && langs != "unknown" {
		head += "  ·  " + langs
	}
	if markers := strings.TrimSpace(str(resp, "markers")); markers != "" && markers != "none found" {
		head += "\n" + markers
	}
	if note := str(resp, "note"); note != "" {
		head += "\n" + note
	} else if trunc, _ := resp["truncated"].(bool); trunc {
		head += "\n(the tree was cut short)"
	}
	return block(head, tree)
}

// subAgent shows the report — it is the whole reason for delegating — and counts
// what it read rather than listing it, because a dozen paths answer a question
// the reader did not ask.
func subAgent(resp map[string]any) string {
	report, _ := resp["report"].(string)
	if note := str(resp, "note"); note != "" {
		return block("the sub-agent reported nothing", note)
	}
	head := ""
	if files, ok := resp["files_read"].([]any); ok && len(files) > 0 {
		head = "read " + plural(len(files), "file", "files")
	}
	return block(head, report)
}

// switchMode reports what the agent asked for and whether it happened now or at
// the end of the turn, because "accepted" without that reads as a refusal.
func switchMode(resp map[string]any) string {
	if reason := str(resp, "reason"); reason != "" {
		return lines("switch_mode", reason)
	}
	if applied, _ := resp["applied"].(bool); applied {
		return lines("switch_mode", "done")
	}
	return lines("switch_mode", "queued for the end of this turn")
}

// todo shows the plan, which is already formatted for a reader by the store, and
// the counts only where a tool reported them.
func todoResult(resp map[string]any) string {
	plan, _ := resp["plan"].(string)
	if done, total := intOf(resp, "done"), intOf(resp, "total"); total > 0 {
		return cappedBlock(fmt.Sprintf("%d of %d done", done, total), plan, toolResMaxCells, toolResMaxLines)
	}
	return block("", plan)
}

// backgroundCommand renders a command that was started in the background, and
// the answer to a later question about it.
//
// They are one case because they are one shape: a handle, a status, and
// sometimes an output. The output is the tail of what the command printed,
// because a build prints thousands of lines and the failure is at the end.
func backgroundCommand(resp map[string]any) string {
	handle := str(resp, "handle")
	status := str(resp, "status")
	body, _ := resp["output"].(string)
	switch status {
	case "running":
		return block(fmt.Sprintf("%s — still running", handle), body)
	case "done", "failed", "timed out":
		head := fmt.Sprintf("%s — %s", handle, status)
		if code := intOf(resp, "exit_code"); code != 0 && status == "failed" {
			head = fmt.Sprintf("%s — failed (exit %d)", handle, code)
		}
		return block(head, body)
	default:
		// The launch itself: a handle and the line it stands for.
		return lines("started", strings.TrimSpace(handle+" "+str(resp, "command")))
	}
}

// outputKey covers run_command and every MCP tool: both answer under "output",
// and an MCP server is free to make it a structured object rather than text.
// run_command's sentinels — cancelled, timed out, failed — are the only place
// the outcome of a command is stated, so they are pulled out of the output and
// given a line of their own instead of trailing it.
func outputKey(resp map[string]any) string {
	switch v := resp["output"].(type) {
	case string:
		body, outcome := splitCommandOutcome(v)
		return block(outcome, body)
	case map[string]any:
		return generic(v)
	case nil:
		return "no output"
	default:
		return fmt.Sprint(v)
	}
}

// splitCommandOutcome separates run_command's own verdict from the command's
// output. The markers are appended by the tools to a combined stream, so they can
// land mid-string when a command printed without a trailing newline; the last
// marker wins, and anything after it is kept with the body.
func splitCommandOutcome(out string) (body, outcome string) {
	body = strings.TrimRight(out, "\n")
	markers := []struct{ token, text string }{
		{"[command cancelled by user]", "cancelled"},
		{"[command failed:", "failed"},
		{"[command timed out after", "timed out"},
		{"[dmcode could not watch", "its effect on the workspace is not in the panel"},
	}
	for _, mk := range markers {
		if i := strings.Index(body, mk.token); i >= 0 {
			// Only a marker on its own line is a verdict; one echoed inside the
			// command's own output is just text it printed.
			if i == 0 || body[i-1] == '\n' {
				rest := strings.TrimSpace(body[i:])
				if j := strings.IndexByte(rest, ']'); j >= 0 {
					rest = strings.TrimSpace(rest[j+1:])
				}
				body = strings.TrimRight(body[:i], "\n")
				outcome = mk.text
				if rest != "" {
					body += "\n" + rest
				}
			}
		}
	}
	return body, outcome
}

// writeOrEdit is the shared shape of write_file and edit_file once the diff has
// been taken out. A no-op edit leaves no diff, and "no change" is the finding —
// writing it as a success would be reporting a change that did not happen.
func writeOrEdit(resp map[string]any, head string) string {
	if _, ok := resp["diff"]; !ok {
		return lines(head+" — no change to the file", "")
	}
	return lines(head, "")
}

// generic is the fallback for a shape nobody has a case for, which in practice
// means an MCP server's structured output. It prints key: value rather than
// JSON, and never a bare one-line map, because a reader who has reached the
// fallback has already lost the tool-specific rendering and is owed whatever can
// still be read.
func generic(resp map[string]any) string {
	keys := make([]string, 0, len(resp))
	for k := range resp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		switch v := resp[k].(type) {
		case string:
			if strings.Contains(v, "\n") {
				fmt.Fprintf(&b, "%s:\n%s\n", k, firstLines(v, toolResMaxLines-2))
			} else {
				fmt.Fprintf(&b, "%s: %s\n", k, v)
			}
		case nil:
			fmt.Fprintf(&b, "%s: none\n", k)
		default:
			fmt.Fprintf(&b, "%s: %v\n", k, v)
		}
	}
	return clip(b.String())
}

// ---------------------------------------------------------------------------
// The call line
// ---------------------------------------------------------------------------

// toolCallSummary is what the transcript shows where the call is made:
// `grep "pattern in path"`, not `grep({"pattern":"…","path":""})`.
//
// The arguments a model sends are a JSON object because that is what the schema
// is; nobody types them by hand and nobody should have to read them. The one
// argument that decides what the call is about is named per tool, because "the
// primary argument" is not a property the wire carries.
//
// A tool this list does not know — which today means every MCP tool — falls back
// to `name key=value`, which is still readable and still says what was asked.
func toolCallSummary(name string, args map[string]any) string {
	if len(args) == 0 {
		return name
	}
	s := func(k string) string { return str(args, k) }

	switch name {
	case "grep":
		return name + " " + quote(s("pattern")) + whereArg(s("path"))
	case "glob":
		return name + " " + quote(s("pattern"))
	case "web_search":
		return name + " " + quote(s("query"))
	case "fetch_url":
		return name + " " + quote(s("url"))
	case "run_command":
		if wd := s("work_dir"); wd != "" {
			return name + " " + quote(s("command")) + "  in " + wd
		}
		return name + " " + quote(s("command"))
	case "read_file":
		if line := intOf(args, "offset_line"); line > 0 {
			return fmt.Sprintf("%s %s:%d", name, s("path"), line)
		}
		return name + " " + s("path")
	case "edit_file", "write_file":
		if n := lenOf(args, "edits"); n > 0 {
			return fmt.Sprintf("%s %s  (%s)", name, s("path"), plural(n, "edit", "edits"))
		}
		return name + " " + s("path")
	case "list_dir":
		if path := s("path"); path != "" {
			return name + " " + path
		}
		return name
	case "project_map":
		return name + whereArg(s("path"))
	case "move_file":
		return fmt.Sprintf("%s %s → %s", name, s("source"), s("destination"))
	case "delete_file":
		return name + " " + s("path")
	case "todo_write":
		n := lenOf(args, "items")
		if n == 0 {
			return name
		}
		return fmt.Sprintf("%s  (%s)", name, plural(n, "step", "steps"))
	case "todo_set":
		return fmt.Sprintf("%s #%d %s", name, intOf(args, "id"), s("status"))
	case "sub_agent":
		return name + " " + quote(firstLines(s("task"), 1))
	case "switch_mode":
		return name + " " + s("mode")
	case "ask_user":
		return name + " " + quote(firstLines(s("question"), 1))
	}
	return name + " " + genericArgs(args)
}

func whereArg(path string) string {
	if path == "" {
		return ""
	}
	return "  in " + path
}

// genericArgs is `name key=value key=value`, sorted so two calls with the same
// arguments look the same on two different turns.
func genericArgs(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := args[k].(type) {
		case string:
			if v != "" {
				parts = append(parts, k+"="+quote(firstLines(v, 1)))
			}
		case []any:
			if len(v) > 0 {
				parts = append(parts, fmt.Sprintf("%s=[%d]", k, len(v)))
			}
		case map[string]any:
			parts = append(parts, fmt.Sprintf("%s={%d}", k, len(v)))
		case float64:
			if v != 0 {
				parts = append(parts, fmt.Sprintf("%s=%s", k, strconv.FormatFloat(v, 'g', -1, 64)))
			}
		case bool:
			if v {
				parts = append(parts, k)
			}
		}
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// lines is the two-line form used where the payload is one sentence: a claim
// and its detail. head may be empty, in which case only the detail shows.
func lines(head, body string) string {
	head, body = strings.TrimSpace(head), strings.TrimSpace(body)
	switch {
	case head == "" && body == "":
		return ""
	case body == "":
		return head
	case head == "":
		return body
	default:
		return head + "\n" + body
	}
}

// block puts a header above a payload, clipping the payload to what fits.
func block(head, body string) string {
	return cappedBlock(strings.TrimSpace(head), strings.TrimRight(body, "\n"), toolResMaxCells, toolResMaxLines)
}

func cappedBlock(head, body string, maxCells, maxLines int) string {
	body = clipTo(strings.TrimRight(body, "\n"), maxCells, maxLines)
	switch {
	case head == "" && body == "":
		return ""
	case body == "":
		return head
	case head == "":
		return body
	default:
		return head + "\n" + body
	}
}

// clipTo caps a payload at a line count and a cell count, saying how much was
// left out rather than stopping without a word. The count is of what was cut,
// not of what is left: "… +12 more lines" next to five lines is a number the
// reader can check, and a bare ellipsis is not.
func clipTo(s string, maxCells, maxLines int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	rows := strings.Split(s, "\n")
	cut := 0
	for i := range rows {
		if i >= maxLines {
			cut = len(rows) - i
			rows = rows[:i]
			break
		}
		if cellsOf(s, i+1) > maxCells {
			cut = len(rows) - i
			rows = rows[:i]
			break
		}
	}
	if cut == 0 {
		return s
	}
	if len(rows) == 0 {
		// A single line too long is cut, not dropped: "the payload exists and it
		// does not fit" is information, and an empty box is not.
		return truncate(strings.Split(s, "\n")[0], maxCells) + fmt.Sprintf("\n… +%d more lines", maxLines)
	}
	return strings.Join(rows, "\n") + fmt.Sprintf("\n… +%d more lines", cut)
}

// cellsOf is the width of the first n lines, counted in terminal cells.
func cellsOf(s string, n int) int {
	total := 0
	for i, l := range strings.Split(s, "\n") {
		if i >= n {
			break
		}
		total += ansi.StringWidth(l) + 1
	}
	return total
}

func clip(s string) string {
	return clipTo(strings.TrimRight(s, "\n"), toolResMaxCells, toolResMaxLines)
}

func firstLines(s string, n int) string {
	parts := strings.Split(strings.TrimRight(strings.TrimSpace(s), "\n"), "\n")
	if len(parts) <= n {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts[:n], " ") + "…"
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), `"`, `'`) + `"`
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func hasAll(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func intOf(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func lenOf(m map[string]any, key string) int {
	l, _ := m[key].([]any)
	return len(l)
}

// pageNums reads the five numbers every paged tool returns. They are all
// float64, which is the ADK's doing and not something to guess at.
func pageNums(m map[string]any) (returned, total int, truncated bool) {
	t, _ := m["truncated"].(bool)
	return intOf(m, "returned"), intOf(m, "total"), t
}

func pageNext(m map[string]any) (int, bool) {
	n, ok := m["next_offset"]
	if !ok {
		return 0, false
	}
	switch v := n.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

func cloneWithout(m map[string]any, key string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func pluralBytes(m map[string]any) string {
	return plural(intOf(m, "bytes_written"), "byte", "bytes")
}

func pluralReplacements(m map[string]any) string {
	return plural(intOf(m, "replacements"), "replacement", "replacements")
}

func pluralLines(n int) string { return plural(n, "line", "lines") }
