package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"

	"google.golang.org/adk/v2/model"

	"dmcode/internal/config"
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
func NewFailoverModel(ctx context.Context, pool []config.Provider, backups BackupSource, onSwitch func(SwitchEvent)) (model.LLM, error) {
	members := make([]PoolMember, 0, len(pool))
	for _, p := range pool {
		LLM, err := BuildLLM(ctx, p)
		if err != nil {
			return nil, err
		}
		members = append(members, PoolMember{Prov: p, LLM: LLM})
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("dmcode: пустой пул провайдеров")
	}
	return &failoverModel{members: members, backups: backups, onSwitch: onSwitch}, nil
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
					// turn is over for reasons that have nothing to do with this
					// endpoint, so there is nothing to reroute.
					abandoned = true
					break
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
			return "недоступен"
		case failStream:
			return "обрыв потока"
		}
	}
	if err == nil {
		return "ошибка"
	}
	return "ошибка"
}
