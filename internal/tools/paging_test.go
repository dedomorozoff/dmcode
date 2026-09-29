package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"
)

// nFiles creates n files named f0000.txt… inside dir and returns their paths in
// the order the tools are expected to report them.
func nFiles(t *testing.T, dir string, n int) []string {
	t.Helper()
	var names []string
	for i := range n {
		name := fmt.Sprintf("f%04d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// TestListDirPagesInsteadOfDeadEnding is the behaviour the whole change is for.
//
// Before, a listing past the cap ended in "... (truncated)" with no way to ask
// for the rest. The only move left was to repeat the same call and get the same
// truncation, which is what a stuck agent does.
func TestListDirPagesInsteadOfDeadEnding(t *testing.T) {
	dir := t.TempDir()
	nFiles(t, dir, 25)
	withRoot(t, dir)

	first, err := listDir(nil, listDirArgs{Path: dir, Limit: 10})
	if err != nil {
		t.Fatalf("listDir: %v", err)
	}
	if first.Total != 25 {
		t.Errorf("total = %d, want 25 — the whole set has to be counted for paging to mean anything", first.Total)
	}
	if first.Returned != 10 || first.Offset != 0 {
		t.Errorf("first page = offset %d, returned %d; want 0, 10", first.Offset, first.Returned)
	}
	if !first.Truncated || first.NextOffset != 10 {
		t.Errorf("first page: truncated=%v next=%d; want true, 10", first.Truncated, first.NextOffset)
	}
	if strings.Contains(first.Entries, "truncated") {
		t.Errorf("the page still says truncated: %q", first.Entries)
	}

	// Walking the pages must cover the set exactly once, in order.
	var seen []string
	for offset := 0; ; {
		res, err := listDir(nil, listDirArgs{Path: dir, Limit: 10, Offset: offset})
		if err != nil {
			t.Fatalf("listDir at offset %d: %v", offset, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(res.Entries), "\n") {
			if line != "" {
				seen = append(seen, line)
			}
		}
		if !res.Truncated {
			break
		}
		if res.NextOffset == offset {
			t.Fatalf("next_offset %d did not advance past offset %d — paging cannot terminate", res.NextOffset, offset)
		}
		offset = res.NextOffset
	}
	if len(seen) != 25 {
		t.Fatalf("paging covered %d entries, want 25", len(seen))
	}
	for i, name := range seen {
		if want := fmt.Sprintf("f%04d.txt", i); name != want {
			t.Errorf("entry %d = %q, want %q", i, name, want)
		}
	}
}

// TestListDirLastPageIsNotMarkedTruncated: the signal has to be right, or the
// agent pages past the end chasing an offset that keeps saying "more".
func TestListDirLastPageIsNotMarkedTruncated(t *testing.T) {
	dir := t.TempDir()
	nFiles(t, dir, 20)
	withRoot(t, dir)

	res, err := listDir(nil, listDirArgs{Path: dir, Limit: 10, Offset: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Returned != 10 {
		t.Errorf("returned = %d, want 10", res.Returned)
	}
	if res.Truncated {
		t.Error("the last page reports truncated: there is nothing after it")
	}
	if res.NextOffset != 0 {
		t.Errorf("next_offset = %d on the last page, want 0", res.NextOffset)
	}
}

// TestListDirOffsetPastTheEndIsEmptyNotAnError: an agent that over-shoots
// should get an empty page, not a failure that invites another guess.
func TestListDirOffsetPastTheEndIsEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	nFiles(t, dir, 3)
	withRoot(t, dir)

	res, err := listDir(nil, listDirArgs{Path: dir, Offset: 999})
	if err != nil {
		t.Fatalf("an offset past the end failed: %v", err)
	}
	if res.Returned != 0 || res.Entries != "" {
		t.Errorf("returned %d entries %q, want none", res.Returned, res.Entries)
	}
	if res.Total != 3 {
		t.Errorf("total = %d past the end, want 3 — total describes the set, not the page", res.Total)
	}
	if res.Truncated {
		t.Error("a page past the end claims more remains")
	}
}

// TestListDirCapsAnUnboundedNonRecursiveListing: the non-recursive branch had
// no cap at all, so one directory could put a hundred thousand lines into the
// context in a single call.
func TestListDirCapsAnUnboundedNonRecursiveListing(t *testing.T) {
	dir := t.TempDir()
	nFiles(t, dir, defaultPageItems+50)
	withRoot(t, dir)

	res, err := listDir(nil, listDirArgs{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Returned != defaultPageItems {
		t.Errorf("returned %d entries with no limit asked for, want the default %d", res.Returned, defaultPageItems)
	}
	if !res.Truncated || res.NextOffset != defaultPageItems {
		t.Errorf("truncated=%v next=%d; want true, %d", res.Truncated, res.NextOffset, defaultPageItems)
	}
}

// TestListDirRecursivePagesAndOrders: the recursive branch used to stop the
// walk at the cap, so it was also unordered. Two pages have to agree on what
// comes next, which means the set has to be sorted.
func TestListDirRecursivePagesAndOrders(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		name := fmt.Sprintf("g%02d.go", i)
		if err := os.WriteFile(filepath.Join(sub, name), []byte("package src"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	withRoot(t, dir)

	first, err := listDir(nil, listDirArgs{Path: dir, Recursive: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 12 {
		t.Errorf("total = %d, want 12", first.Total)
	}
	second, err := listDir(nil, listDirArgs{Path: dir, Recursive: true, Limit: 5, Offset: first.NextOffset})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != first.Total {
		t.Errorf("page 2 total = %d, page 1 total = %d — the set changed under the pager", second.Total, first.Total)
	}
	if overlap := overlapLines(first.Entries, second.Entries); overlap != "" {
		t.Errorf("the two pages share %q, so offset is not a stable position", overlap)
	}
}

// TestGrepPagesAndCounts pins the same contract on grep, whose cap used to end
// in "... (more matches truncated)".
func TestGrepPagesAndCounts(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "line%d\n", i)
		if i == 10 {
			b.WriteString("needle here\n")
		}
	}
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	withRoot(t, dir)

	res, err := grep(nil, grepArgs{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Errorf("total = %d, want 3", res.Total)
	}
	if strings.Contains(res.Matches, "truncated") {
		t.Errorf("grep still dead-ends: %q", res.Matches)
	}

	paged, err := grep(nil, grepArgs{Pattern: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !paged.Truncated || paged.NextOffset != 1 || paged.Returned != 1 {
		t.Errorf("limit 1: returned=%d next=%d truncated=%v; want 1, 1, true",
			paged.Returned, paged.NextOffset, paged.Truncated)
	}
	// Walk to the end; the last page must report that nothing is left.
	for offset := 0; ; {
		res, err := grep(nil, grepArgs{Pattern: "needle", Limit: 1, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Truncated {
			if res.NextOffset != 0 {
				t.Errorf("the last page offers next_offset %d", res.NextOffset)
			}
			break
		}
		if res.NextOffset <= offset {
			t.Fatalf("next_offset %d did not advance past %d", res.NextOffset, offset)
		}
		offset = res.NextOffset
	}
}

// TestGrepContextLines covers the reason to reach for grep at all: the lines
// around a hit, not its number.
func TestGrepContextLines(t *testing.T) {
	dir := t.TempDir()
	body := "alpha\nbravo\nMATCH\ncharlie\ndelta\necho\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	withRoot(t, dir)

	res, err := grep(nil, grepArgs{Pattern: "MATCH", ContextLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmpty(res.Matches)
	if len(lines) != 3 {
		t.Fatalf("context 1 returned %d lines %q, want 3 (one before, the match, one after)", len(lines), res.Matches)
	}
	if !strings.Contains(lines[0], "bravo") {
		t.Errorf("first line = %q, want the line before the match", lines[0])
	}
	if !strings.Contains(lines[1], "MATCH") {
		t.Errorf("second line = %q, want the match", lines[1])
	}
	if !strings.Contains(lines[2], "charlie") {
		t.Errorf("third line = %q, want the line after the match", lines[2])
	}
	// The marker is the only thing that tells a reader which line matched.
	if !strings.Contains(lines[1], ":3:") {
		t.Errorf("match line %q is not marked with ':'", lines[1])
	}
	if !strings.Contains(lines[0], "-2-") {
		t.Errorf("context line %q is not marked with '-'", lines[0])
	}
	if strings.Contains(lines[0], "charlie") || strings.Contains(lines[2], "delta") {
		t.Errorf("context reached further than one line: %q", res.Matches)
	}
}

// TestGrepContextDoesNotRepeatOverlappingWindows: two matches three lines apart
// share their context, and printing it twice makes the output longer without
// adding anything.
func TestGrepContextDoesNotRepeatOverlappingWindows(t *testing.T) {
	dir := t.TempDir()
	body := "a\nb\nMATCH\nd\ne\nf\ng\nMATCH\ni\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	withRoot(t, dir)

	res, err := grep(nil, grepArgs{Pattern: "MATCH", ContextLines: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range nonEmpty(res.Matches) {
		if n := strings.Count(res.Matches, line); n != 1 {
			t.Errorf("line %q appears %d times in:\n%s", line, n, res.Matches)
		}
	}
	if got := len(nonEmpty(res.Matches)); got != 9 {
		t.Errorf("rendered %d lines, want 9 (lines 1-9 of the file, no repeats)", got)
	}
}

// TestGrepContextIsBounded keeps a generous context setting from turning one
// page into the whole file.
func TestGrepContextIsBounded(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("filler\n", 500) + "MATCH\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	withRoot(t, dir)

	res, err := grep(nil, grepArgs{Pattern: "MATCH", ContextLines: 10000})
	if err != nil {
		t.Fatal(err)
	}
	// 2*maxContextLines+1 lines is the most a single match may drag in.
	if got := len(nonEmpty(res.Matches)); got > 2*maxContextLines+1 {
		t.Errorf("context_lines=10000 produced %d lines, want at most %d", got, 2*maxContextLines+1)
	}
}

// TestGrepPagesAreStableAcrossCalls: two pages of a paginated set are only
// meaningful if the set is ordered the same way every time.
func TestGrepPagesAreStableAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"c.txt", "a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("hit\nhit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	withRoot(t, dir)

	one, err := grep(nil, grepArgs{Pattern: "hit", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 6 {
		t.Errorf("total = %d, want 6", one.Total)
	}
	// Three files of two hits each is three pages of two, and the set is
	// ordered by path — so the pages must walk a.txt, then b.txt, then c.txt.
	var combined []string
	for offset := 0; ; {
		res, err := grep(nil, grepArgs{Pattern: "hit", Limit: 2, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		combined = append(combined, res.Matches)
		if !res.Truncated {
			break
		}
		offset = res.NextOffset
	}
	all := strings.Join(combined, "\n")
	for _, want := range []string{"a.txt", "b.txt", "c.txt"} {
		if !strings.Contains(all, want) {
			t.Errorf("paging never covered %s:\n%s", want, all)
		}
	}
	if n := strings.Count(all, ":1:"); n != 3 {
		t.Errorf("saw %d first-line matches, want 3 — a page repeated an earlier one:\n%s", n, all)
	}
}

// TestGlobPagesInsteadOfDeadEnding: glob appended the truncation notice as if
// it were a filename, which made the notice itself look like a match.
func TestGlobPagesInsteadOfDeadEnding(t *testing.T) {
	dir := t.TempDir()
	nFiles(t, dir, 30)
	withRoot(t, dir)

	res, err := glob(nil, globArgs{Pattern: "*.txt", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 30 {
		t.Errorf("total = %d, want 30", res.Total)
	}
	if strings.Contains(res.Files, "truncated") {
		t.Errorf("glob still emits a truncation notice as a filename: %q", res.Files)
	}
	if !res.Truncated || res.NextOffset != 10 {
		t.Errorf("truncated=%v next=%d; want true, 10", res.Truncated, res.NextOffset)
	}

	last, err := glob(nil, globArgs{Pattern: "*.txt", Limit: 10, Offset: 20})
	if err != nil {
		t.Fatal(err)
	}
	if last.Truncated {
		t.Error("the final page claims more remains")
	}
	if last.Returned != 10 {
		t.Errorf("final page returned %d, want 10", last.Returned)
	}
}

// TestGlobRecursivePages walks the branch that sorts its matches rather than
// relying on the order the filesystem happened to hand over.
func TestGlobRecursivePages(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.go", filepath.Join("src", "b.go"), filepath.Join("src", "deep", "c.go")} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("package x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	withRoot(t, dir)

	res, err := glob(nil, globArgs{Pattern: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Errorf("total = %d, want 3", res.Total)
	}
	lines := nonEmpty(res.Files)
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Errorf("results are not sorted: %q before %q", lines[i-1], lines[i])
		}
	}
}

// TestPagingContractIsInTheToolDescriptions: the contract is only worth having
// if the model is told about it, and the old list_dir text claimed to skip
// hidden files while listing them.
func TestPagingContractIsInTheToolDescriptions(t *testing.T) {
	ts, err := MakeTools()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"list_dir": {"next_offset", "truncated", "offset"},
		"grep":     {"next_offset", "truncated", "context_lines"},
		"glob":     {"next_offset", "truncated", "offset"},
	}
	for _, tl := range ts {
		need, ok := want[tl.Name()]
		if !ok {
			continue
		}
		for _, s := range need {
			if !strings.Contains(tl.Description(), s) {
				t.Errorf("%s's description never mentions %q:\n%s", tl.Name(), s, tl.Description())
			}
		}
	}
	if d := descriptionOf(t, ts, "list_dir"); strings.Contains(d, "skipping hidden files") {
		t.Errorf("list_dir still claims to skip hidden files, which it does not do:\n%s", d)
	}
}

func descriptionOf(t *testing.T, ts []tool.Tool, name string) string {
	t.Helper()
	for _, tl := range ts {
		if tl.Name() == name {
			return tl.Description()
		}
	}
	t.Fatalf("no tool named %q", name)
	return ""
}

// TestPageWindowClamps covers the arithmetic directly, including the bounds a
// model can reach by accident.
func TestPageWindowClamps(t *testing.T) {
	cases := []struct {
		name               string
		n, offset, limit   int
		wantStart, wantEnd int
		wantTruncated      bool
		wantNext           int
	}{
		{"empty", 0, 0, 10, 0, 0, false, 0},
		{"under the limit", 5, 0, 10, 0, 5, false, 0},
		{"exactly one page", 10, 0, 10, 0, 10, false, 0},
		{"a page and a half", 15, 0, 10, 0, 10, true, 10},
		{"negative offset", 5, -3, 10, 0, 5, false, 0},
		{"offset past the end", 5, 99, 10, 5, 5, false, 0},
		{"limit past the ceiling", 5, 0, 999999, 0, 5, false, 0},
		{"zero limit means default", 300, 0, 0, 0, defaultPageItems, true, defaultPageItems},
		{"negative limit means default", 300, 0, -1, 0, defaultPageItems, true, defaultPageItems},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end, p := pageWindow(c.n, c.offset, c.limit)
			if start != c.wantStart || end != c.wantEnd {
				t.Errorf("window = [%d,%d), want [%d,%d)", start, end, c.wantStart, c.wantEnd)
			}
			if p.Truncated != c.wantTruncated {
				t.Errorf("truncated = %v, want %v", p.Truncated, c.wantTruncated)
			}
			if p.NextOffset != c.wantNext {
				t.Errorf("next_offset = %d, want %d", p.NextOffset, c.wantNext)
			}
			if p.Total != c.n {
				t.Errorf("total = %d, want %d", p.Total, c.n)
			}
			if p.Returned != end-start {
				t.Errorf("returned = %d, want %d", p.Returned, end-start)
			}
		})
	}
}

func nonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func overlapLines(a, b string) string {
	set := map[string]bool{}
	for _, l := range nonEmpty(a) {
		set[l] = true
	}
	for _, l := range nonEmpty(b) {
		if set[l] {
			return l
		}
	}
	return ""
}
