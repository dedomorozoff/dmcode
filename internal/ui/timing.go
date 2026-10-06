package ui

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// turnTiming is what one turn cost and how long it took.
//
// Three numbers, because three different things go wrong separately. The wait
// to the first token is the endpoint and the network; the total is the turn;
// the prompt count is how much of the window the next request will have to
// carry. A single "it took four minutes" cannot tell a slow model from a full
// context, which are fixed by opposite things.
type turnTiming struct {
	// Elapsed is the whole turn, from the send to the last event.
	Elapsed time.Duration
	// FirstToken is the wait for the first piece of the answer. Zero when the
	// turn produced no text at all — a turn that was nothing but tool calls
	// has no first token, and reporting 0ms as if it had one would be a lie.
	FirstToken time.Duration
	// Prompt and Completion are the token counts the endpoint reported.
	Prompt     int
	Completion int
	// Calls is how many round trips the turn made — one per model call, so a
	// tool loop costs more than one and the per-call speed is only
	// meaningful once it is divided by this.
	Calls int
	// Truncated is the endpoint saying the answer ran into its output limit
	// (finish_reason=length) rather than finishing. It is not an error — the
	// turn did end cleanly — but the answer on screen is a half sentence, and
	// without this the only number a user can read is the context meter, which
	// invites the wrong conclusion entirely: a long calculation has nothing to
	// do with how full the window is.
	Truncated bool
}

// TokensPerSecond is the answer's own rate: the completion tokens over the time
// actually spent generating them. Zero rather than a guard when nothing was
// generated, so a caller can print it unconditionally.
func (t turnTiming) TokensPerSecond() float64 {
	if t.Completion <= 0 || t.Elapsed <= 0 {
		return 0
	}
	return float64(t.Completion) / t.Elapsed.Seconds()
}

// PerCall is the average length of one model call in the turn, which is the
// number that stays comparable between a single-answer turn and a tool loop.
func (t turnTiming) PerCall() time.Duration {
	if t.Calls <= 0 || t.Elapsed <= 0 {
		return 0
	}
	return t.Elapsed / time.Duration(t.Calls)
}

// percentUsed is how much of the window the next request will carry. A window
// of zero means the model is not recognised, which is a different answer from
// an empty context — so it reports 0 and the caller shows the bare count
// rather than dividing by a guess.
func (t turnTiming) percentUsed(window int) int {
	if window <= 0 || t.Prompt <= 0 {
		return 0
	}
	p := t.Prompt * 100 / window
	if p > 999 {
		return 999
	}
	return p
}

// observe folds one model response into the turn's numbers. It is a method
// rather than a block inside the turn loop so the two facts it reads — what the
// endpoint charged for, and whether it finished — are stated once and can be
// driven from a test without a runner behind them.
//
// Two rules, both about which event wins:
//
//   - Usage is read from every response and the largest count kept. A streaming
//     endpoint reports the running total on the final chunk, but some report it
//     only on an intermediate one and leave the last empty — taking the most
//     recent instead of the most complete is how the number ends up zero on a
//     turn that plainly used tokens.
//   - A cut is latched and never cleared. The adapter reports finish_reason on
//     the response that closed the stream, and a wrapper above it may follow
//     with a synthesised one saying "stop"; an answer that has been cut cannot be
//     un-cut, and letting a later event clear the mark is exactly how a cut
//     answer would end up reported as a finished one.
func (t *turnTiming) observe(resp model.LLMResponse) {
	if u := resp.UsageMetadata; u != nil {
		if int(u.PromptTokenCount) > t.Prompt {
			t.Prompt = int(u.PromptTokenCount)
		}
		if int(u.CandidatesTokenCount) > t.Completion {
			t.Completion = int(u.CandidatesTokenCount)
		}
	}
	if resp.FinishReason == genai.FinishReasonMaxTokens {
		t.Truncated = true
	}
}

// sessionTotal is what the whole session has cost so far.
//
// The per-turn line answers "how long was that one". This answers "what has
// this session cost me", which is the question after the twentieth turn and the
// reason a user watches for time at all: a session that has spent eleven
// minutes waiting is a session they may want to restart, and no single turn
// says so.
type sessionTotal struct {
	Elapsed    time.Duration
	Calls      int
	Turns      int
	FirstToken time.Duration
	// firstSum is the running sum behind FirstToken. The mean is recomputed from
	// it rather than averaged into the previous mean, which would weight a
	// 200ms answer as heavily as a 20s one and make the figure drift toward
	// whichever turn happened to be reported last.
	firstSum time.Duration
	firstN   int
}

// record folds a finished turn into the session total.
//
// A stopped turn is counted like any other. The user spent the time whether or
// not the answer arrived, and a total that silently excludes cancelled turns
// would be the one number that cannot be compared against a clock.
func (s *sessionTotal) record(t turnTiming) {
	s.Elapsed += t.Elapsed
	s.Calls += t.Calls
	s.Turns++
	if t.FirstToken <= 0 {
		return
	}
	s.firstSum += t.FirstToken
	s.firstN++
	s.FirstToken = s.firstSum / time.Duration(s.firstN)
}

// Average is the mean turn length of the session.
func (s sessionTotal) Average(turns int) time.Duration {
	if turns <= 0 || s.Elapsed <= 0 {
		return 0
	}
	return s.Elapsed / time.Duration(turns)
}

// timingLine is the per-turn report appended under the agent's answer.
//
// It is a system line rather than a badge because it is a fact about a turn
// that has already happened: it belongs with that turn in the transcript, and
// a value in the status bar would be overwritten by the next one.
//
// The context is given as a fraction of the window rather than as "32k/128k",
// because the fraction is the part that answers anything — a bare pair of
// numbers asks the reader to do the division themselves to find out whether
// there is room left.
func timingLine(t turnTiming, window int) string {
	parts := make([]string, 0, 6)

	parts = append(parts, fmt.Sprintf("%s %s", i18n.T("time:"), formatWait(t.Elapsed)))
	if t.FirstToken > 0 {
		parts = append(parts, fmt.Sprintf("%s %s", i18n.T("first token:"), formatWait(t.FirstToken)))
	}
	if t.Completion > 0 {
		parts = append(parts, fmt.Sprintf("%s %s %s",
			i18n.T("answer:"), formatCount(t.Completion), i18n.T("tokens")))
	}
	if t.Truncated {
		// On the same row as the token count it qualifies, rather than as a row
		// of its own: "answer: 4096 tokens · cut at the output limit" reads as
		// one fact about one number, where a separate line below reads as a
		// separate problem that happened afterwards.
		parts = append(parts, i18n.T("cut at the output limit"))
	}
	if t.Prompt > 0 {
		if window > 0 {
			parts = append(parts, fmt.Sprintf("%s %d%% %s (%s %s %s)",
				i18n.T("context:"), t.percentUsed(window), i18n.T("full"),
				formatCount(t.Prompt), i18n.T("of"), formatCount(window)))
		} else {
			parts = append(parts, fmt.Sprintf("%s %s %s",
				i18n.T("context:"), formatCount(t.Prompt), i18n.T("tokens")))
		}
	}
	if t.Calls > 1 {
		parts = append(parts, fmt.Sprintf("%s %d", i18n.T("calls:"), t.Calls))
	}
	if r := t.TokensPerSecond(); r >= 1 {
		parts = append(parts, fmt.Sprintf("%.0f %s", r, i18n.T("tok/s")))
	}
	return "⏱ " + strings.Join(parts, " · ")
}

// elapsedNote is the live counter shown while a turn runs: the time so far, and
// the context as it was last reported.
//
// It is what makes a slow turn legible. Without it a long wait is
// indistinguishable from a hung one, which is the exact failure the retry
// announcement already exists to prevent.
func (m *uiModel) elapsedNote() string {
	if m.turnStart.IsZero() {
		return ""
	}
	note := formatWait(time.Since(m.turnStart))
	if w := m.window; w > 0 && m.timing.Prompt > 0 {
		note += fmt.Sprintf(" · %s %d%%", i18n.T("context"), m.timing.percentUsed(w))
	}
	return note
}

// usageBar is the context meter: a fixed-width bar of block glyphs.
//
// Block glyphs rather than ASCII so a partly filled bar does not shimmer
// between redraws, and clamped to its own width — a count above the window
// (which a wrong DMCODE_CONTEXT, or a provider's own accounting, produces)
// fills the bar rather than drawing past the panel edge.
func usageBar(prompt, window, width int) string {
	if width < 1 {
		return ""
	}
	if window <= 0 || prompt <= 0 {
		return strings.Repeat("░", width)
	}
	filled := prompt * width / window
	if filled > width {
		filled = width
	}
	if filled < 1 && prompt > 0 {
		// A context that is not empty must show something, or the bar reads as
		// an untouched meter on a conversation already under way.
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// formatCount renders a number with a unit a person reads without translating:
// "842", "12 345", "1.2M". Thousands are grouped rather than abbreviated to "k",
// because a bare "12.4k" beside a window of "128k" was the complaint — the
// reader had to work out what was being counted before they could use it, and
// what they actually want is "how much of the window, and how much is left".
func formatCount(n int) string {
	switch {
	case n < 0:
		return "0"
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	// Grouped from the right, so the separators land the same way whatever the
	// magnitude rather than being computed against a threshold.
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// contextRoom is the one line that answers "how much is left", in the unit a
// user can act on: how many more messages of this conversation fit.
//
// Tokens are the model's own unit and useless to say out loud; the question
// behind every context meter is "can I keep going or do I start a new
// session", and that is a question about turns, not about tokenizer output.
//
// The division is by the session's own average turn size, so a conversation of
// short questions answers honestly, and one that pastes large files answers
// "a couple more" rather than the forty a fixed 500-token guess would promise.
// It is clamped at zero because a negative "turns left" is nonsense, and at a
// number below ten because past that the estimate is too rough to be worth
// printing.
func (m *uiModel) contextRoom(prompt int) int {
	if m.window <= 0 || prompt <= 0 || m.turnCount <= 0 {
		return 0
	}
	free := m.window - prompt
	if free <= 0 {
		return 0
	}
	avg := prompt / m.turnCount
	if avg <= 0 {
		return 0
	}
	n := free / avg
	if n > 99 {
		n = 99
	}
	if n < 1 {
		return 1
	}
	return n
}

// contextLines are the sidebar rows describing how full the window is.
//
// The endpoint's own count is preferred and the estimate is the fallback, so
// the meter is drawn from the first turn onward. A meter that only appears on
// providers which report usage is a meter most users never see at all — the
// absence was the bug, not the sparseness of the data.
//
// Three facts, in the order they answer questions: what the model holds, how
// full that is, and how many more turns fit. The estimate is marked with a "~"
// because it is derived from characters rather than from a tokenizer, and a
// user deciding whether to start a new session deserves to know which number
// they are looking at.
func (m *uiModel) contextLines() []string {
	prompt, exact := m.timing.Prompt, true
	if prompt <= 0 {
		prompt, exact = m.approxPrompt(), false
	}
	if prompt <= 0 {
		return nil
	}
	const barWidth = 10
	// Two different things can be guessed, and only one mark is wanted on the
	// row: the token count (no usage from the endpoint) and the denominator
	// (a model this build does not recognise). Both feed the same numbers, so
	// one "~" on the row tells the reader the figures are approximate without
	// making them hunt for which part it applies to.
	mark := ""
	if !exact || m.windowGuess {
		mark = "~"
	}
	pct := turnTiming{Prompt: prompt}.percentUsed(m.window)
	rows := []string{
		fmt.Sprintf(" %s %d%% %s", usageBar(prompt, m.window, barWidth), pct, i18n.T("full")),
		fmt.Sprintf(" %s%s %s %s", mark, formatCount(prompt), i18n.T("of"), formatCount(m.window)+" "+i18n.T("tokens")),
	}
	if n := m.contextRoom(prompt); n > 0 {
		// One format string, not two translated halves glued together. Splitting
		// the sentence meant the count went through Sprintf on its own, and
		// "more turns:" carries no verb in English — so the number came out as
		// "%!(EXTRA int=99)" beside a label that already ended in a colon.
		rows = append(rows, " "+fmt.Sprintf(i18n.T("room for about %d more turns"), n))
	}
	return rows
}

// approxPrompt is the fallback token count: characters of conversation turned
// into tokens by the usual ratio.
//
// The divisor is the conservative one for the two scripts dmcode is used in —
// Cyrillic costs roughly two characters to a token against English's four — so
// the estimate errs towards reporting a fuller context than there really is. A
// meter that over-reports is the safe direction to be wrong in: the user
// starts a new session slightly early rather than being cut off mid-turn.
func (m *uiModel) approxPrompt() int {
	if m.sessions == nil {
		return 0
	}
	chars := m.sessions.ApproxChars("dmcode", "user", m.sessionID)
	if chars <= 0 {
		return 0
	}
	return chars / 2
}

// statsLine is the /stats report: where the time went and what the window holds.
//
// It answers "why was that slow" with more than one number, so a user can tell
// a slow endpoint from a full context from a tool loop that called the model
// five times — the three cases a single duration hides.
func (m *uiModel) statsLine() string {
	t := m.timing
	if t.Elapsed == 0 && t.Prompt == 0 {
		return i18n.T("no turn has run yet")
	}
	var b strings.Builder
	b.WriteString("⏱ " + i18n.T("last turn"))
	b.WriteString("\n  " + fmt.Sprintf("%s %s", i18n.T("total:"), formatWait(t.Elapsed)))
	if t.FirstToken > 0 {
		b.WriteString("\n  " + fmt.Sprintf("%s %s", i18n.T("first token:"), formatWait(t.FirstToken)))
	}
	if per := t.PerCall(); per > 0 && t.Calls > 0 {
		b.WriteString("\n  " + fmt.Sprintf("%s %s × %d", i18n.T("per call:"), formatWait(per), t.Calls))
	}
	if r := t.TokensPerSecond(); r >= 1 {
		b.WriteString("\n  " + fmt.Sprintf("%s %.0f %s", i18n.T("speed:"), r, i18n.T("tok/s")))
	}
	if t.Completion > 0 {
		b.WriteString("\n  " + fmt.Sprintf("%s %s %s",
			i18n.T("output:"), formatCount(t.Completion), i18n.T("tokens")))
	}
	for _, l := range m.contextLines() {
		b.WriteString("\n  " + strings.TrimSpace(l))
	}
	if m.compacted {
		b.WriteString("\n  " + i18n.T("the context was compressed"))
	}
	b.WriteString(m.totalBlock())
	return b.String()
}

// totalBlock is the session's running cost, appended below the last turn.
//
// It is here rather than only in the sidebar because the sidebar is optional
// (ctrl+b, or a terminal too narrow to hold it) while the question survives
// both. A user who never opens the panel still gets the number after typing
// /stats, which is the one moment they are deliberately asking.
func (m *uiModel) totalBlock() string {
	s := m.total
	if s.Turns <= 1 && s.Elapsed == 0 {
		// One turn is already described above, in full detail; repeating it
		// under a "session" heading would say nothing new.
		return ""
	}
	var b strings.Builder
	b.WriteString("\n⏱ " + fmt.Sprintf(i18n.T("session (%d turns)"), s.Turns))
	b.WriteString("\n  " + fmt.Sprintf("%s %s", i18n.T("total time:"), formatWait(s.Elapsed)))
	if avg := s.Average(s.Turns); avg > 0 {
		b.WriteString("\n  " + fmt.Sprintf("%s %s", i18n.T("per turn:"), formatWait(avg)))
	}
	if s.FirstToken > 0 {
		b.WriteString("\n  " + fmt.Sprintf("%s %s", i18n.T("first token:"), formatWait(s.FirstToken)))
	}
	if s.Calls > s.Turns {
		// Only worth saying when the turn made more than one call: a tool loop
		// is a different cost profile from a single answer, and "one call per
		// turn" is not a fact anybody needs restated.
		b.WriteString("\n  " + fmt.Sprintf("%s %d (%s %.1f)",
			i18n.T("model calls:"), s.Calls, i18n.T("per turn:"), float64(s.Calls)/float64(s.Turns)))
	}
	return b.String()
}
