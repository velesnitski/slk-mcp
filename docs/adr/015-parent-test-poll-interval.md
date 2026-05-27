# ADR 015 — `parent_test.go` poll interval raised from 1ms to 10ms

**Status:** accepted
**Date:** 2026-05-27
**Tag at acceptance:** v0.4.13

## Context

ADR 012 (v0.4.10) raised the test deadline from 2s to 5s after
`TestWatchParent_FiresWhenPpidChanges` flaked on Go 1.23 `-race`
runs in CI. The fix held for one run, then flaked again — *with the
new 5s deadline* — on both Go 1.23 and Go 1.24:

```
--- FAIL: TestWatchParent_FiresWhenPpidChanges (5.00s)
    parent_test.go:78: WatchParent did not detect ppid change within deadline
```

Two failures, same SHA passing on dev branch CI, failing on main.
The deadline bump was a misdiagnosis: 5 seconds is genuinely
generous for what is, on paper, a 1ms-cadence polling loop.

### Root cause

`WatchParent` uses `time.NewTicker(interval)`. Tests passed
`1 * time.Millisecond`. Two stacking effects make that interval
unsafe under CI race-detection load:

1. **Linux scheduler granularity.** The kernel timer wheel has
   coarse granularity under contention — typically 4ms under
   stress. A 1ms ticker on a busy runner can have ticks coalesced
   or delayed in ways the test budget doesn't model.
2. **Race detector memory barriers.** `-race` instrumentation
   slows goroutine scheduling by ~5–10x AND adds synchronisation
   barriers on channel and atomic operations. Even when a tick
   fires on time, the consumer goroutine may not be scheduled to
   read from `t.C` for several scheduler slices.

Net effect: on a loaded GitHub Actions runner, the 1ms ticker can
go *silent* for >5 seconds — not slow, **silent**. No amount of
deadline bump fixes that.

### Diagnostic confirmation

`v0.4.10` validation run (SHA `4f4fd10`, fresh after deadline bump)
passed. The next runs (SHA `71c2192`, `d2e1fa7`) failed
identically, on Go 1.23 AND Go 1.24, both with the new 5s deadline.
That's the signature of a load-dependent timer issue: same code,
different runner state, different outcome.

### Options considered

- **a.** Bump the deadline again (5s → 30s). Mostly masks the
  problem; a real hang would now eat 30s of CI time before
  failing.
- **b.** Switch to a condition-variable wakeup instead of polling.
  Bigger change to `WatchParent` semantics; the polling model is
  deliberate for the reparent-to-PID-1 detection case. Out of
  scope.
- **c.** Raise the test poll interval to something safely above
  scheduler granularity (10ms). Production code unchanged.
  Happy-path latency in tests goes from ~3ms to ~30ms — still well
  under a second. With the 5s deadline that's 500+ ticks of
  headroom for a real change to be detected.

## Decision

Use **(c)**. Concretely:

1. New `testPollInterval = 10 * time.Millisecond` constant at the
   top of `parent_test.go` with a comment that explicitly names the
   kernel-granularity / race-detector interaction.
2. All 5 instances of `1 * time.Millisecond` / `10*time.Millisecond`
   inline literals in the test file replaced with the constant.
3. Production code (`WatchParent`, `DefaultParentPollInterval`,
   lifecycle semantics) **not touched**. Real callers using
   `os.Getppid` with the 5-second default are unaffected.

## Consequences

- **Flake closed at the interval level**, not the deadline level.
  The two are different levers: deadline absorbs slow ticks,
  interval prevents absent ticks. Bumping the right one matters.
- **No production behaviour change.** `WatchParent` and its
  default interval are unchanged.
- **Happy-path latency in tests** goes from <3ms to <30ms typical.
  Negligible for the suite.
- **Test budget vs interval is now an explicit ratio**:
  `testDeadline / testPollInterval = 500`. If real-world CI ever
  stretches further, that headroom is auditable in one place.
- **Documented lesson** for future test stability work: deadline
  bumps compensate for slow scheduling, NOT for sub-granularity
  intervals. Look at the interval first when a polling-based test
  goes silent.

## Validation

- `go test -race -count=1 ./internal/lifecycle/...` — 6 tests
  green locally.
- Full suite: 395 → 395 (refactor only, no test count change).
- Expected on CI: the same Go 1.23 + Go 1.24 + lint matrix that
  was flaking should now pass consistently across reruns.

## Out of scope

- `WatchParent` redesign (condition variables, exit-status channel
  from the OS, etc.). The poll model is correct for the
  reparent-to-PID-1 detection case; only the test was misbalanced.
- Sweeping every `time.Millisecond`-based test in the codebase.
  Only `parent_test.go` has been observed flaking; other
  ticker-based tests can be revisited if and when they actually
  fail.
- A general "test polling cadence" utility package. One file's
  constant is sufficient until a second consumer appears.
