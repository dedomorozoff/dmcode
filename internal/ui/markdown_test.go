package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// plainText strips the styling so a test can assert on what the user reads
// rather than on the escape sequences wrapped around it.
func plainText(s string) string {
	return ansi.Strip(s)
}

func TestMarkdownDropsMarkersAndKeepsWords(t *testing.T) {
	rows := renderMarkdown("**bold** and *italic* and `code`", 60, styleAgent)
	got := plainText(strings.Join(rows, "\n"))
	for _, banned := range []string{"**", "*italic*"} {
		if strings.Contains(got, banned) {
			t.Errorf("markup %q survived into the output: %q", banned, got)
		}
	}
	for _, want := range []string{"bold", "italic", "code"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing the word %q", got, want)
		}
	}
}

func TestMarkdownDropsHeadingMarkers(t *testing.T) {
	for _, src := range []string{"# Title", "## Section", "### Detail"} {
		got := plainText(strings.Join(renderMarkdown(src, 60, styleAgent), "\n"))
		if strings.Contains(got, "#") {
			t.Errorf("heading marker survived %q: %q", src, got)
		}
	}
	if got := plainText(renderMarkdown("## Section", 60, styleAgent)[0]); !strings.Contains(got, "Section") {
		t.Errorf("heading text was lost: %q", got)
	}
}

// TestMarkdownPreservesCodeIndentation is the regression the feature exists for:
// the old wrapper re-flowed code on spaces, so an indented body lost its shape.
func TestMarkdownPreservesCodeIndentation(t *testing.T) {
	src := "before\n\n```go\nfunc main() {\n\tif x {\n\t\treturn\n\t}\n}\n```\n\nafter"
	rows := renderMarkdown(src, 60, styleAgent)

	var block []string
	collecting := false
	for _, r := range rows {
		txt := plainText(r)
		// The block is the run of indented rows between the two blank rows the
		// renderer leaves around it.
		if strings.HasPrefix(txt, "  func main()") {
			collecting = true
		} else if collecting && strings.TrimSpace(txt) == "" {
			collecting = false
		}
		if collecting {
			block = append(block, txt)
		}
	}
	if len(block) != 5 {
		t.Fatalf("code block has %d rows, want 5: %q", len(block), plainText(strings.Join(rows, "\n")))
	}
	// The nested body line must still be more indented than the one above it,
	// which is exactly what the old plain-text wrapper destroyed.
	nested := plainText(block[2])
	depth := len(nested) - len(strings.TrimLeft(nested, " \t"))
	if depth <= len(block[0])-len(strings.TrimLeft(block[0], " \t")) {
		t.Errorf("the nested code line lost its indentation: %q vs %q", nested, block[0])
	}
}

func TestMarkdownUnterminatedFenceStillRendersAsCode(t *testing.T) {
	// The renderer sees a half-arrived reply while the model is still streaming,
	// so an unclosed fence is the normal case mid-answer, not an error.
	rows := renderMarkdown("```go\nx := 1\n\ty := 2", 40, styleAgent)
	joined := plainText(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "x := 1") || !strings.Contains(joined, "y := 2") {
		t.Fatalf("an unterminated fence lost the code: %q", joined)
	}
	if strings.Contains(joined, "```") {
		t.Errorf("the fence marker leaked into the output: %q", joined)
	}
}

func TestMarkdownListsUseBulletsAndKeepNumbering(t *testing.T) {
	rows := renderMarkdown("- one\n- two", 40, styleAgent)
	joined := plainText(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "•") {
		t.Errorf("bulleted list has no bullet marker: %q", joined)
	}
	if strings.Contains(joined, "- ") {
		t.Errorf("the raw bullet marker survived: %q", joined)
	}

	rows = renderMarkdown("1. first\n2. second", 40, styleAgent)
	joined = plainText(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "1.") || !strings.Contains(joined, "2.") {
		t.Errorf("numbered list lost its numbering: %q", joined)
	}
}

func TestMarkdownNormalisesCRLF(t *testing.T) {
	rows := renderMarkdown("**a**\r\n**b**", 40, styleAgent)
	for i, r := range rows {
		if strings.ContainsRune(r, '\r') {
			t.Errorf("row %d still carries a carriage return: %q", i, r)
		}
	}
}

// TestMarkdownRowsFitWidth is the invariant the frame depends on: a row wider
// than the panel pushes the whole layout past the terminal.
func TestMarkdownRowsFitWidth(t *testing.T) {
	const w = 40
	src := strings.Repeat("**word** ", 40) + "\n" +
		"## A heading that is quite long and will need to wrap somewhere\n" +
		"- " + strings.Repeat("item ", 30) + "\n" +
		"```\n" + strings.Repeat("x", 200) + "\n```"
	for i, r := range renderMarkdown(src, w, styleAgent) {
		if got := ansi.StringWidth(r); got > w {
			t.Errorf("row %d is %d cells wide, want at most %d: %q", i, got, w, plainText(r))
		}
	}
}

func TestMarkdownEmptyInputProducesNoRows(t *testing.T) {
	if rows := renderMarkdown("", 40, styleAgent); len(rows) != 0 {
		t.Errorf("empty input produced %d rows: %q", len(rows), rows)
	}
	if rows := renderMarkdown("\n\n", 40, styleAgent); len(rows) != 0 {
		t.Errorf("blank input produced %d rows: %q", len(rows), rows)
	}
}

// TestReportReplyIsRendered is the whole report in one test: the exact reply
// from the bug report, asserted on the three things it got wrong — the table
// pipes, the ">" marker and the numbered list.
func TestReportReplyIsRendered(t *testing.T) {
	src := "Ингредиенты (на 12-14 блинчиков)\n\n" +
		"| № | Ингредиент | Количество | Замечание |\n" +
		"|---|---|---|---|\n" +
		"| 1 | Мука (пшеничная) | 250 г | ≈ 2 ст. л |\n" +
		"| 2 | Молоко | 330 мл | Можно заменить |\n\n" +
		"> Если питаете детей - можно заменить часть воды на овсяное молоко.\n\n" +
		"## Приготовление\n\n" +
		"1. **Подготовительный этап**\n\n" +
		"1.1 В миску просейте муку с солью.\n"

	joined := plainText(strings.Join(renderMarkdown(src, 90, styleAgent), "\n"))

	if strings.Contains(joined, "|") {
		t.Errorf("the table pipes are still visible:\n%s", joined)
	}
	if strings.Contains(joined, ">") {
		t.Errorf("the blockquote marker is still visible:\n%s", joined)
	}
	if strings.Contains(joined, "##") {
		t.Errorf("the heading marker is still visible:\n%s", joined)
	}
	if strings.Contains(joined, "**") {
		t.Errorf("the bold marker is still visible:\n%s", joined)
	}
	for _, want := range []string{"Ингредиент", "Приготовление", "Подготовительный этап", "Если питаете детей"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q was lost from the reply:\n%s", want, joined)
		}
	}
}

func TestMarkdownPlainProseIsUnchanged(t *testing.T) {
	// Text with no markup must come back as itself, or every plain reply would
	// be silently restyled.
	src := "just a normal sentence with no markup at all"
	rows := renderMarkdown(src, 60, lipgloss.NewStyle())
	if got := plainText(strings.Join(rows, "\n")); got != src {
		t.Errorf("plain prose changed: %q, want %q", got, src)
	}
}

// TestMarkdownRendersTableAsAlignedColumns covers the reply shape that reported
// this feature as broken: a pipe table arrived with every raw "|" still in it.
func TestMarkdownRendersTableAsAlignedColumns(t *testing.T) {
	src := "| № | Ингредиент | Количество |\n" +
		"|---|---|---|\n" +
		"| 1 | Мука | 250 г |\n" +
		"| 2 | Молоко | 330 мл |"

	rows := renderMarkdown(src, 60, styleAgent)
	joined := plainText(strings.Join(rows, "\n"))
	if strings.Contains(joined, "|") {
		t.Errorf("the raw pipe separators survived: %q", joined)
	}
	if !strings.Contains(joined, "Ингредиент") || !strings.Contains(joined, "Молоко") {
		t.Errorf("the table lost its cells: %q", joined)
	}
	// The delimiter row is markup, not content: it must not come through as a
	// row of literal dashes the way it would as prose.
	for i, r := range rows {
		txt := plainText(r)
		if strings.Count(txt, "-") >= 3 {
			t.Errorf("row %d is still a raw delimiter row: %q", i, txt)
		}
	}
	// Every row of the table must be the same width, or the columns do not line up.
	if len(rows) < 2 {
		t.Fatalf("the table produced %d rows: %q", len(rows), joined)
	}
	first := ansi.StringWidth(rows[0])
	for i, r := range rows[1:] {
		if got := ansi.StringWidth(r); got != first {
			t.Errorf("row %d is %d cells, want %d to match the header: %q", i+1, got, first, plainText(r))
		}
	}
}

func TestMarkdownTableFitsANarrowPanel(t *testing.T) {
	// A table too wide for the panel must degrade rather than overflow, since an
	// over-wide row pushes the whole frame past the terminal.
	src := "| aaaa | bbbb | cccc | dddd | eeee | ffff | gggg |\n|---|---|---|---|---|---|---|\n" +
		"| 1 | 2 | 3 | 4 | 5 | 6 | 7 |"
	for _, w := range []int{20, 30, 40, 60} {
		for i, r := range renderMarkdown(src, w, styleAgent) {
			if got := ansi.StringWidth(r); got > w {
				t.Errorf("width %d: row %d is %d cells: %q", w, i, got, plainText(r))
			}
		}
	}
}

// TestMarkdownRendersBlockQuote is the second shape from the same report: a ">"
// line arrived with the marker still in front of it.
func TestMarkdownRendersBlockQuote(t *testing.T) {
	got := plainText(strings.Join(renderMarkdown("> Если это важно — читайте.", 50, styleAgent), "\n"))
	if strings.Contains(got, ">") {
		t.Errorf("the blockquote marker survived: %q", got)
	}
	if !strings.Contains(got, "Если это важно") {
		t.Errorf("the quoted text was lost: %q", got)
	}
}

// TestMarkdownTableIsNotFakedByProsePipes guards the detection: a sentence that
// merely contains a pipe must not be turned into a table.
func TestMarkdownTableIsNotFakedByProsePipes(t *testing.T) {
	src := "Use the pipe | character to join commands together."
	got := plainText(strings.Join(renderMarkdown(src, 60, styleAgent), "\n"))
	if !strings.Contains(got, "pipe | character") {
		t.Errorf("prose containing a pipe was mangled into a table: %q", got)
	}
}
