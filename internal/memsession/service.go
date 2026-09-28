// Package memsession is the session store dmcode runs on.
//
// It replaces session.InMemoryService for one reason: that service hands out a
// copy of the event slice, so nothing upstream can take an event back. A rewind
// to the previous user message is impossible against it, and so is switching
// between conversations already in the store. Everything here exists to make
// both of those ordinary operations rather than workarounds.
package memsession

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/platform"
	"google.golang.org/adk/v2/session"
)

// userAuthor is the author name ADK stamps on an event carrying something the
// user said. The rewind keys off it rather than off the text, so a turn that
// ended in a tool call rewinds as cleanly as one that ended in prose.
const userAuthor = "user"

// Service stores conversations for one app and one user.
//
// The zero value is not usable; call NewMemory or NewPersistent. Every method
// is safe for concurrent use: the ADK runner reads the session from the turn
// goroutine while the UI rewinds or deletes it from the event loop, and a map
// written from both is a crash with a delay.
type Service struct {
	mu       sync.RWMutex
	sessions map[string]*stored
	order    []string // map keys, oldest first, so List is stable
	store    store    // nil for a memory-only service
	appName  string
	userID   string

	// writeErr is the first failure the store reported. A turn must not fail
	// because a session file could not be written — the conversation is in
	// memory and still usable — so the error is kept here for the UI to
	// surface once instead of being returned from AppendEvent.
	writeErr error
}

// stored is one conversation: its events, the state those events built up, and
// the bookkeeping the store needs.
type stored struct {
	id        string
	appName   string
	userID    string
	created   time.Time
	updated   time.Time
	events    []*session.Event
	state     map[string]any
	title     string
	onDisk    bool
	loaded    bool
	truncated bool // the file held more events than maxLoadEvents
}

// NewMemory returns a service that keeps everything in memory and writes
// nothing to disk.
//
// It is what a sub-agent gets: a throwaway session for one delegated task,
// which has no business in the user's session list or surviving the process.
func NewMemory() *Service {
	return newService(nil)
}

// NewPersistent returns a service that mirrors every session into dir as JSONL.
//
// A blank dir falls back to a memory-only service, so a caller that could not
// resolve a home directory degrades to losing history on exit rather than
// failing to start.
func NewPersistent(dir string) *Service {
	if strings.TrimSpace(dir) == "" {
		return NewMemory()
	}
	return newService(&fileStore{dir: dir})
}

func newService(st store) *Service {
	return &Service{
		sessions: make(map[string]*stored),
		store:    st,
		appName:  "dmcode",
		userID:   "user",
	}
}

// SetIdentity names the app and user this service serves. The ADK session key
// is (app, user, session), so a store that answered every request with its own
// pair would let two users collide on one session id.
func (s *Service) SetIdentity(appName, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if appName != "" {
		s.appName = appName
	}
	if userID != "" {
		s.userID = userID
	}
}

// LastWriteError reports the first storage failure since the service was
// created, or nil. The UI shows it and clears it: a session file that cannot be
// written is worth saying out loud once, not once per token.
func (s *Service) LastWriteError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writeErr
}

// ClearWriteError forgets the recorded storage failure.
func (s *Service) ClearWriteError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeErr = nil
}

// recordWriteErr remembers a storage failure. The caller must hold the write
// lock: every path that reports a write error is a method that already has it,
// and sync.RWMutex is not reentrant, so taking it again here would deadlock the
// turn against the failure it is trying to report.
func (s *Service) recordWriteErr(err error) {
	if err == nil || s.writeErr != nil {
		return
	}
	s.writeErr = err
}

// key builds the map key for a session. Using the ADK triple rather than the id
// alone keeps a service shared by two users from handing one user's history to
// the other.
func key(appName, userID, sessionID string) string {
	return appName + "\x00" + userID + "\x00" + sessionID
}

// Create registers an empty session, or fails if the id is taken.
func (s *Service) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req == nil || req.AppName == "" || req.UserID == "" {
		return nil, fmt.Errorf("app_name and user_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id := req.SessionID
	if id == "" {
		id = platform.NewUUID(ctx)
	}
	k := key(req.AppName, req.UserID, id)
	if _, exists := s.sessions[k]; exists {
		return nil, fmt.Errorf("session %s already exists", id)
	}

	now := platform.Now(ctx)
	st := &stored{
		id:      id,
		appName: req.AppName,
		userID:  req.UserID,
		created: now,
		updated: now,
		state:   make(map[string]any),
	}
	applyStateDelta(st.state, req.State)
	s.sessions[k] = st
	s.order = append(s.order, k)

	if s.store != nil {
		if err := s.store.writeMeta(st); err != nil {
			s.recordWriteErr(err)
		} else {
			st.onDisk = true
		}
	}
	return &session.CreateResponse{Session: s.view(st, nil)}, nil
}

// Get returns the stored session, or session.ErrNotFound so a caller can tell
// a missing session from a broken one — the runner auto-creates on exactly that
// error and treats any other failure as fatal.
func (s *Service) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req == nil || req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("app_name, user_id, session_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load(req.AppName, req.UserID, req.SessionID)
	if err != nil {
		return nil, err
	}
	events := filterEvents(st.events, req.NumRecentEvents, req.After)
	return &session.GetResponse{Session: s.view(st, events)}, nil
}

// List returns every session of this app and user, oldest first.
func (s *Service) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("app_name and user_id are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := &session.ListResponse{}
	for _, k := range s.order {
		st := s.sessions[k]
		if st.appName != req.AppName || st.userID != req.UserID {
			continue
		}
		out.Sessions = append(out.Sessions, s.view(st, nil))
	}
	return out, nil
}

// Delete drops a session. A missing one is not an error, matching ADK: the
// caller's intent — that it be gone — already holds.
func (s *Service) Delete(ctx context.Context, req *session.DeleteRequest) error {
	if req == nil || req.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(req.AppName, req.UserID, req.SessionID)
	st, ok := s.sessions[k]
	if !ok {
		return nil
	}
	delete(s.sessions, k)
	if i := slices.Index(s.order, k); i >= 0 {
		s.order = slices.Delete(s.order, i, i+1)
	}
	if s.store != nil {
		if err := s.store.remove(st.id); err != nil {
			s.recordWriteErr(err)
		}
	}
	return nil
}

// AppendEvent stores one event and mirrors it to disk.
//
// Two obligations come from ADK rather than from us. An event arriving with no
// id must end up with one — events built as struct literals by an agent or tool
// never pass through session.NewEvent — and a partial event must not be stored
// at all, because the streaming chunks would be replayed as conversation.
func (s *Service) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	if ev == nil {
		return fmt.Errorf("event is required")
	}
	if ev.Partial {
		return nil
	}
	if ev.ID == "" {
		ev.ID = platform.NewUUID(ctx)
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = platform.Now(ctx)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load(sess.AppName(), sess.UserID(), sess.ID())
	if err != nil {
		// The runner holds a session this store never registered — a
		// registration lost to a failed write, most likely. Adopt it rather
		// than losing the turn: the events are already in hand.
		now := ev.Timestamp
		st = &stored{
			id:      sess.ID(),
			appName: sess.AppName(),
			userID:  sess.UserID(),
			created: now,
			updated: now,
			state:   make(map[string]any),
		}
		k := key(st.appName, st.userID, st.id)
		s.sessions[k] = st
		s.order = append(s.order, k)
		if s.store != nil {
			if werr := s.store.writeMeta(st); werr != nil {
				s.recordWriteErr(werr)
			} else {
				st.onDisk = true
			}
		}
	}

	applyStateDelta(st.state, ev.Actions.StateDelta)
	st.events = append(st.events, ev)
	st.updated = ev.Timestamp
	// A session is named after its first prompt, and the name lives in the file
	// header — so deriving it here has to rewrite the header. Only once per
	// session: the check is what keeps a long conversation from rewriting its
	// first line on every event.
	named := false
	if st.title == "" && ev.Author == userAuthor {
		if title := titleFromEvent(ev); title != "" {
			st.title = title
			named = true
		}
	}

	if s.store != nil {
		if werr := s.store.appendEvent(st.id, ev); werr != nil {
			s.recordWriteErr(werr)
		}
		if named {
			if werr := s.store.writeMeta(st); werr != nil {
				s.recordWriteErr(werr)
			}
		}
	}
	return nil
}

// RewindToLastUserMessage truncates the session to just before its last user
// message and returns that message's text.
//
// The message itself goes too, not only what came after: the point is to hand
// the user back a prompt they can edit and send again, and leaving it in the
// model's memory would make the next turn answer a question already answered.
// The state built up by the dropped events goes with them — the deltas are
// replayed from what survives, so a rewound session cannot inherit a state
// change whose event no longer exists.
//
// ok is false when the session has no user message to rewind to, which is the
// caller's cue to say so rather than to pretend something happened.
func (s *Service) RewindToLastUserMessage(ctx context.Context, appName, userID, sessionID string) (text string, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load(appName, userID, sessionID)
	if err != nil {
		return "", false, err
	}
	idx := -1
	for i := len(st.events) - 1; i >= 0; i-- {
		if st.events[i].Author == userAuthor {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false, nil
	}

	text = eventText(st.events[idx])
	if st.title == truncateTitle(text) {
		st.title = ""
	}
	st.events = st.events[:idx]
	replayState(st)
	if n := len(st.events); n > 0 {
		st.updated = st.events[n-1].Timestamp
	}

	if s.store != nil {
		if werr := s.store.rewrite(st); werr != nil {
			s.recordWriteErr(werr)
		}
	}
	return text, true, nil
}

// Summary is the short description of a session the list shows.
type Summary struct {
	ID        string
	Title     string
	Created   time.Time
	Updated   time.Time
	Events    int
	OnDisk    bool
	Truncated bool
}

// Summaries lists the sessions newest first.
//
// It unions what is in memory with what is on disk and reads only the header
// of a file it has not touched, so opening /sessions costs one short read per
// saved conversation rather than a whole conversation each. A session with no
// events is left out — it is a /new the user never typed into, and a list of
// empty rows helps nobody.
func (s *Service) Summaries() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]int, len(s.sessions))
	out := make([]Summary, 0, len(s.sessions))
	add := func(st *stored) {
		if len(st.events) == 0 {
			return
		}
		seen[st.id] = len(out)
		out = append(out, Summary{
			ID:        st.id,
			Title:     st.title,
			Created:   st.created,
			Updated:   st.updated,
			Events:    len(st.events),
			OnDisk:    st.onDisk,
			Truncated: st.truncated,
		})
	}
	for _, k := range s.order {
		if st := s.sessions[k]; st != nil {
			add(st)
		}
	}
	// Sessions left by an earlier run are not in memory, so the list is not
	// complete until the files are consulted too.
	if s.store != nil {
		if metas, err := s.store.listMeta(); err == nil {
			for _, m := range metas {
				if _, ok := seen[m.ID]; ok {
					continue
				}
				evs, truncated, err := s.store.countEvents(m.ID)
				if err != nil || evs == 0 {
					continue
				}
				created := m.Created
				if created.IsZero() {
					created = m.Updated
				}
				out = append(out, Summary{
					ID: m.ID, Title: m.Title, Created: created, Updated: m.Updated,
					Events: evs, OnDisk: true, Truncated: truncated,
				})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Summary) int {
		return b.Updated.Compare(a.Updated)
	})
	return out
}

// Turn is one exchange: what the user said and what came back.
type Turn struct {
	User   string
	Agent  string
	Broken bool // the user spoke and nothing answered — an error, or an Esc
}

// Transcript is a session's recent conversation in a form the UI can print
// without knowing anything about ADK events.
type Transcript struct {
	ID        string
	Title     string
	Turns     []Turn
	Truncated bool
}

// TranscriptOf renders the last maxTurns exchanges of a session.
//
// It exists because switching to a session that is not on screen would
// otherwise show an empty transcript next to a model that remembers everything:
// the user would read "nothing happened here" while the agent reads the full
// history. A few lines of context are the difference between a switch that
// looks like a bug and one that looks like a switch.
func (s *Service) TranscriptOf(appName, userID, sessionID string, maxTurns int) (Transcript, error) {
	if maxTurns <= 0 {
		maxTurns = 10
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load(appName, userID, sessionID)
	if err != nil {
		return Transcript{}, err
	}
	tr := Transcript{ID: st.id, Title: st.title, Truncated: st.truncated}

	// Walking backwards, the newest answer is met before the question it
	// answers, so an answer is held in pending until the prompt below it proves
	// what it belongs to. Reversed at the end to get chronological order back.
	type pair struct {
		user  string
		agent string
	}
	var pairs []pair
	pending := ""
	for i := len(st.events) - 1; i >= 0 && len(pairs) < maxTurns; i-- {
		text := eventText(st.events[i])
		if text == "" {
			continue
		}
		if st.events[i].Author == userAuthor {
			pairs = append(pairs, pair{user: text, agent: pending})
			pending = ""
			continue
		}
		// Only the first answer met belongs to the prompt about to appear; the
		// rest belonged to a prompt already collected.
		if pending == "" {
			pending = text
		} else if len(pairs) > 0 {
			if pairs[len(pairs)-1].agent == "" {
				pairs[len(pairs)-1].agent = text
			} else {
				pairs[len(pairs)-1].agent += "\n" + text
			}
		}
	}
	tr.Turns = make([]Turn, 0, len(pairs))
	for i := len(pairs) - 1; i >= 0; i-- {
		tr.Turns = append(tr.Turns, Turn{
			User:   pairs[i].user,
			Agent:  pairs[i].agent,
			Broken: pairs[i].agent == "",
		})
	}
	return tr, nil
}

// Ensure registers a session if it is not known yet, and returns its id.
//
// The runner auto-creates through Create, but the UI needs a session to exist
// before the first message: /new has to produce a row in /sessions even if the
// user types nothing, and a later run has to find what an earlier one left.
func (s *Service) Ensure(ctx context.Context, appName, userID, sessionID string) (string, error) {
	if sessionID == "" {
		sessionID = platform.NewUUID(ctx)
	}
	_, err := s.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return sessionID, err
	}
	return sessionID, nil
}

// Discover registers every session file in the store directory.
//
// Metadata only, so a home directory holding a hundred conversations costs a
// hundred short reads and no conversation memory; a session's events are read
// when it is first opened. Returns how many were newly discovered.
func (s *Service) Discover(ctx context.Context, appName, userID string) int {
	if s.store == nil {
		return 0
	}
	metas, err := s.store.listMeta()
	if err != nil {
		s.recordWriteErr(err)
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range metas {
		k := key(appName, userID, m.ID)
		if _, ok := s.sessions[k]; ok {
			continue
		}
		created, updated := m.Created, m.Updated
		if created.IsZero() {
			created = updated
		}
		s.sessions[k] = &stored{
			id:      m.ID,
			appName: appName,
			userID:  userID,
			title:   m.Title,
			created: created,
			updated: updated,
			state:   make(map[string]any),
			onDisk:  true,
		}
		s.order = append(s.order, k)
		n++
	}
	// Discovered sessions are older than the ones made this run, so the order
	// has to be put back in chronological order for /sessions to read top-down
	// and for List to keep its oldest-first promise.
	slices.SortStableFunc(s.order, func(a, b string) int {
		return s.sessions[a].updated.Compare(s.sessions[b].updated)
	})
	return n
}

// Rename gives a session a title of the user's choosing, replacing the one
// derived from its first prompt.
func (s *Service) Rename(appName, userID, sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load(appName, userID, sessionID)
	if err != nil {
		return err
	}
	st.title = truncateTitle(title)
	if s.store != nil {
		if werr := s.store.writeMeta(st); werr != nil {
			s.recordWriteErr(werr)
		}
	}
	return nil
}

// load returns the stored session, reading it from disk the first time.
//
// The caller holds the write lock: a read can be a lazy load, and two goroutines
// loading one session at the same time would each append to it.
func (s *Service) load(appName, userID, sessionID string) (*stored, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	k := key(appName, userID, sessionID)
	st, ok := s.sessions[k]
	if !ok {
		return nil, fmt.Errorf("%w: %q", session.ErrNotFound, sessionID)
	}
	if st.loaded || s.store == nil {
		st.loaded = true
		return st, nil
	}
	meta, events, truncated, err := s.store.read(st.id)
	if err != nil {
		// A session that cannot be read is still a session: an unreadable file
		// must not turn into a failure on the next message.
		st.loaded = true
		return st, nil
	}
	applyMeta(st, meta)
	st.events = events
	st.truncated = truncated
	for _, ev := range events {
		applyStateDelta(st.state, ev.Actions.StateDelta)
	}
	st.loaded = true
	return st, nil
}

// view wraps a stored session as the read-only session.Session the ADK
// interfaces expect.
//
// Events come back as a copy of the slice and state as a copy of the map, for
// the same reason the in-memory service does it: the runner mutates what it is
// given, and a mutation that never reaches AppendEvent would leave the store
// and the turn disagreeing about what was said.
func (s *Service) view(st *stored, events []*session.Event) session.Session {
	if events == nil {
		events = st.events
	}
	return &viewSession{
		id:      st.id,
		appName: st.appName,
		userID:  st.userID,
		updated: st.updated,
		events:  slices.Clone(events),
		state:   maps.Clone(st.state),
	}
}

// filterEvents applies the two optional filters a Get may carry. Neither can be
// expressed by the caller, because the caller never sees the stored slice.
func filterEvents(events []*session.Event, numRecent int, after time.Time) []*session.Event {
	out := events
	if numRecent > 0 && len(out) > numRecent {
		out = out[len(out)-numRecent:]
	}
	if !after.IsZero() && len(out) > 0 {
		cut := 0
		for cut < len(out) && out[cut].Timestamp.Before(after) {
			cut++
		}
		out = out[cut:]
	}
	return out
}

// applyStateDelta folds an event's state changes into the session state,
// dropping the temporary keys. ADK marks scratch state with a "temp:" prefix
// and strips it on the way into storage; keeping those keys would hand the next
// turn state that no surviving event describes.
func applyStateDelta(state map[string]any, delta map[string]any) {
	for k, v := range delta {
		if strings.HasPrefix(k, session.KeyPrefixTemp) {
			delete(state, k)
			continue
		}
		state[k] = v
	}
}

// replayState rebuilds the state from the events that survived a rewind.
//
// Recomputing rather than undoing is deliberate: an inverse per key would have
// to know which event set it and what the value was before, and a key written
// twice before the rewind has no single event to look at.
func replayState(st *stored) {
	fresh := make(map[string]any)
	for _, ev := range st.events {
		applyStateDelta(fresh, ev.Actions.StateDelta)
	}
	st.state = fresh
}

// eventText returns the plain text of an event, ignoring tool calls — a
// function call has no prose to show a user.
func eventText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range ev.Content.Parts {
		if p != nil && p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// titleFromEvent is the text a session is named after: its first user message.
func titleFromEvent(ev *session.Event) string {
	return truncateTitle(eventText(ev))
}

const maxTitleCells = 60

// truncateTitle keeps a title to one short line. A session named after a whole
// prompt is unusable in a list, and one line is all that fits there anyway.
func truncateTitle(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	runes := []rune(s)
	if len(runes) <= maxTitleCells {
		return s
	}
	return strings.TrimSpace(string(runes[:maxTitleCells])) + "…"
}

func applyMeta(st *stored, m Meta) {
	if m.Title != "" {
		st.title = m.Title
	}
	if !m.Created.IsZero() {
		st.created = m.Created
	}
	if !m.Updated.IsZero() {
		st.updated = m.Updated
	}
}

// viewSession is the read-only session.Session handed to the runner.
type viewSession struct {
	id      string
	appName string
	userID  string
	updated time.Time
	events  []*session.Event
	state   map[string]any
}

func (v *viewSession) ID() string                { return v.id }
func (v *viewSession) AppName() string           { return v.appName }
func (v *viewSession) UserID() string            { return v.userID }
func (v *viewSession) LastUpdateTime() time.Time { return v.updated }
func (v *viewSession) Events() session.Events    { return eventList(v.events) }
func (v *viewSession) State() session.State      { return viewState{state: v.state} }

type eventList []*session.Event

func (e eventList) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, ev := range e {
			if !yield(ev) {
				return
			}
		}
	}
}

func (e eventList) Len() int { return len(e) }

func (e eventList) At(i int) *session.Event {
	if i >= 0 && i < len(e) {
		return e[i]
	}
	return nil
}

type viewState struct{ state map[string]any }

func (s viewState) Get(k string) (any, error) {
	v, ok := s.state[k]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

// Set writes into the copy the view handed out. It is deliberately not the
// store: the only way an event becomes part of the conversation is
// AppendEvent, and a state write that skipped it would exist in one turn's
// prompt and in no session.
func (s viewState) Set(k string, v any) error {
	s.state[k] = v
	return nil
}

func (s viewState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s.state {
			if !yield(k, v) {
				return
			}
		}
	}
}

var _ session.Service = (*Service)(nil)
var _ session.Session = (*viewSession)(nil)
var _ session.Events = eventList{}
var _ session.State = viewState{}
