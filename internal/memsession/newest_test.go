package memsession

import (
	"path/filepath"
	"testing"
)

// TestNewestSessionIDAnswersWhereTheUserWas covers the bare "-s": it has to name
// the most recently updated conversation with events, not the newest file, and not
// a /new nobody typed into. It also has to answer from disk rather than out of a
// map, because the session belongs to an earlier run.
func TestNewestSessionIDAnswersWhereTheUserWas(t *testing.T) {
	dir := t.TempDir()
	if got := NewestSessionID(dir); got != "" {
		t.Errorf("an empty store reports %q, want nothing", got)
	}

	s := NewPersistent(dir)
	s.SetIdentity("dmcode", "user")
	// The store stamps its own times, so the order here is the order written: two
	// conversations and a /new nobody typed into.
	for _, title := range []string{"s-older", "s-newer"} {
		sess := mustCreate(t, s, title)
		appendEvent(t, s, sess, userEvent("вопрос про "+title))
	}
	mustCreate(t, s, "s-empty")

	want := "s-newer"
	if got := NewestSessionID(dir); got != want {
		t.Fatalf("NewestSessionID = %q, want %q — the session written last, and a /new nobody used is not a candidate", got, want)
	}
	// A second call, and a second service, must give the same answer: the point is
	// that the answer comes off disk rather than out of one map's worth of state.
	for range 2 {
		if got := NewestSessionID(dir); got != want {
			t.Errorf("NewestSessionID = %q on a fresh service, want %q", got, want)
		}
	}
}

// TestNewestSessionIDOnAStoreThatDoesNotExistYet: a first run has no directory,
// which is not a failure and must not look like one.
func TestNewestSessionIDOnAStoreThatDoesNotExistYet(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created-yet")
	if got := NewestSessionID(missing); got != "" {
		t.Errorf("NewestSessionID(%q) = %q, want nothing", missing, got)
	}
}
