package llm

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// scriptedModel fails a given number of times before answering, counting how
// many times it was asked.
type scriptedModel struct {
	failures int // how many of the first calls fail
	calls    int
	// emitBeforeFail makes a failing call yield a response first, which is the
	// case where a retry must not happen: the user is already looking at part
	// of an answer.
	emitBeforeFail bool
	err            error
}

func (s *scriptedModel) Name() string { return "scripted" }

func (s *scriptedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		s.calls++
		// The failure is reported at the end of the stream, exactly as a real
		// client reports an error frame after the frames before it — which is
		// why "something was already emitted" has to be tracked separately from
		// "the call failed".
		if s.calls <= s.failures {
			if s.emitBeforeFail {
				yield(&model.LLMResponse{}, nil)
			}
			yield(nil, s.err)
			return
		}
		yield(&model.LLMResponse{}, nil)
	}
}

// instantPolicy is a retry policy with no waiting, so a test exercises the
// logic rather than the clock.
func instantPolicy(attempts int) RetryPolicy {
	return RetryPolicy{MaxAttempts: attempts, Base: time.Microsecond, Max: time.Microsecond}
}

func testProvider(name string) config.Provider {
	return config.Provider{BaseURL: "https://" + name + ".test", Model: "m", Label: name}
}

func newFailover(t *testing.T, m model.LLM, retry RetryPolicy, onRetry func(RetryEvent)) *failoverModel {
	t.Helper()
	return &failoverModel{
		members: []PoolMember{{Prov: testProvider("first"), LLM: m}},
		retry:   retry,
		onRetry: onRetry,
	}
}

// drain runs a generation and reports how many responses came back and the
// first error, if any.
func drain(seq iter.Seq2[*model.LLMResponse, error]) (n int, err error) {
	for resp, e := range seq {
		if e != nil {
			return n, e
		}
		if resp != nil {
			n++
		}
	}
	return n, nil
}

// TestRetrySucceedsOnALaterAttempt is the reason the retry exists: a rate limit
// is not a verdict, and a session that dies on the first 429 dies for a reason
// the user cannot do anything about.
func TestRetrySucceedsOnALaterAttempt(t *testing.T) {
	m := &scriptedModel{failures: 2, err: errors.New("HTTP 429")}
	f := newFailover(t, m, instantPolicy(3), nil)

	n, err := drain(f.GenerateContent(context.Background(), &model.LLMRequest{}, false))
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}
	if n != 1 {
		t.Errorf("got %d responses, want 1", n)
	}
	if m.calls != 3 {
		t.Errorf("the endpoint was called %d times, want 3", m.calls)
	}
}

// TestRetryStopsAtTheAttemptLimit: a policy that never gave up would turn a
// dead endpoint into an endless turn.
func TestRetryStopsAtTheAttemptLimit(t *testing.T) {
	m := &scriptedModel{failures: 99, err: errors.New("HTTP 503")}
	f := newFailover(t, m, instantPolicy(3), nil)

	if _, err := drain(f.GenerateContent(context.Background(), &model.LLMRequest{}, false)); err == nil {
		t.Fatal("GenerateContent reported success against a dead endpoint")
	}
	if m.calls != 3 {
		t.Errorf("the endpoint was called %d times, want 3", m.calls)
	}
}

// TestNoRetryOnceAnythingWasEmitted: the user is already looking at part of an
// answer, and a second run would print it twice with no way to take the first
// back.
func TestNoRetryOnceAnythingWasEmitted(t *testing.T) {
	m := &scriptedModel{failures: 99, emitBeforeFail: true, err: errors.New("HTTP 500")}
	f := newFailover(t, m, instantPolicy(3), nil)

	drain(f.GenerateContent(context.Background(), &model.LLMRequest{}, false))

	if m.calls != 1 {
		t.Errorf("the endpoint was called %d times, want 1 — nothing may be retried after output", m.calls)
	}
}

// TestNoRetryOnARejectedRequest: a request the provider refused is refused by
// every host, so repeating it only spends the user's time to arrive at the same
// answer.
func TestNoRetryOnARejectedRequest(t *testing.T) {
	m := &scriptedModel{failures: 99, err: &providerError{
		err: errors.New("unauthorized"), status: 401, reason: failHTTP,
	}}
	f := newFailover(t, m, instantPolicy(3), nil)

	drain(f.GenerateContent(context.Background(), &model.LLMRequest{}, false))

	if m.calls != 1 {
		t.Errorf("the endpoint was called %d times, want 1 — a 401 is not transient", m.calls)
	}
}

// TestRetryAnnouncesItself: a silent pause after an error reads as a hang, so
// every retry has to be reported before it happens.
func TestRetryAnnouncesItself(t *testing.T) {
	m := &scriptedModel{failures: 2, err: errors.New("HTTP 429")}
	var events []RetryEvent
	f := newFailover(t, m, instantPolicy(3), func(ev RetryEvent) { events = append(events, ev) })

	drain(f.GenerateContent(context.Background(), &model.LLMRequest{}, false))

	if len(events) != 2 {
		t.Fatalf("got %d retry events, want 2", len(events))
	}
	if events[0].Attempt != 2 || events[0].Max != 3 {
		t.Errorf("first event = %+v, want attempt 2 of 3", events[0])
	}
	if events[0].Reason == "" {
		t.Error("the retry event carries no reason to show the user")
	}
	if events[0].Provider.Label != "first" {
		t.Errorf("the event names %q, want the endpoint being retried", events[0].Provider.Label)
	}
}

// TestNoRetryEventOnASuccessfulCall: the first attempt is not a retry, and
// announcing it would put "attempt 1/3" in front of every single answer.
func TestNoRetryEventOnASuccessfulCall(t *testing.T) {
	m := &scriptedModel{}
	var events []RetryEvent
	f := newFailover(t, m, instantPolicy(3), func(ev RetryEvent) { events = append(events, ev) })

	drain(f.GenerateContent(context.Background(), &model.LLMRequest{}, false))

	if len(events) != 0 {
		t.Errorf("a successful call announced %d retries", len(events))
	}
}

// TestCancellationInterruptsTheBackoff: a user who pressed Esc must not sit
// through the rest of the wait before the turn notices.
func TestCancellationInterruptsTheBackoff(t *testing.T) {
	m := &scriptedModel{failures: 99, err: errors.New("HTTP 429")}
	// A long pause, so a test that does not honour cancellation takes a minute.
	f := &failoverModel{
		members: []PoolMember{{Prov: testProvider("first"), LLM: m}},
		retry:   RetryPolicy{MaxAttempts: 5, Base: time.Minute, Max: time.Minute},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := drain(f.GenerateContent(ctx, &model.LLMRequest{}, false))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the backoff ran for %s after cancellation", elapsed)
	}
	// Zero calls, not one: the pool loop checks the context before reaching an
	// endpoint at all, which is the cheapest way to honour a cancelled turn.
	if m.calls != 0 {
		t.Errorf("the endpoint was called %d times after cancellation, want 0", m.calls)
	}
}

// TestDelayGrowsAndIsCapped: a backoff that does not grow turns a rate limit
// into a hammering, and one that is not capped turns three attempts into a wait
// the user will not sit through.
func TestDelayGrowsAndIsCapped(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 6, Base: 100 * time.Millisecond, Max: 400 * time.Millisecond}
	first := p.delay(1)
	second := p.delay(2)
	late := p.delay(5)

	if second <= first {
		t.Errorf("delay did not grow: %s then %s", first, second)
	}
	// The cap is checked with room for the jitter, which is a quarter of the
	// delay by design.
	if late > p.Max+p.Max/4+time.Millisecond {
		t.Errorf("delay %s escaped the cap %s", late, p.Max)
	}
}

// TestDelayIsJittered: clients that retry in lockstep reproduce exactly the
// burst a rate limit was issued against.
func TestDelayIsJittered(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 3, Base: time.Second, Max: time.Second}
	seen := make(map[time.Duration]bool)
	for range 20 {
		seen[p.delay(1)] = true
	}
	if len(seen) < 2 {
		t.Error("every retry got the same delay, so they would all arrive together")
	}
}

// TestPolicyFromEnvIgnoresRubbish: a typo in an optional tuning knob must not
// stop dmcode from starting.
func TestPolicyFromEnvIgnoresRubbish(t *testing.T) {
	t.Setenv("DMCODE_LLM_RETRIES", "не число")
	t.Setenv("DMCODE_LLM_RETRY_MS", "")

	p := RetryPolicyFromEnv()
	if p.MaxAttempts != defaultRetryAttempts {
		t.Errorf("MaxAttempts = %d, want the default %d", p.MaxAttempts, defaultRetryAttempts)
	}
	if p.Base != defaultRetryBase {
		t.Errorf("Base = %s, want the default %s", p.Base, defaultRetryBase)
	}
}

// TestPolicyFromEnvReadsTheKnobs: the point of the env vars is that a user on a
// flaky free endpoint can make the retries stick.
func TestPolicyFromEnvReadsTheKnobs(t *testing.T) {
	t.Setenv("DMCODE_LLM_RETRIES", "5")
	t.Setenv("DMCODE_LLM_RETRY_MS", "200")

	p := RetryPolicyFromEnv()
	if p.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", p.MaxAttempts)
	}
	if p.Base != 200*time.Millisecond {
		t.Errorf("Base = %s, want 200ms", p.Base)
	}
	if p.Max < p.Base {
		t.Errorf("Max = %s, below the base %s", p.Max, p.Base)
	}
}
