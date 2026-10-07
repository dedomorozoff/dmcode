package memsession

// NewestSessionID reports the id of the most recently updated session in the
// store, or "" when there is none to resume.
//
// It exists for a bare "dmcode -s": the user is asking for "where I was", not for
// a session they can name, and the id printed when they left is what they would
// paste if they did name one. It reads only the header of each file — the same
// short read /sessions costs — so answering it loads no conversation memory.
//
// A /new nobody typed into is not a candidate: Summaries leaves a session with no
// events out, which is the right answer here too, since resuming it would open a
// conversation that never happened.
func NewestSessionID(dir string) string {
	s := NewPersistent(dir)
	if s == nil {
		return ""
	}
	for _, sum := range s.Summaries() {
		return sum.ID
	}
	return ""
}
