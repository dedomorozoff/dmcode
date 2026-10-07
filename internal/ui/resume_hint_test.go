package ui

import (
	"strings"
	"testing"
)

// The line dmcode leaves behind when it exits, and the decision -s makes before
// anything is built.
//
// The transcript was on the alternate screen, so by the time the program is over
// everything it said about which session is in is gone — the header, the sidebar,
// every /sessions row. The id is opaque and lives in a directory the user has
// never opened, so printing the exact command is what makes the session usable at
// all rather than something to look up.

// TestResumeHintSaysTheCommandThatWorks: the line has to carry the id twice —
// once as the subject and once in the command, because a user reading it needs to
// see which session it is about before pasting anything — and the command has to
// be spelled the way the parser spells it.
func TestResumeHintSaysTheCommandThatWorks(t *testing.T) {
	hint := resumeHint("sess-42")
	if !strings.Contains(hint, "sess-42") {
		t.Fatalf("the hint does not name the session: %q", hint)
	}
	if !strings.Contains(hint, "dmcode -s sess-42") {
		t.Fatalf("the hint does not give a command that works: %q", hint)
	}
	// A flag where the argument belongs would be printed by a line nobody could
	// follow, and that is exactly the mistake a bare -s invites.
	if strings.Contains(hint, "-s -") {
		t.Errorf("the printed command has a flag where its argument belongs: %q", hint)
	}
}

// TestResumeHintSaysNothingWithoutASession: a start that never got one — a model
// that failed to build, a store that could not be written — must not print a
// command that would resume nothing and look like it had.
func TestResumeHintSaysNothingWithoutASession(t *testing.T) {
	for _, id := range []string{"", "   ", "\t"} {
		if hint := resumeHint(id); hint != "" {
			t.Errorf("resumeHint(%q) = %q, want nothing", id, hint)
		}
	}
}

// TestResumeDecidesWhichSessionToOpen is the whole of what -s asks for, and the
// mistake to guard against is the id arriving after the runner has been built
// around the fresh one InitialModel minted.
func TestResumeDecidesWhichSessionToOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Resume
		want string
	}{
		{"no flag at all", Resume{}, ""},
		{"asked, but the store had nothing to name", Resume{Asked: true}, ""},
		{"asked for one by id", Resume{Asked: true, ID: "sess-42"}, "sess-42"},
		// An id with the flag absent is not a request: nothing asked to resume
		// anything, so the start stays a fresh conversation.
		{"an id nobody asked for", Resume{ID: "sess-42"}, ""},
	} {
		if got := tc.r.sessionToOpen(); got != tc.want {
			t.Errorf("%s: sessionToOpen() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
