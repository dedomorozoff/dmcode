package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"sync"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
)

// PoolMember is one endpoint in the failover pool: the provider it stands for
// and the client that talks to it. Keeping the provider alongside the client is
// what lets the UI be told which host is actually answering, and what lets a
// later model switch rewrite the pool without re-probing anything.
type PoolMember struct {
	Prov config.Provider
	LLM  model.LLM
}

// SwitchEvent records a failover that actually happened, so the transcript can
// say which endpoint answered instead of leaving the user to wonder why the
// answer quality just changed.
type SwitchEvent struct {
	From   config.Provider
	To     config.Provider
	Reason string // short form of the failure that forced the move
}

// BackupSource produces the fallback endpoints. It is a function rather than a
// slice so the free-provider probe is never paid by a user whose own key works:
// startup must not sit through a five-second scan of candidates that may never
// be needed, and the scan inside a failing turn is already behind a spinner.
type BackupSource func(ctx context.Context) ([]PoolMember, error)

// failoverModel presents several endpoints as one model.LLM and moves to the
// next one when the current one cannot serve a call.
//
// Two rules keep this from corrupting a turn:
//
//   - It never reroutes after output has been yielded. By the time the user can
//     see text, re-running the same request on another host would print the same
//     answer twice, and the transcript has no way to retract the first copy.
//   - It only reroutes on failures that are the endpoint's fault. A rejected
//     request is rejected by its peers too, and walking the pool would just
//     spread one decided failure across every host.
//
// Either way the conversation is untouched: the runner keeps the session, and
// only the transport for a single call is swapped. That is also why a failover
// mid tool loop is safe — the failed call simply never happened.
type failoverModel struct {
	mu           sync.Mutex
	members      []PoolMember
	backups      BackupSource
	backupsTried bool
	active       int
	onSwitch     func(SwitchEvent)
	onRetry      func(RetryEvent)
	retry        RetryPolicy
}

// NewFailoverModel builds the pool for a session: the providers the user asked
// for, in order, with the free endpoints behind them as a lazily probed
// reserve.
//
// The wrapper is kept even for a single configured provider, because that is
// precisely the case where the reserve is worth having. Returning the bare
// client instead would make a working Groq key indistinguishable from a session
// with nowhere to go when it starts returning 429s.
// NewFailoverModel builds the pool for a session: the providers the user asked
// for, in order, with a lazily probed reserve behind them.
//
// The wrapper is kept even for a single configured provider, because that is
// precisely the case where the reserve is worth having. Returning the bare
// client instead would make a working Groq key indistinguishable from a session
// with nowhere to go when it starts returning 429s. backups may be nil: a pool
// without a reserve simply reports the first failure instead of hunting for a
// free endpoint mid-turn.
// RetryEvent records a retry that is about to happen, so the transcript can say
// what the wait is for instead of showing a spinner that looks hung.
type RetryEvent struct {
	// Attempt is the try about to be made, counting from 2 — the first attempt
	// is not a retry and is never announced.
	Attempt int
	// Max is the total number of attempts this endpoint gets.
	Max int
	// Wait is how long the pause before it lasts.
	Wait time.Duration
	// Reason is the short form of the failure that forced the retry.
	Reason string
	// Provider names the endpoint being retried, which may not be the one the
	// session started on: the pool has already moved by the time a retry
	// happens on a later member.
	Provider config.Provider
}

// NewFailoverModel builds the pool for a session: the providers the user asked
// for, in order, with the free endpoints behind them as a lazily probed
// reserve.
//
// The wrapper is kept even for a single configured provider, because that is
// precisely the case where the reserve is worth having. Returning the bare
// client instead would make a working Groq key indistinguishable from a session
// with nowhere to go when it starts returning 429s. backups may be nil: a pool
// without a reserve simply reports the first failure instead of hunting for a
// free endpoint mid-turn.
//
// onRetry may be nil; it is called before each retry pause so the UI can say
// what is being waited out.
func NewFailoverModel(ctx context.Context, pool []config.Provider, backups BackupSource, onSwitch func(SwitchEvent), onRetry func(RetryEvent)) (model.LLM, error) {
	members := make([]PoolMember, 0, len(pool))
	for _, p := range pool {
		LLM, err := BuildLLM(ctx, p)
		if err != nil {
			return nil, err
		}
		members = append(members, PoolMember{Prov: p, LLM: LLM})
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("dmcode: empty provider pool")
	}
	return &failoverModel{
		members:  members,
		backups:  backups,
		onSwitch: onSwitch,
		onRetry:  onRetry,
		retry:    RetryPolicyFromEnv(),
	}, nil
}

func (f *failoverModel) Name() string {
	members := f.snapshot()
	if i := f.current(); i < len(members) {
		return members[i].LLM.Name()
	}
	return ""
}

func (f *failoverModel) snapshot() []PoolMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PoolMember(nil), f.members...)
}

func (f *failoverModel) current() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active < len(f.members) {
		return f.active
	}
	return 0
}

func (f *failoverModel) setActive(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= 0 && i < len(f.members) {
		f.active = i
	}
}

// extend runs the backup source once and appends whatever it finds. A probe
// that comes back empty is not a failure worth reporting: the caller is about to
// be told the real error anyway, and a free endpoint that is not there is not a
// problem to apologise for.
func (f *failoverModel) extend(ctx context.Context) error {
	f.mu.Lock()
	if f.backupsTried || f.backups == nil {
		f.mu.Unlock()
		return nil
	}
	f.backupsTried = true
	src := f.backups
	f.mu.Unlock()

	found, err := src(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range found {
		// The same host can already be in the pool: an auto-discovered session
		// starts with what the probe found, and a later re-probe sees it again.
		// Listing it twice would spend a round trip re-failing on a host that
		// has already been ruled out this turn.
		if f.hasLocked(m.Prov) {
			continue
		}
		f.members = append(f.members, m)
	}
	return nil
}

func (f *failoverModel) hasLocked(p config.Provider) bool {
	for _, m := range f.members {
		if m.Prov.BaseURL == p.BaseURL && m.Prov.Model == p.Model && m.Prov.APIKey == p.APIKey {
			return true
		}
	}
	return false
}

func (f *failoverModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if err := f.extend(ctx); err != nil {
			yield(nil, err)
			return
		}
		members := f.snapshot()
		start := f.current()
		// firstErr is reported if nothing succeeds, so the message names the
		// endpoint the user actually configured rather than whichever free
		// fallback happened to be tried last.
		var firstErr error

		for i := start; i < len(members); i++ {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			m := members[i]
			if i != start {
				f.setActive(i)
				if f.onSwitch != nil {
					f.onSwitch(SwitchEvent{
						From:   members[start].Prov,
						To:     m.Prov,
						Reason: shortReason(firstErr),
					})
				}
			}

			var (
				callErr   error
				emitted   bool
				abandoned bool
			)
			// Attempts are spent on this member before the pool moves on. The
			// retryable cases are narrow, and all of them are about the endpoint
			// rather than the request: a rate limit, a gateway hiccup, a socket
			// that dropped before the server saw anything. A rejected request is
			// rejected by every host, so repeating it here would only spend the
			// user's time arriving at the same answer.
			attempts := max(f.retry.MaxAttempts, 1)
			for attempt := 1; ; attempt++ {
				// Reset per attempt: callErr belongs to the attempt that is
				// running, and a success after two failures must not be judged by
				// the failure that came before it.
				callErr = nil
				for resp, err := range m.LLM.GenerateContent(ctx, req, stream) {
					if err != nil {
						callErr = err
						break
					}
					if resp == nil {
						continue
					}
					emitted = true
					if !yield(resp, nil) {
						// The consumer stopped taking events — Esc, or a quit. The
						// turn is over for reasons that have nothing to do with
						// this endpoint, so there is nothing to reroute.
						abandoned = true
						break
					}
				}
				if abandoned || callErr == nil {
					break
				}
				// Once anything has been yielded the user is looking at a partial
				// answer. Re-running the request would print a second one, and the
				// transcript has no way to retract the first.
				if emitted || attempt >= attempts || !sameEndpointRetryable(callErr) {
					break
				}
				wait := f.retry.delay(attempt)
				if f.onRetry != nil {
					f.onRetry(RetryEvent{
						Attempt:  attempt + 1,
						Max:      attempts,
						Wait:     wait,
						Reason:   shortReason(callErr),
						Provider: m.Prov,
					})
				}
				if err := sleep(ctx, wait); err != nil {
					// Esc during the backoff: the turn is over by the user's own
					// hand, so that is the error reported, not the one that
					// started the wait.
					yield(nil, err)
					return
				}
			}
			if abandoned {
				return
			}
			if callErr == nil {
				// A clean finish is a success even if the stream was truncated
				// without a TurnComplete frame: the client closed the turn, and
				// inventing a second attempt here would replace a short answer
				// with a possibly different one.
				return
			}
			if firstErr == nil {
				firstErr = callErr
			}
			if emitted || !retryable(callErr) {
				break
			}
		}

		if firstErr == nil {
			return
		}
		// The pool is spent. Rewind so the next call walks it in order again:
		// a member that was down a minute ago may be back, and pinning the
		// session to whichever one died last would strand it there.
		f.setActive(0)
		yield(nil, firstErr)
	}
}

// retryable reports whether the failure is worth passing to another endpoint.
// A client that does not speak providerError — ADK's own responses-wire client,
// for one — is still describing a single endpoint, so it gets a second chance
// like any other.
func retryable(err error) bool {
	var pe *providerError
	if errors.As(err, &pe) {
		return pe.retryable()
	}
	return true
}

// sameEndpointRetryable reports whether asking the *same* endpoint again could
// plausibly work.
//
// This is a different question from retryable(), which asks whether another
// host in the pool stands a chance — and answers yes for a 401, because a
// different endpoint may hold a different key. Repeating a rejected request on
// the host that just rejected it has no such escape: the answer will be the
// same answer, one round trip later.
//
// What is left is the failures that are about the moment rather than the
// request: a rate limit that will lift, a gateway hiccup, a socket that
// dropped. An unrecognised error is treated as transient, matching the
// conservative default the pool already uses.
func sameEndpointRetryable(err error) bool {
	var pe *providerError
	if errors.As(err, &pe) && pe.reason == failHTTP {
		switch pe.status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusNotFound, http.StatusRequestEntityTooLarge,
			http.StatusUnprocessableEntity:
			return false
		}
	}
	return true
}

// shortReason renders a failure in a few characters for the transcript: the
// status when the host answered, and the transport complaint when it did not.
func shortReason(err error) string {
	var pe *providerError
	if errors.As(err, &pe) {
		if pe.status != 0 {
			return fmt.Sprintf("HTTP %d", pe.status)
		}
		switch pe.reason {
		case failTransport:
			return i18n.T("unavailable")
		case failStream:
			return i18n.T("stream broke")
		}
	}
	if err == nil {
		return i18n.T("failed")
	}
	return i18n.T("failed")
}
