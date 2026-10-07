package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The "/" list, and the reason it is not a prefix filter.
//
// One keystroke used to leave one row. "/p" is "/proxy" and nothing else, "/n"
// is "/new", "/q" is "/quit" — so the list closed the moment it opened, and a
// user who meant "/resume" had to backspace out to find it. Matching the letters
// in order keeps every command that is still reachable on screen, and the ranking
// is what stops that from being noise: the row under the highlight is the one a
// single Enter takes, so it has to be the command most likely meant.

// suggestTexts types text into the input and returns the commands the "/" list
// offers for it, in the order it offers them.
func suggestTexts(t *testing.T, m *uiModel, text string) []string {
	t.Helper()
	m.input.SetValue(text)
	m.updateSuggest()
	out := make([]string, 0, len(m.suggest))
	for _, s := range m.suggest {
		out = append(out, s.text)
	}
	return out
}

func suggestModel(t *testing.T) *uiModel {
	t.Helper()
	m := newSessionModel(t)
	m.width, m.height = 100, 30
	m.layout()
	m.followVP()
	return m
}

func hasCommand(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestNoMatchIsHidden is the invariant, and it is stronger than "the list is
// longer": for any needle, the list must be *every* command the needle can still
// reach. A prefix filter was not that — "/p" reached one command and showed one,
// which is right by accident, but "/r" reached nothing while /resume and /rewind
// were plainly there on screen.
func TestNoMatchIsHidden(t *testing.T) {
	m := suggestModel(t)
	all := m.commands()
	for _, needle := range []string{
		"", "a", "c", "d", "e", "g", "h", "i", "l", "m", "n", "o", "p", "q",
		"r", "s", "t", "u", "w", "y", "z",
		"ch", "cl", "co", "cd", "ch", "de", "ed", "he", "hi", "im", "la", "mo",
		"mu", "ne", "pr", "re", "se", "se", "si", "to", "un",
		"ses", "sess", "too", "too", "rew", "res", "und", "und", "qui", "qui",
		"qwerty", "zz", "x",
	} {
		got := suggestTexts(t, m, "/"+needle)
		for _, c := range all {
			if _, ok := commandRank(strings.ToLower(c.name), needle); !ok {
				continue
			}
			if !hasCommand(got, "/"+c.name) {
				t.Errorf("/%s hides /%s (it matches): list was %v", needle, c.name, got)
			}
		}
		for _, g := range got {
			if _, ok := commandRank(strings.TrimPrefix(g, "/"), needle); !ok {
				t.Errorf("/%s offers %s, which it cannot match", needle, g)
			}
		}
	}
}

// TestTheFirstKeystrokeLeavesTheListOpen is the complaint itself, on the letters
// where it was felt. The two excluded are /q and /y: no other command in the
// list contains those letters at all, so one row is the whole truth there and
// padding it would be inventing matches.
func TestTheFirstKeystrokeLeavesTheListOpen(t *testing.T) {
	m := suggestModel(t)
	for _, tc := range []struct{ needle, first string }{
		{"p", "/proxy"}, {"c", "/cd"}, {"s", "/setup"}, {"m", "/mode"},
		{"n", "/new"}, {"h", "/help"}, {"t", "/todo"}, {"r", "/resume"},
		{"l", "/lang"}, {"i", "/image"}, {"u", "/unimage"}, {"e", "/editor"},
		{"d", "/debug"},
	} {
		got := suggestTexts(t, m, "/"+tc.needle)
		if len(got) < 2 {
			t.Errorf("typing /%s leaves one row (%v) — the list closed on the first keystroke", tc.needle, got)
		}
		if got[0] != tc.first {
			t.Errorf("typing /%s highlights %v, want %s", tc.needle, got, tc.first)
		}
	}
}

// TestTheHighlightedRowIsTheOneAMeanUserMeant covers the ordering rules through
// the list, which is where they matter: the highlighted row is what a single
// Enter takes.
func TestTheHighlightedRowIsTheOneAMeanUserMeant(t *testing.T) {
	m := suggestModel(t)
	for _, tc := range []struct{ needle, first string }{
		{"c", "/cd"},     // four start with c; the shortest is the one meant
		{"m", "/mode"},   // models is the longer of the two obvious ones
		{"md", "/mode"},  // neither starts with "md" — the tie is on length
		{"do", "/todo"},  // letters together beat letters apart
		{"de", "/debug"}, // /debug starts with them
		{"ses", "/sessions"},
		{"sess", "/sessions"},
		{"rew", "/rewind"},
		{"unim", "/unimage"},
		{"ch", "/changes"},
		{"too", "/tools"},
	} {
		got := suggestTexts(t, m, "/"+tc.needle)
		if len(got) == 0 || got[0] != tc.first {
			t.Errorf("/%s highlights %v, want %s", tc.needle, got, tc.first)
		}
	}
}

// TestCommandRankGradesTheThreeWays is the ranker on its own, with synthetic
// names. The list tests above can only check the orders that happen to occur in
// one small vocabulary, and the grades need examples the vocabulary does not
// have: in real command names there is almost nothing to match with letters found
// apart, which is the point of the third grade being about one keystroke.
func TestCommandRankGradesTheThreeWays(t *testing.T) {
	for _, tc := range []struct {
		name, needle string
		want         int
	}{
		{"abcd", "abcd", 0}, // exact — the command and nothing else
		{"abcd", "", 0},     // nothing typed matches everything, equally
		{"abcd", "ab", 1},   // starts with it
		{"xabz", "ab", 2},   // letters side by side, match starting anywhere
		{"axbz", "ab", 3},   // the same letters, one letter between them
		{"axxbz", "ab", 4},  // and two
		{"abcd", "ad", 4},   // a gap is measured, and it is the gap after the first match
	} {
		got, ok := commandRank(tc.name, tc.needle)
		if !ok {
			t.Errorf("commandRank(%q, %q) did not match", tc.name, tc.needle)
			continue
		}
		if got != tc.want {
			t.Errorf("commandRank(%q, %q) = %d, want %d", tc.name, tc.needle, got, tc.want)
		}
	}

	// Nothing matched: a letter that is not in the name, or letters out of order.
	for _, tc := range []struct{ name, needle string }{
		{"abc", "abcd"}, // ran off the end of the name
		{"abc", "ba"},   // out of order
		{"abc", "d"},
	} {
		if _, ok := commandRank(tc.name, tc.needle); ok {
			t.Errorf("commandRank(%q, %q) matched and should not", tc.name, tc.needle)
		}
	}

	// A name that starts with the needle outranks any subsequence match, however
	// tight: somebody typing "to" means /todo, not a name that merely contains
	// those letters.
	prefix, _ := commandRank("todo", "to")
	loose, _ := commandRank("atod", "to")
	if prefix >= loose {
		t.Errorf("a prefix must outrank a subsequence: %d vs %d", prefix, loose)
	}
	// Equal ranks are settled by the shorter name, which is what puts /mode above
	// /models. That half lives in the sort, not in the rank, so it is pinned
	// through the list in TestTheHighlightedRowIsTheOneAMeanUserMeant.
	if mode, _ := commandRank("mode", "md"); mode != 0 {
		if models, _ := commandRank("models", "md"); models != mode {
			t.Errorf("mode=%d and models=%d should tie and be settled by length", mode, models)
		}
	}
}

// TestABareSlashIsTheWholeListInOrder: with nothing typed there is nothing to
// rank by, and the declared order is the one somebody chose. Sorting a list
// nobody has filtered is the same as shuffling it.
func TestABareSlashIsTheWholeListInOrder(t *testing.T) {
	m := suggestModel(t)
	got := suggestTexts(t, m, "/")
	if len(got) != len(m.commands()) {
		t.Fatalf("/ offers %d commands, want all %d", len(got), len(m.commands()))
	}
	for i, c := range m.commands() {
		if got[i] != "/"+c.name {
			t.Fatalf("row %d of / is %s, want /%s — the whole list keeps its declared order", i, got[i], c.name)
		}
	}
}

// TestTheListIsBigEnoughToBeAList: the window is capped so a list cannot swallow
// the reply the user is answering, but it has to be several rows. Matching the
// letters in order keeps more commands plausible after one keystroke, and a
// window of two would hide the reason for the change.
func TestTheListIsBigEnoughToBeAList(t *testing.T) {
	if suggestMaxRows < 8 {
		t.Fatalf("the command list is capped at %d rows, which shows fewer than the several commands one keystroke now offers", suggestMaxRows)
	}
	m := suggestModel(t)
	m.input.SetValue("/")
	m.updateSuggest()
	if h := m.suggestHeight(); h <= 4 {
		t.Fatalf("/ draws a %d-row box, too small to be a list", h)
	}
}

// TestEveryCommandThatWorksIsOffered pins the inventory, because the list of
// commands and the set of commands being two different sets is how a command
// hides: /debug answered, the README named it, and it appeared in neither the
// "/" list nor ctrl+p. Typing "/de" matched nothing at all.
func TestEveryCommandThatWorksIsOffered(t *testing.T) {
	m := suggestModel(t)
	offered := map[string]bool{}
	for _, c := range m.commands() {
		offered[c.name] = true
	}
	for _, name := range []string{
		"debug", "new", "todo", "rewind", "resume", "sessions", "cd", "image",
		"mode", "proxy", "setup", "models", "history", "mouse", "sidebar",
		"clear", "copy", "help", "quit", "editor", "lang", "tools", "unimage",
		"changes",
	} {
		if !offered[name] {
			t.Errorf("/%s works but is not offered", name)
		}
	}
	if got := suggestTexts(t, m, "/de"); len(got) == 0 || got[0] != "/debug" {
		t.Errorf("/de gave %v, want /debug first", got)
	}
}

// TestDebugRunsFromTheList covers the same command from the palette, which is
// the other place the list is drawn and the one ctrl+p reaches.
func TestDebugRunsFromTheList(t *testing.T) {
	m := suggestModel(t)
	m.palette = paletteState{open: true, query: "debug"}
	found := false
	for _, c := range m.filteredCommands() {
		if c.name == "debug" {
			found = true
			c.run(m)
		}
	}
	if !found {
		t.Fatal("ctrl+p cannot find /debug")
	}
	tail := m.history[max(len(m.history)-7, 0):]
	var rows []string
	for _, l := range tail {
		rows = append(rows, l.text)
	}
	joined := ansi.Strip(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "renderCalls") || !strings.Contains(joined, "md=") {
		t.Errorf("running /debug from the list printed:\n%s", joined)
	}
}
