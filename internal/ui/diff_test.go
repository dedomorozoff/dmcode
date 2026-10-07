package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"google.golang.org/genai"
)

func TestDiffRowsColourAndIndent(t *testing.T) {
	m := &uiModel{}
	row := m.rowStyle(kindDiff)
	got := diffRows("── main.go\n-2: old line\n+2: new line", 60, row)

	var plain []string
	for _, r := range got.rows {
		plain = append(plain, ansi.Strip(r))
	}
	want := []string{"    ── main.go", "      2 - old line", "      2 + new line"}
	if len(plain) != len(want) {
		t.Fatalf("rows = %d, want %d:\n%s", len(plain), len(want), strings.Join(plain, "\n"))
	}
	for i, w := range want {
		if plain[i] != w {
			t.Fatalf("row %d = %q, want %q", i, plain[i], w)
		}
	}
	// A language chroma knows routes the row through the highlighted path:
	// the gutter is dim, the marker carries its own colour and the code the
	// theme's, over the removal's dim red background.
	if !strings.Contains(got.rows[1], "48;5;52") {
		t.Fatalf("the removed row carries no removal background: %q", got.rows[1])
	}
	if !strings.Contains(got.rows[2], "48;5;22") {
		t.Fatalf("the added row carries no addition background: %q", got.rows[2])
	}
}

func TestDiffRowsPlainPathForUnknownLanguage(t *testing.T) {
	m := &uiModel{}
	row := m.rowStyle(kindDiff)
	got := diffRows("── data.xyz123\n-2: old line\n+2: new line", 60, row)
	if got.rows[1] != styleDel.Render("    ")+styleHint.Render("  2 ")+styleDel.Render("- old line") {
		t.Fatalf("an unknown language must wrap and colour the whole row: %q", got.rows[1])
	}
}

func TestDiffRowsWrapLongLines(t *testing.T) {
	m := &uiModel{}
	row := m.rowStyle(kindDiff)
	long := "+2: " + strings.Repeat("x", 80)
	got := diffRows(long, 40, row)
	if len(got.rows) < 2 {
		t.Fatalf("a row wider than the panel must wrap, got %d rows", len(got.rows))
	}
	for _, r := range got.rows {
		if w := ansi.StringWidth(ansi.Strip(r)); w > 40 {
			t.Fatalf("row is %d cells wide: %q", w, ansi.Strip(r))
		}
	}
}

func TestToolResultDiffBecomesItsOwnBlock(t *testing.T) {
	m := newTurnModel(t)
	m.workDir = t.TempDir()

	m.Update(toolResMsg{name: "edit_file", output: `{"replacements":1}`, diff: "── main.go\n- old\n+ new"})
	if len(m.history) < 2 {
		t.Fatal("the result and the diff must both land in the transcript")
	}
	res := m.history[len(m.history)-2]
	d := m.history[len(m.history)-1]
	if res.kind != kindToolRes || d.kind != kindDiff {
		t.Fatalf("got kinds %v/%v, want toolres/diff", res.kind, d.kind)
	}
	if d.text != "── main.go\n- old\n+ new" {
		t.Fatalf("diff line = %q", d.text)
	}
}

func TestRenderToolResponsePullsTheDiffOut(t *testing.T) {
	fr := &genai.FunctionResponse{Response: map[string]any{
		"bytes_written": 5,
		"diff":          "── f.txt\n+ x",
	}}
	summary, diff := renderToolResponse(fr)
	if diff != "── f.txt\n+ x" {
		t.Fatalf("diff = %q", diff)
	}
	if strings.Contains(summary, "diff") || strings.Contains(summary, "x") {
		t.Fatalf("the summary still carries the diff: %s", summary)
	}

	// A result without a diff keeps its whole JSON.
	plain := &genai.FunctionResponse{Response: map[string]any{"replacements": 1}}
	summary, diff = renderToolResponse(plain)
	if diff != "" || !strings.Contains(summary, "replacements") {
		t.Fatalf("summary = %q, diff = %q", summary, diff)
	}
}
