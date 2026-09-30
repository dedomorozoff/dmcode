package llm

import (
	"context"
	"math/rand"
	"os"
	"strconv"
	"time"
)

// Retry defaults. Three attempts is enough to ride out a rate limit or a
// dropped connection without turning a failure into a minute of waiting, and
// the ceiling keeps the last wait short enough that a user watching a spinner
// does not give up before the answer arrives.
const (
	defaultRetryAttempts = 3
	defaultRetryBase     = 500 * time.Millisecond
	defaultRetryMax      = 8 * time.Second
)

// RetryPolicy is how hard a single endpoint is tried before the failure is
// passed to the next one in the pool.
//
// The two fields are a count and a backoff rather than a single timeout because
// the two failures are different problems. A 429 needs distance: retrying it
// immediately is the same request arriving while the window is still closed. A
// dropped connection needs almost none: the server never saw the request, so
// the next attempt is free. A single number cannot serve both.
type RetryPolicy struct {
	// MaxAttempts is the total number of tries against one endpoint, so 1
	// means no retry at all.
	MaxAttempts int
	// Base is the first pause; it doubles each further attempt.
	Base time.Duration
	// Max caps the pause, so a long chain of failures cannot add up to a wait
	// longer than the user is willing to sit through.
	Max time.Duration
}

// RetryPolicyFromEnv reads the policy from the environment, falling back to the
// defaults for anything unset or unparseable.
//
// A malformed value is ignored rather than reported: a typo in an optional
// tuning knob should not stop dmcode from starting, and the default is a
// perfectly good policy.
func RetryPolicyFromEnv() RetryPolicy {
	p := RetryPolicy{
		MaxAttempts: defaultRetryAttempts,
		Base:        defaultRetryBase,
		Max:         defaultRetryMax,
	}
	if v, err := strconv.Atoi(os.Getenv("DMCODE_LLM_RETRIES")); err == nil && v > 0 {
		p.MaxAttempts = v
	}
	if v, err := strconv.Atoi(os.Getenv("DMCODE_LLM_RETRY_MS")); err == nil && v > 0 {
		p.Base = time.Duration(v) * time.Millisecond
		// The cap follows the base unless it was set on its own, so a user who
		// asks for a long first pause does not end up capped below it.
		p.Max = 8 * p.Base
	}
	if p.Max < p.Base {
		p.Max = p.Base
	}
	return p
}

// delay is how long to wait before attempt n, counting from 1: the first retry
// waits one base period, the next two, and so on up to the cap.
//
// The jitter is not decoration. Without it, every client that hit the same
// rate limit retries at the same instant, and the endpoint sees exactly the
// burst it was already overwhelmed by. A quarter of the period is enough to
// spread them out without making the wait feel random to the user.
func (p RetryPolicy) delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := p.Base
	for range attempt - 1 {
		d *= 2
		if d >= p.Max {
			break
		}
	}
	if d > p.Max {
		d = p.Max
	}
	jitter := time.Duration(rand.Int63n(int64(d)/4 + 1))
	return d + jitter
}

// sleep waits for d, or returns early if the context is cancelled.
//
// The cancellation is the point: a user who pressed Esc must not sit through
// the remaining backoff before the turn notices. Returning ctx.Err() rather
// than a nil error is what lets the caller tell a deliberate stop from a wait
// that ran out.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
