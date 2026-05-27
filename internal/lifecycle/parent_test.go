package lifecycle

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testDeadline is the budget given to async test conditions (goroutine
// startup, channel close, watcher tick). The race detector adds ~5–10x
// scheduling overhead on shared CI runners; a 2-second budget flaked
// intermittently on Go 1.23 -race runs while the same SHA passed on a
// faster runner. 5 seconds is a generous-but-bounded ceiling that
// still fails loudly on a real hang.
const testDeadline = 5 * time.Second

// testPollInterval is the watcher's poll cadence inside this file's
// tests. We deliberately do NOT use 1ms here — it's below the Linux
// kernel scheduler's nominal granularity (~4ms under stress) and
// gets further amplified by the race detector's goroutine-scheduling
// barriers. A v0.4.10 deadline bump (2s → 5s) was insufficient because
// the *interval* was the bottleneck, not the deadline: ticks on a
// loaded CI runner were sometimes not firing at all within the budget.
//
// 10ms is small enough to keep the happy path under a second
// (waitInitialised + 1 tick + onLost ≈ 30ms typical) while staying
// safely above scheduler granularity. With testDeadline = 5s, that's
// 500+ ticks of headroom for a real change to be detected.
const testPollInterval = 10 * time.Millisecond

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// pidSource emulates a parent process whose PID can be flipped. The
// initialised channel makes test setup deterministic: it closes the
// first time getPpid is called by the watcher, so callers know the
// "initial" snapshot has been taken before they flip the value.
type pidSource struct {
	value       atomic.Int64
	initialised chan struct{}
	once        sync.Once
}

func newPidSource(initial int64) *pidSource {
	p := &pidSource{initialised: make(chan struct{})}
	p.value.Store(initial)
	return p
}

func (p *pidSource) get() int {
	p.once.Do(func() { close(p.initialised) })
	return int(p.value.Load())
}

func (p *pidSource) waitInitialised(t *testing.T) {
	t.Helper()
	select {
	case <-p.initialised:
	case <-time.After(testDeadline):
		t.Fatalf("watcher never read initial ppid")
	}
}

func TestWatchParent_FiresWhenPpidChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newPidSource(12345)
	lost := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		WatchParent(ctx, quietLogger(), src.get, testPollInterval,
			func() { close(lost) },
		)
	}()

	src.waitInitialised(t)
	// Flip the parent — emulates the parent dying and us being
	// reparented to PID 1 / launchd.
	src.value.Store(1)

	select {
	case <-lost:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not detect ppid change within deadline")
	}

	// onLost must also stop the watcher loop.
	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not return after onLost")
	}
}

func TestWatchParent_NoFireWhenPpidStable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var lostCount atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchParent(ctx, quietLogger(),
			func() int { return 999 },
			testPollInterval,
			func() { lostCount.Add(1) },
		)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not return after context cancel")
	}

	if got := lostCount.Load(); got != 0 {
		t.Fatalf("onLost called %d times when ppid was stable; want 0", got)
	}
}

func TestWatchParent_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchParent(ctx, quietLogger(),
			func() int { return 1 },
			testPollInterval,
			func() { t.Errorf("onLost must not fire when ppid is stable") },
		)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not exit on ctx.Done()")
	}
}

func TestWatchParent_FiresExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newPidSource(100)

	var calls atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchParent(ctx, quietLogger(), src.get, testPollInterval,
			func() { calls.Add(1) },
		)
	}()

	src.waitInitialised(t)
	src.value.Store(1)

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not return after onLost")
	}

	// Even if multiple ticks elapsed before the goroutine returned,
	// onLost should have fired exactly once.
	if got := calls.Load(); got != 1 {
		t.Fatalf("onLost call count = %d; want 1", got)
	}
}

func TestWatchParent_ZeroIntervalUsesDefault(t *testing.T) {
	// Cannot directly observe DefaultParentPollInterval inside a black-box
	// caller, but we can verify the watcher does not panic or busy-spin
	// when interval <= 0 is passed: cancel quickly and confirm the
	// goroutine exits without firing onLost.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchParent(ctx, nil,
			func() int { return 1 },
			0,
			func() { t.Errorf("onLost must not fire when ppid is stable") },
		)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("watcher with interval=0 did not exit on ctx.Done()")
	}
}

func TestWatchParent_NilLoggerSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newPidSource(10)
	lost := make(chan struct{})
	go WatchParent(ctx, nil, src.get, testPollInterval,
		func() { close(lost) },
	)

	src.waitInitialised(t)
	src.value.Store(1)

	select {
	case <-lost:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent with nil logger did not fire onLost")
	}
}
