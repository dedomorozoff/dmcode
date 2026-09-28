package memsession

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"
)

// maxLoadEvents caps how many events a session file contributes when it is read
// back. A very long session is a real thing — a day of work is thousands of
// events — and deciding what fits a prompt is the model's job, not the store's.
// The tail is what a resumed conversation needs, and the cut is reported rather
// than applied silently.
const maxLoadEvents = 2000

// maxLineBytes bounds one line of a session file. A tool result can be large
// and an event holding it is one line, so the default 64 KiB scanner buffer is
// too small; the ceiling keeps a runaway line from being read into memory whole.
const maxLineBytes = 8 * 1024 * 1024

// metaKind marks the header line of a session file.
const metaKind = "meta"

// Meta is the header a session file starts with.
//
// It is stored separately from the events so the session list can be built
// without reading a single conversation: one short line per file instead of
// megabytes. Everything in it is also derivable from the events, which is why a
// file with a damaged header is still readable.
type Meta struct {
	Kind    string    `json:"kind"`
	ID      string    `json:"id"`
	Title   string    `json:"title,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// store is the persistence behind a Service. It is an interface so the
// memory-only case is a nil store rather than a directory nobody touches, and
// so a test can count what was written.
type store interface {
	// writeMeta creates or refreshes the header of a session file.
	writeMeta(st *stored) error
	// appendEvent adds one event line to an existing file.
	appendEvent(id string, ev *session.Event) error
	// rewrite replaces a whole file: the header plus the events that survived a
	// rewind. It must be atomic, so a crash mid-write cannot leave a session
	// that parses as a shorter conversation.
	rewrite(st *stored) error
	// read returns a whole session: metadata, events, and whether the tail was
	// cut by maxLoadEvents.
	read(id string) (Meta, []*session.Event, bool, error)
	// readMeta reads just the header.
	readMeta(id string) (Meta, error)
	// listMeta reads the headers of every session in the store.
	listMeta() ([]Meta, error)
	// countEvents counts the event lines in a session file without decoding
	// them, so a list can say how long a conversation is without reading one.
	countEvents(id string) (n int, truncated bool, err error)
	// remove deletes a session file.
	remove(id string) error
}

// fileStore keeps one JSONL file per session under dir.
type fileStore struct {
	dir string
}

// path maps a session id to its file. Ids come from the session key, which
// dmcode generates but the user may also type into /resume, so the name is
// sanitised rather than trusted: a session id is not a filename until it has
// been reduced to characters that cannot walk out of the directory.
func (f *fileStore) path(id string) string {
	return filepath.Join(f.dir, safeName(id)+".jsonl")
}

// safeName reduces an arbitrary string to something that can only ever name a
// file inside the store directory.
//
// Separators and dots are both replaced: a separator would walk out of the
// directory, and a run of dots is the other half of that trick — ".." names the
// parent, so no id may contain one. An empty result becomes "session" rather
// than "", which would name the directory itself.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "session"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func (f *fileStore) ensureDir() error {
	// 0700: a session file holds everything the user typed and everything the
	// agent read. The prompt history next to it is already 0600 for the
	// same reason.
	return os.MkdirAll(f.dir, 0o700)
}

func metaLine(st *stored) string {
	b, err := json.Marshal(Meta{
		Kind:    metaKind,
		ID:      st.id,
		Title:   st.title,
		Created: st.created,
		Updated: st.updated,
	})
	if err != nil {
		// Meta is five plain fields; a failure here is a programming error, and
		// a header line is still better than a panic in a write path.
		return `{"kind":"meta"}` + "\n"
	}
	return string(b) + "\n"
}

func (f *fileStore) writeMeta(st *stored) error {
	if err := f.ensureDir(); err != nil {
		return err
	}
	p := f.path(st.id)
	// The header is swapped in place rather than by replacing the file: a
	// rename would empty the session, and the header is refreshed on every
	// rename of a session that already has events.
	return f.replaceFirstLine(p, metaLine(st))
}

func (f *fileStore) appendEvent(id string, ev *session.Event) error {
	if err := f.ensureDir(); err != nil {
		return err
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("cannot encode event: %w", err)
	}
	fh, err := os.OpenFile(f.path(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	// The event and its newline go out in one Write: a session is a list of
	// lines, and a line split across two writes is a line nobody can parse.
	if _, err := fh.Write(append(line, '\n')); err != nil {
		return err
	}
	// Synced rather than left to the OS: a session file that is a little
	// behind the conversation in it is a session that rewinds to the wrong
	// place after a crash, and the cost is one fsync per event.
	return fh.Sync()
}

func (f *fileStore) rewrite(st *stored) error {
	if err := f.ensureDir(); err != nil {
		return err
	}
	path := f.path(st.id)
	tmp := path + ".tmp"
	fh, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// os.Create would ask for 0666, and on Windows the umask that would trim
	// it is not applied — the file would come out world-writable whatever the
	// process umask says. The mode is asked for explicitly, and a filesystem
	// that refuses to narrow it is not worth failing a turn over.
	_ = fh.Chmod(0o600)
	w := bufio.NewWriter(fh)
	if _, err := w.WriteString(metaLine(st)); err != nil {
		return f.abort(fh, tmp, err)
	}
	enc := json.NewEncoder(w)
	for _, ev := range st.events {
		if err := enc.Encode(ev); err != nil {
			return f.abort(fh, tmp, fmt.Errorf("cannot encode event: %w", err))
		}
	}
	if err := w.Flush(); err != nil {
		return f.abort(fh, tmp, err)
	}
	if err := fh.Sync(); err != nil {
		return f.abort(fh, tmp, err)
	}
	if err := fh.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// Rename is the atomic step: before it the session is the old one, after it
	// the new one, and there is no moment in between where a crash leaves a
	// file that parses as a conversation with a hole in it.
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// abort closes a half-written temp file and removes it, so a failed rewrite
// leaves the previous session exactly as it was.
func (f *fileStore) abort(fh *os.File, tmp string, err error) error {
	fh.Close()
	os.Remove(tmp)
	return err
}

// replaceFirstLine swaps the header of a file, keeping everything after it.
func (f *fileStore) replaceFirstLine(path, line string) error {
	data, err := os.ReadFile(path)
	rest := ""
	switch {
	case err == nil:
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			rest = string(data[i+1:])
		}
	case !os.IsNotExist(err):
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(line+rest), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (f *fileStore) read(id string) (Meta, []*session.Event, bool, error) {
	fh, err := os.Open(f.path(id))
	if err != nil {
		return Meta{}, nil, false, err
	}
	defer fh.Close()
	return readLines(fh)
}

func (f *fileStore) readMeta(id string) (Meta, error) {
	fh, err := os.Open(f.path(id))
	if err != nil {
		return Meta{}, err
	}
	defer fh.Close()
	// Only the first line is wanted, and reading it through a Scanner stops
	// there — the rest of a long conversation is never touched.
	sc := newScanner(fh)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Meta{}, err
		}
		return Meta{}, errors.New("session file is empty")
	}
	return decodeMeta(sc.Bytes())
}

func (f *fileStore) listMeta() ([]Meta, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		if os.IsNotExist(err) {
			// No directory yet simply means no sessions yet; that is not a
			// failure, and the first session will create it.
			return nil, nil
		}
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		m, err := f.readMeta(id)
		if err != nil || m.ID == "" {
			// A file whose header will not parse is skipped rather than fatal:
			// one damaged session must not hide every other one.
			continue
		}
		// The file's own mtime is when the session was last touched, which is
		// what the list sorts by. Rewriting the header on every event to keep
		// it fresh would double the writes of a streaming turn for one number
		// the filesystem already tracks.
		if info, err := os.Stat(f.path(id)); err == nil {
			m.Updated = info.ModTime()
		}
		out = append(out, m)
	}
	return out, nil
}

func (f *fileStore) countEvents(id string) (int, bool, error) {
	fh, err := os.Open(f.path(id))
	if err != nil {
		return 0, false, err
	}
	defer fh.Close()
	sc := newScanner(fh)
	var (
		n       int
		hasMeta bool
	)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !hasMeta {
			if _, err := decodeMeta([]byte(line)); err == nil {
				hasMeta = true
				continue
			}
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return n, false, err
	}
	return n, n > maxLoadEvents, nil
}

func (f *fileStore) remove(id string) error {
	err := os.Remove(f.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func newScanner(r *os.File) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	return sc
}

// decodeMeta reads a header line, accepting a line that is not a header at all.
//
// A session file is append-only and its header is rewritten on a rename, so a
// file can legitimately begin with an event — an older layout, or a header
// write that lost a race. Decoding an event into a Meta yields an empty id,
// which the caller treats as "no header" and derives from the events instead.
func decodeMeta(line []byte) (Meta, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return Meta{}, errors.New("empty header")
	}
	if strings.HasPrefix(trimmed, "{") {
		var m Meta
		if err := json.Unmarshal([]byte(trimmed), &m); err == nil && m.Kind == metaKind {
			return m, nil
		}
	}
	return Meta{}, errors.New("not a session header")
}

// readLines parses a whole session file.
//
// A line that will not parse is skipped rather than fatal, matching how the
// prompt history treats a hand-edited line: a truncated last line after a crash
// costs one event, while refusing the file costs the entire conversation.
func readLines(fh *os.File) (Meta, []*session.Event, bool, error) {
	sc := newScanner(fh)
	var (
		meta     Meta
		events   []*session.Event
		haveMeta bool
	)
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		if trimmed == "" {
			continue
		}
		if !haveMeta {
			if m, err := decodeMeta([]byte(trimmed)); err == nil {
				meta, haveMeta = m, true
				continue
			}
		}
		var ev session.Event
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			continue
		}
		events = append(events, &ev)
	}
	if err := sc.Err(); err != nil {
		return meta, events, false, err
	}
	truncated := false
	if len(events) > maxLoadEvents {
		events = events[len(events)-maxLoadEvents:]
		truncated = true
	}
	return meta, events, truncated, nil
}

var _ store = (*fileStore)(nil)
