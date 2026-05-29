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

// testDeadline is the safety net for channel waits (goroutine startup,
// onLost firing, watcher return). It is NOT a poll-cadence budget: the
// watcher is driven by a deterministic fakeTicker (see below), so
// onLost fires within microseconds of a manual tick. This deadline
// only ever trips if the watcher is genuinely wedged — in which case
// failing loud after a bounded wait beats hanging until the suite
// timeout.
//
// History: ADR 012 raised this 2s→5s and ADR 015 raised the poll
// interval 1ms→10ms, both trying to stabilise a real-time ticker
// under -race on loaded CI. Both only lowered flake probability.
// ADR 019 replaced the real ticker in tests with a manual one, so
// the deadline no longer gates correctness — it is pure backstop.
const testDeadline = 5 * time.Second

// testPollInterval is the nominal interval passed to the watcher in
// tests that use a non-zero cadence. With the fakeTicker it is never
// used for real timing — only recorded, so the zero-interval default
// can be distinguished from an explicit value. Its concrete duration
// is therefore arbitrary.
const testPollInterval = 10 * time.Millisecond

// fakeTicker is a manually-driven tickerFunc for deterministic tests.
//
//   - tick() sends on an UNBUFFERED channel, so it blocks until the
//     watcher consumes the tick. When tick() returns, the watcher has
//     received (and is about to process) exactly one poll cycle — no
//     wall-clock guessing about whether a tick "landed".
//   - factory records the interval the watcher requested (so the
//     DefaultParentPollInterval fallback is observable) and reports
//     whether stop() was called (resource-cleanup assertion).
type fakeTicker struct {
	ticks     chan time.Time
	intervals chan time.Duration
	stopped   atomic.Bool
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{
		ticks:     make(chan time.Time),
		intervals: make(chan time.Duration, 1),
	}
}

// factory satisfies tickerFunc. intervals is buffered (cap 1) and the
// factory is called once per watcher, so this never blocks even if a
// test doesn't read the interval.
func (f *fakeTicker) factory(d time.Duration) (<-chan time.Time, func()) {
	f.intervals <- d
	return f.ticks, func() { f.stopped.Store(true) }
}

// tick advances the watcher by exactly one poll. Only call it while
// the watcher is alive — an unbuffered send to a returned watcher
// would block forever (surfaced as a suite timeout, never a silent
// pass).
func (f *fakeTicker) tick() { f.ticks <- time.Time{} }

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
	// Snapshot the value BEFORE signalling initialisation. Order
	// matters: waitInitialised unblocks the test (which then stores a
	// new value) only after this read has happened, so the watcher's
	// "initial" snapshot is guaranteed to observe the pre-flip value.
	// Closing the channel first would let value.Store race the initial
	// Load — if the store won, `initial` would capture the *new* pid,
	// the change would never be detected, and onLost would never fire.
	// That latent race was masked by real-ticker timing noise until
	// ADR 019's deterministic ticker exposed it (~1 in 50 -race runs).
	v := int(p.value.Load())
	p.once.Do(func() { close(p.initialised) })
	return v
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
	ft := newFakeTicker()
	lost := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		watchParent(ctx, quietLogger(), src.get, testPollInterval,
			func() { close(lost) }, ft.factory)
	}()

	src.waitInitialised(t)
	// Flip the parent — emulates the parent dying and us being
	// reparented to PID 1 / launchd.
	src.value.Store(1)
	// One deterministic poll: the watcher receives this tick, reads
	// the changed ppid, fires onLost, and returns.
	ft.tick()

	select {
	case <-lost:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not detect ppid change")
	}

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
	ft := newFakeTicker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchParent(ctx, quietLogger(),
			func() int { return 999 },
			testPollInterval,
			func() { lostCount.Add(1) }, ft.factory)
	}()

	// Two stable polls: ppid never changes, so onLost must not fire.
	ft.tick()
	ft.tick()
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

	ft := newFakeTicker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchParent(ctx, quietLogger(),
			func() int { return 1 },
			testPollInterval,
			func() { t.Errorf("onLost must not fire when ppid is stable") },
			ft.factory)
	}()

	// No ticks: cancel alone must unblock the select and return.
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
	ft := newFakeTicker()

	var calls atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchParent(ctx, quietLogger(), src.get, testPollInterval,
			func() { calls.Add(1) }, ft.factory)
	}()

	src.waitInitialised(t)
	src.value.Store(1)
	// A single detecting tick fires onLost and returns; the post-fire
	// `return` in watchParent structurally guarantees no second fire.
	ft.tick()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent did not return after onLost")
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("onLost call count = %d; want 1", got)
	}
}

func TestWatchParent_ZeroIntervalUsesDefault(t *testing.T) {
	// With the ticker injected, the resolved interval is now directly
	// observable: a zero interval must be replaced by
	// DefaultParentPollInterval before the ticker is created. The old
	// black-box version could only assert "doesn't panic/busy-spin";
	// this asserts the actual default.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ft := newFakeTicker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchParent(ctx, nil,
			func() int { return 1 },
			0, // <= 0 must resolve to DefaultParentPollInterval
			func() { t.Errorf("onLost must not fire when ppid is stable") },
			ft.factory)
	}()

	select {
	case got := <-ft.intervals:
		if got != DefaultParentPollInterval {
			t.Fatalf("interval=0 resolved to %v; want %v", got, DefaultParentPollInterval)
		}
	case <-time.After(testDeadline):
		t.Fatalf("watcher never requested a ticker")
	}

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
	ft := newFakeTicker()
	lost := make(chan struct{})
	go watchParent(ctx, nil, src.get, testPollInterval,
		func() { close(lost) }, ft.factory)

	src.waitInitialised(t)
	src.value.Store(1)
	ft.tick()

	select {
	case <-lost:
	case <-time.After(testDeadline):
		t.Fatalf("WatchParent with nil logger did not fire onLost")
	}
}

func TestWatchParent_StopsTickerOnReturn(t *testing.T) {
	// The ticker's stop() must run on every exit path. Now that the
	// ticker is injected, we can assert the cleanup that the real
	// time.Ticker.Stop() defer was always supposed to perform.
	ctx, cancel := context.WithCancel(context.Background())

	ft := newFakeTicker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchParent(ctx, nil,
			func() int { return 1 },
			testPollInterval,
			func() {}, ft.factory)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatalf("watcher did not return on ctx cancel")
	}

	if !ft.stopped.Load() {
		t.Fatalf("ticker stop() was not called on return")
	}
}
