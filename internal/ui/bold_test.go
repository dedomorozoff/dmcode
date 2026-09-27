package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/config"
)

const reportReply = "# Классический рецепт русских блинов 🥞\n\n" +
	"## Ингредиенты\n" +
	"*(на 10-12 блинчиков, 1-1,5 л жидкости)*\n\n" +
	"| Ингредиент | Кол-во | Примечание |\n" +
	"|--------|--------|------------|\n" +
	"| Мука (пшеничная, цельнозерновая или смесь) | **200 г** | Разрыхлитель можно добавить 1 ст. л. |\n" +
	"| Яйцо | **2 шт.** | Хорошо взбивайте |\n" +
	"| Молоко | **600 мл** | При желании заменить водой, растительным молоком |\n" +
	"| Вода | **200 мл** | Поменяйте 400 мл смеси молока и воды |\n" +
	"| Сахар | **1 ст. л.** | (по желанию) | Для сладкого начинки |\n" +
	"| Соль | **¼ ч. л** | |\n" +
	"| Масло растительное | **3 ст. л.** | + 1 ст. л. для смазывания сковороды |\n" +
	"| Топливо (мёд, стружка, творог и д.д.) | По вкусу | |\n\n" +
	"> **Важно:** используйте небольшое количество масла на сковороде — без жёстких участков, не не слишком сильно.\n\n" +
	"----\n\n" +
	"## Приготовление\n\n" +
	"1. **Смешайте сухие ингредиенты.**\n" +
	"   В миске просейте муку, разрыхлитель (если применяете), сахар и соль. Тщательно перемешайте.\n\n" +
	"2. **Приготовьте жидкую часть.**\n" +
	"   В отдельной ёмкости взбейте яйца, молоко (или смесь молока и воды), растительное масло.\n\n" +
	"3. **Соедините тесто.**\n" +
	"   Влейте жидкую смесь в сухие и быстро размешайте венчиком — тесто должно получаться однородным, но слегка жидким.\n\n" +
	"4. **Отдохните тесто 10-15 мин**\n" +
	"   Накройте миску крышкой или пленкой.\n\n" +
	"5. **Разогрейте сковороду.**\n" +
	"   Добавьте небольшое количество масла, разомлейте кисточкой. Сковорода должна быть слегка влажной.\n\n" +
	"6. **Жарка.**\n" +
	"   - Вылейте 60-80 мл теста (≈ 1 блин) и распределите ложкой.\n" +
	"   - Жарьте до появления пузырьков (около 30-60 сек), затем переверните и готовьте вторую сторону 30-40 сек.\n"

// TestReportReplyRenders walks the reply from the report and reports each
// marker that survived, so the failure is named rather than guessed at.
// TestReportReplyThroughFullPipeline is the end-to-end check: the reply goes in
// as agent text and comes out of the viewport exactly as the TUI would draw it.
// Testing renderMarkdown alone would miss anything the transcript layer does to
// the text on the way past.
func TestReportReplyThroughFullPipeline(t *testing.T) {
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.width, m.height = 120, 45
	m.layout()
	m.history = nil
	m.appendAgentText(reportReply)
	m.historyDirty = true
	m.syncVP()

	drawn := ansi.Strip(m.vp.View())
	for _, marker := range []struct{ m, what string }{
		{"##", "heading marker"},
		{"**", "bold marker"},
		{"|", "table pipe"},
		{"> ", "blockquote marker"},
	} {
		if strings.Contains(drawn, marker.m) {
			t.Errorf("%s reached the screen: %q", marker.what, marker.m)
		}
	}
	if !strings.Contains(drawn, "Мука") {
		t.Error("the table content was lost")
	}
}

func TestReportReplyRenders(t *testing.T) {
	rows := renderMarkdown(reportReply, 120, styleAgent)
	joined := ansi.Strip(strings.Join(rows, "\n"))

	for _, m := range []struct{ marker, what string }{
		{"##", "a heading marker"},
		{"**", "a bold marker"},
		{"|", "a table pipe"},
		{">", "a blockquote marker"},
	} {
		if strings.Contains(joined, m.marker) {
			t.Errorf("%s survived: %q", m.what, m.marker)
		}
	}
}

// TestEmitBytesForTerminal prints what actually reaches the terminal so the
// emphasis can be checked against the font rather than guessed at.
func TestEmitBytesForTerminal(t *testing.T) {
	rows := renderMarkdown("**жирный** обычный", 60, styleAgent)
	for _, prof := range []struct {
		n string
		p colorprofile.Profile
	}{
		{"TrueColor", colorprofile.TrueColor},
		{"ANSI256", colorprofile.ANSI256},
		{"ANSI", colorprofile.ANSI},
	} {
		var buf bytes.Buffer
		w := colorprofile.NewWriter(&buf, nil)
		w.Profile = prof.p
		for _, r := range rows {
			w.WriteString(r + "\n")
		}
		fmt.Printf("[%s] %q\n", prof.n, buf.String())
	}
	// What the visible text is, and what the bold span looks like stripped of
	// everything except the weight.
	for _, r := range rows {
		fmt.Printf("visible=%q boldOnly=%q\n", ansi.Strip(r), boldSpans(r))
	}
}

// boldSpans returns only the SGR sequences that set weight, which is what
// decides whether a terminal can render the emphasis.
func boldSpans(s string) string {
	var out []string
	for _, seq := range strings.Split(s, "\x1b[") {
		if strings.HasPrefix(seq, "1;") || strings.HasPrefix(seq, "1m") {
			out = append(out, seq)
		}
	}
	return strings.Join(out, "|")
}

// TestBoldMarkupProducesBoldCodes is the direct check for "the bold is not
// showing": a **bold** span has to come out carrying a real bold escape, not
// just the word with the asterisks stripped.
func TestBoldMarkupProducesBoldCodes(t *testing.T) {
	rows := renderMarkdown("**жирный** текст", 60, styleAgent)
	joined := rows[0]
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("no escape sequences at all in %q", joined)
	}
	// The bold flag is the "1" in an SGR sequence, but it usually shares the
	// sequence with a colour ("1;35"), so it cannot be matched as "1m" alone.
	hasBold := func(s string) bool {
		for _, seq := range strings.Split(s, "\x1b[") {
			if strings.HasPrefix(seq, "1;") || strings.HasPrefix(seq, "1m") {
				return true
			}
		}
		return false
	}
	if !hasBold(joined) {
		t.Errorf("no bold code in %q", joined)
	}
	// A derived style must be able to carry bold on its own, independent of the
	// parser: the emphasis is built from base, not from a package-level style.
	if got := styleAgent.Bold(true).Render("x"); !hasBold(got) {
		t.Errorf("base.Bold(true).Render = %q, want a bold code", got)
	}
	// The heading style is bold too and must survive the same way.
	if got := styleH1.Render("x"); !hasBold(got) {
		t.Errorf("styleH1.Render = %q, want a bold code", got)
	}
}

// TestBoldSurvivesEveryColorProfile is the question behind "the bold is not
// showing": a terminal that reports only the base 16 colours must still get
// bold. If a profile could strip it, the styling would be at the mercy of
// whatever the terminal happened to report, and the parser would be innocent.
func TestBoldSurvivesEveryColorProfile(t *testing.T) {
	profiles := []struct {
		name string
		p    colorprofile.Profile
	}{
		{"TrueColor", colorprofile.TrueColor},
		{"ANSI256", colorprofile.ANSI256},
		{"ANSI", colorprofile.ANSI},
	}
	hasBold := func(s string) bool {
		for _, seq := range strings.Split(s, "\x1b[") {
			if strings.HasPrefix(seq, "1;") || strings.HasPrefix(seq, "1m") {
				return true
			}
		}
		return false
	}
	for _, prof := range profiles {
		st := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
		styled := st.Render("x")
		// The profile is applied on write, by downsampling the escapes: this is
		// the step that could drop the bold.
		w := colorprofile.NewWriter(nil, nil)
		w.Profile = prof.p
		var buf bytes.Buffer
		w2 := colorprofile.NewWriter(&buf, nil)
		w2.Profile = prof.p
		if _, err := w2.WriteString(styled); err != nil {
			t.Fatalf("%s: %v", prof.name, err)
		}
		if !hasBold(buf.String()) {
			t.Errorf("%s: bold did not survive the profile: %q", prof.name, buf.String())
		}
	}
}

// TestBoldDiffersFromPlainBeyondWeight is the regression behind "the bold looks
// the same as the rest". Weight alone is invisible on a terminal whose font has
// no separate bold face, so the emphasis has to carry a second channel: colour.
// It also guards the nesting bug, where an inner reset cancelled the outer bold.
func TestBoldDiffersFromPlainBeyondWeight(t *testing.T) {
	row := renderMarkdown("**жирный** обычный", 60, styleAgent)[0]

	boldSeq, plainSeq := "", ""
	for _, seq := range strings.Split(row, "\x1b[") {
		if strings.Contains(seq, "жирный") && boldSeq == "" {
			boldSeq = seq
		}
		if strings.Contains(seq, "обычный") && plainSeq == "" {
			plainSeq = seq
		}
	}
	if boldSeq == "" || plainSeq == "" {
		t.Fatalf("could not locate both spans in %q", row)
	}
	if boldSeq == plainSeq {
		t.Errorf("the bold span is styled identically to the plain one: %q", boldSeq)
	}
	// The colour must genuinely differ, not just the weight.
	boldColor, plainColor := sgrValue(boldSeq, "9"), sgrValue(plainSeq, "9")
	if boldColor != "" && boldColor == plainColor {
		t.Errorf("bold and plain share the colour %q, so the weight is the only difference", boldColor)
	}
}

// sgrValue returns the digits of the SGR parameter starting with prefix.
func sgrValue(seq, prefix string) string {
	if !strings.HasPrefix(seq, prefix) {
		return ""
	}
	rest := seq[len(prefix):]
	if i := strings.IndexByte(rest, 'm'); i >= 0 {
		return rest[:i]
	}
	return ""
}

// TestHeadingIsBoldEvenWithoutInlineMarkup is the case from the report: a reply
// that leans on structure (headings, numbered steps) rather than **stars**. The
// weight has to come from the heading style, since there is nothing else to set.
func TestHeadingIsBoldEvenWithoutInlineMarkup(t *testing.T) {
	rows := renderMarkdown("## Приготовление", 60, styleAgent)
	if len(rows) == 0 {
		t.Fatal("a heading produced no rows")
	}
	hasBold := func(s string) bool {
		for _, seq := range strings.Split(s, "\x1b[") {
			if strings.HasPrefix(seq, "1;") || strings.HasPrefix(seq, "1m") {
				return true
			}
		}
		return false
	}
	if !hasBold(rows[0]) {
		t.Errorf("the heading is not bold: %q", rows[0])
	}
	// And the plain prose around it must not be bold, or the whole reply would
	// look heavy and the heading would stop standing out.
	rows = renderMarkdown("обычный текст", 60, styleAgent)
	if hasBold(rows[0]) {
		t.Errorf("plain prose came out bold: %q", rows[0])
	}
}

// TestUpgradeColorProfileSkipsTrueColor guards the terminals that answer these
// queries wrongly: a terminal that is already truecolor must not be asked.
func TestUpgradeColorProfileSkipsTrueColor(t *testing.T) {
	if cmd := upgradeColorProfile(colorprofile.TrueColor); cmd != nil {
		t.Error("a truecolor terminal was sent a capability request")
	}
	if cmd := upgradeColorProfile(colorprofile.ANSI256); cmd == nil {
		t.Error("a 256-colour terminal was not offered the finer capabilities")
	}
}
