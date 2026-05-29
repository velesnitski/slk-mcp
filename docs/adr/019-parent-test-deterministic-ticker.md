# ADR 019 — Deterministic ticker for `parent_test.go` (and the real bug it exposed)

**Status:** accepted (supersedes the *approach* of ADR 012 and ADR 015)
**Date:** 2026-05-29
**Tag at acceptance:** v0.4.17

## Context

`TestWatchParent_*` has flaked three times now:

- **ADR 012 (v0.4.10):** raised the test deadline 2s → 5s.
- **ADR 015 (v0.4.13):** raised the poll interval 1ms → 10ms.
- **This time (v0.4.16 CI):** `TestWatchParent_NilLoggerSafe` failed
  on the Go 1.24 `-race` job, hit the 5s deadline. Same SHA passed
  on the `main` run — load-dependent, the classic flake signature.

Both prior fixes tuned a real-time constant. Both lowered the flake
probability without removing it, because the test asserted a
real-time deadline against a real `time.NewTicker` under the race
detector on a shared, sometimes-saturated CI runner. No fixed
deadline survives a runner that stalls a goroutine for seconds.

### Two bugs, not one

Replacing the real ticker with a deterministic one (below) made the
suite pass — then immediately surfaced a **second, latent bug** that
the timing noise had been masking. Hammering `go test -race
-count=50` still failed ~1 in 50, now instantly rather than after a
5s wait. The cause was in the test harness itself, not the ticker:

```go
// pidSource.get() — BEFORE
func (p *pidSource) get() int {
    p.once.Do(func() { close(p.initialised) }) // signals "initial taken"
    return int(p.value.Load())                  // ...but reads AFTER signalling
}
```

`waitInitialised` unblocks the test the moment `initialised` closes.
The test then calls `value.Store(newPid)`. If that store landed
between the `close()` and the `value.Load()`, the watcher's
"initial" snapshot captured the **new** pid. With initial == current,
the change is never detected and `onLost` never fires → deadline →
fail.

This race existed in the original test all along. The real-ticker
timing jitter both *caused* its own flakes and *hid* this one;
ADRs 012/015 attributed everything to the ticker. The deterministic
ticker is what made the second bug reproducible and obvious.

## Decision

Two changes.

### 1. Inject the tick source (production seam)

`parent.go` gains an unexported `tickerFunc` seam:

```go
type tickerFunc func(time.Duration) (ticks <-chan time.Time, stop func())

func newRealTicker(d time.Duration) (<-chan time.Time, func()) {
    t := time.NewTicker(d)
    return t.C, t.Stop
}

func WatchParent(ctx, log, getPpid, interval, onLost) {        // public API unchanged
    watchParent(ctx, log, getPpid, interval, onLost, newRealTicker)
}

func watchParent(ctx, log, getPpid, interval, onLost, newTicker tickerFunc) { ... }
```

Tests pass a `fakeTicker` whose `tick()` sends on an **unbuffered**
channel — so it blocks until the watcher consumes the tick. When
`tick()` returns, exactly one poll cycle has been processed. No
wall-clock guessing about whether a tick landed. `testDeadline`
remains only as a backstop for "watcher is wedged", never as a
cadence budget.

Bonus: the fake records the interval the watcher requested, so
`TestWatchParent_ZeroIntervalUsesDefault` now *asserts*
`DefaultParentPollInterval` instead of only checking "doesn't
busy-spin", and a new `TestWatchParent_StopsTickerOnReturn` verifies
the `stop()` cleanup that the real `time.Ticker.Stop()` defer always
implied but could never observe.

### 2. Fix the initialisation race (test harness)

`pidSource.get()` now snapshots the value **before** signalling
initialisation:

```go
func (p *pidSource) get() int {
    v := int(p.value.Load())                    // snapshot first
    p.once.Do(func() { close(p.initialised) })  // then signal
    return v
}
```

Now the happens-before chain is: initial `Load` → `close` →
`waitInitialised` returns → test `Store`. The initial snapshot is
guaranteed to observe the pre-flip pid.

## Consequences

- **Flake class eliminated, not reduced.** Verified with
  `go test -race -count=200 ./internal/lifecycle/...` (1400 test
  executions, zero failures) plus three full-suite `-race` runs.
- **Production code unchanged in behaviour.** `WatchParent`'s public
  signature, semantics, and the `DefaultParentPollInterval` default
  are identical. Only an internal seam was added; `main.go` is
  untouched.
- **Stronger tests.** Two assertions that were previously impossible
  (the resolved default interval; ticker stop-on-return) are now
  covered.
- **ADRs 012 and 015 are superseded in approach, not reverted.**
  Their constants (`testDeadline = 5s`, `testPollInterval = 10ms`)
  remain in the file but no longer gate correctness — the comments
  now say so. They are kept as a generous backstop and as the
  nominal interval value, respectively.
- **Lesson, recorded for the next polling test:** when a time-based
  test flakes, prefer injecting the clock/ticker over tuning the
  deadline. A deterministic driver both fixes the flake and tends to
  surface the *real* bug that the timing noise was hiding.

## Validation

- `go vet ./...` — clean.
- `go test -race -count=200 ./internal/lifecycle/...` — 1400/1400.
- `go test -race -count=1 ./...` ×3 — green each time.
- Full suite size unchanged at 421 + 1 new lifecycle test = the
  lifecycle package goes 6 → 7 tests.

## Out of scope

- Migrating other time-based tests in the codebase to injected
  clocks. Only `parent_test.go` has ever flaked; convert others
  if and when they actually do.
- A shared clock-abstraction package. One unexported `tickerFunc`
  in one file is sufficient until a second consumer appears.
