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
	case <-time.After(2 * time.Second):
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
		WatchParent(ctx, quietLogger(), src.get, 1*time.Millisecond,
			func() { close(lost) },
		)
	}()

	src.waitInitialised(t)
	// Flip the parent — emulates the parent dying and us being
	// reparented to PID 1 / launchd.
	src.value.Store(1)

	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchParent did not detect ppid change within 2s")
	}

	// onLost must also stop the watcher loop.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
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
			1*time.Millisecond,
			func() { lostCount.Add(1) },
		)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
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
			10*time.Millisecond,
			func() { t.Errorf("onLost must not fire when ppid is stable") },
		)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
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
		WatchParent(ctx, quietLogger(), src.get, 1*time.Millisecond,
			func() { calls.Add(1) },
		)
	}()

	src.waitInitialised(t)
	src.value.Store(1)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
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
	case <-time.After(time.Second):
		t.Fatalf("watcher with interval=0 did not exit on ctx.Done()")
	}
}

func TestWatchParent_NilLoggerSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newPidSource(10)
	lost := make(chan struct{})
	go WatchParent(ctx, nil, src.get, 1*time.Millisecond,
		func() { close(lost) },
	)

	src.waitInitialised(t)
	src.value.Store(1)

	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchParent with nil logger did not fire onLost")
	}
}
