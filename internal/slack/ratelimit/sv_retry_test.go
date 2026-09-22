package ratelimit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// svLog is a logger that throws everything away — Do logs a warning on
// every retry and these tests exercise a lot of retries.
func svLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// svTick is the backoff these tests ask Slack for. Real Slack answers in
// whole seconds; the retry loop simply honours whatever Retry-After the
// error carries, so a sub-millisecond value exercises the same code with
// no wall-clock cost.
const svTick = 500 * time.Microsecond

// svLimited builds the error slack-go surfaces for an HTTP 429.
func svLimited(after time.Duration) error {
	return &slack.RateLimitedError{RetryAfter: after}
}

// svBudget is the ceiling every test in this file stays under: no test
// may sit through a real backoff schedule.
const svBudget = 50 * time.Millisecond

func TestDo_SuccessCallsTheFunctionExactlyOnce(t *testing.T) {
	calls := 0
	err := Do(context.Background(), svLog(), 3, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDo_NonRateLimitErrorIsReturnedWithoutRetrying(t *testing.T) {
	sentinel := errors.New("invalid_auth")
	calls := 0
	err := Do(context.Background(), svLog(), 5, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (a non-429 must not be retried)", calls)
	}
}

func TestDo_RateLimitIsRetriedUntilTheCallSucceeds(t *testing.T) {
	calls := 0
	start := time.Now()
	err := Do(context.Background(), svLog(), 5, func() error {
		calls++
		if calls < 3 {
			return svLimited(svTick)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (two refusals then a success)", calls)
	}
	if d := time.Since(start); d > svBudget {
		t.Fatalf("took %v, want under %v — the test slept through a real schedule", d, svBudget)
	}
}

func TestDo_ExhaustedRetriesSurfaceTheRateLimitErrorNotNil(t *testing.T) {
	calls := 0
	err := Do(context.Background(), svLog(), 2, func() error {
		calls++
		return svLimited(svTick)
	})
	if err == nil {
		t.Fatal("Do returned nil after exhausting retries: a rate-limited call must never look like a success")
	}
	var rle *slack.RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %T (%v), want *slack.RateLimitedError", err, err)
	}
	// maxRetries is the number of RETRIES, so the first attempt plus two.
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (1 attempt + 2 retries)", calls)
	}
}

func TestDo_NonPositiveMaxRetriesFallsBackToTheDefault(t *testing.T) {
	for _, maxRetries := range []int{0, -1} {
		calls := 0
		err := Do(context.Background(), svLog(), maxRetries, func() error {
			calls++
			return svLimited(svTick)
		})
		if err == nil {
			t.Fatalf("maxRetries=%d: Do returned nil", maxRetries)
		}
		if want := DefaultMaxRetries + 1; calls != want {
			t.Fatalf("maxRetries=%d: calls = %d, want %d", maxRetries, calls, want)
		}
	}
}

func TestDo_AlreadyCancelledContextNeverCallsTheFunction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := Do(ctx, svLog(), 3, func() error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestDo_CancellationDuringBackoffAbortsImmediately(t *testing.T) {
	// A zero Retry-After drives the exponential fallback, whose first
	// wait is a full second. Cancelling inside fn means the wait select
	// resolves on ctx.Done() instead, so the branch is covered without
	// the test sleeping.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	start := time.Now()
	err := Do(ctx, svLog(), 5, func() error {
		calls++
		cancel()
		return svLimited(0)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if d := time.Since(start); d > svBudget {
		t.Fatalf("took %v, want under %v — the exponential backoff was actually slept", d, svBudget)
	}
}

func TestDo_NegativeRetryAfterAlsoUsesExponentialBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	err := Do(ctx, svLog(), 5, func() error {
		cancel()
		return svLimited(-3 * time.Second)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > svBudget {
		t.Fatalf("took %v, want under %v", d, svBudget)
	}
}

func TestDo_ContextCancelledBetweenAttemptsStopsTheLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	err := Do(ctx, svLog(), 5, func() error {
		calls++
		if calls == 2 {
			cancel()
		}
		return svLimited(svTick)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestDoR_ReturnsTheValueFromTheCall(t *testing.T) {
	got, err := DoR(context.Background(), svLog(), func() ([]string, error) {
		return []string{"alpha", "beta"}, nil
	})
	if err != nil {
		t.Fatalf("DoR: %v", err)
	}
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("got = %v, want [alpha beta]", got)
	}
}

func TestDoR_ErrorYieldsTheZeroValue(t *testing.T) {
	sentinel := errors.New("channel_not_found")
	got, err := DoR(context.Background(), svLog(), func() (*int, error) {
		n := 7
		_ = n
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
}

func TestDoR_RateLimitedCallIsRetriedNotTurnedIntoAnEmptyResult(t *testing.T) {
	calls := 0
	got, err := DoR(context.Background(), svLog(), func() ([]string, error) {
		calls++
		if calls == 1 {
			// The dangerous shape: a partial/empty payload alongside a
			// 429. DoR must discard it and retry, not hand it back.
			return nil, svLimited(svTick)
		}
		return []string{"C1", "C2", "C3"}, nil
	})
	if err != nil {
		t.Fatalf("DoR: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3 — a retried call must return the full result", len(got))
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestDoR_ExhaustedRateLimitIsAnErrorAndAnEmptyValue(t *testing.T) {
	calls := 0
	got, err := DoR(context.Background(), svLog(), func() ([]string, error) {
		calls++
		return []string{"partial"}, svLimited(svTick)
	})
	if err == nil {
		t.Fatal("DoR returned nil error: a short list must never look like a complete one")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil — a failed call must not leak a partial payload", got)
	}
	if want := DefaultMaxRetries + 1; calls != want {
		t.Fatalf("calls = %d, want %d", calls, want)
	}
}

func TestDoR_CancelledContextReturnsZeroValueAndContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := DoR(ctx, svLog(), func() (string, error) {
		return "should not run", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
}
