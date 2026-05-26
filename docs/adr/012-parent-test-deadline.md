# ADR 012 — `parent_test.go` deadline raised to 5s under `-race`

**Status:** accepted
**Date:** 2026-05-26
**Tag at acceptance:** v0.4.10

## Context

Day 34 CI showed a flake: `TestWatchParent_FiresWhenPpidChanges`
failed on the `main` branch run while the identical SHA passed on
`dev` minutes earlier. Same code, different result → schedule
artefact, not a regression.

Looking at the failure log:

```
--- FAIL: TestWatchParent_FiresWhenPpidChanges (2.00s)
    parent_test.go:70: WatchParent did not detect ppid change within 2s
```

The watcher polls at `1 * time.Millisecond` and the test waited
exactly `2 * time.Second` for the `lost` channel to close. Under
the race detector, goroutine scheduling overhead is famously
5–10x; on a busy GitHub Actions runner that budget can collapse.

Other tests in the same file used the same `2 * time.Second`
literal in 7 more places. Any of them could flake the same way.

### Options considered

- **a.** Make the watcher's first poll happen immediately instead
  of after one tick. Real fix at the source, but touches production
  code in `WatchParent` for a test-stability symptom. Risk of
  introducing a subtle behaviour change in a code path that gates
  the entire MCP lifecycle.
- **b.** Add a retry loop around the failing assertion. Adds
  control-flow noise; doesn't address the root cause (test budget
  is too tight).
- **c.** Raise the deadline. The watcher does poll every 1ms — a
  successful run completes in milliseconds. 5 seconds is still
  loud-on-real-hang; the only thing we lose is the false-positive
  rate.

## Decision

Use **(c)**. Concretely:

1. Add `const testDeadline = 5 * time.Second` at the top of
   `parent_test.go` with a comment explaining the race-detector
   overhead rationale.
2. Replace all 7 `time.After(2 * time.Second)` and 1
   `time.After(time.Second)` occurrences with
   `time.After(testDeadline)`.
3. Update the one stale failure message that named "2s" so a
   future reader doesn't read a contradiction between the literal
   constant and the error text.

Production code untouched.

## Consequences

- **Flake eliminated.** A normal run still completes in <10ms;
  the deadline only fires on a real hang.
- **Real hangs still fail loudly.** 5s is generous but bounded —
  if `WatchParent` deadlocks, the test fails within 5s rather than
  hanging forever and stalling the whole suite.
- **One constant, one source of truth.** Future contributors who
  want a different ceiling change it in one place.
- **No production code change.** `WatchParent`, the watcher
  interval, and the lifecycle semantics are unchanged. The fix is
  test-tolerance only.
- **Pattern reusable.** Other timing-based tests across the
  codebase that hardcode `2 * time.Second` against the race
  detector should mirror this pattern. No immediate sweep — only
  do it when a test actually flakes.

## Validation

- `go test -race -count=1 ./internal/lifecycle/...` — 6 tests
  green locally.
- Full suite: 381 → 381 (no test count change; refactor only).
- Same SHA was confirmed green on `dev` CI before this patch, so
  the rerun-loop expectation is that v0.4.10 main + dev both pass
  Go 1.23 + 1.24 + lint consistently.

## Out of scope

- Sweeping every test file for hardcoded `2 * time.Second` budgets.
  Premature — only fix what's observed to flake.
- Switching from polling to a condition variable in `WatchParent`.
  Possible, but the polling model is intentional (handles the
  reparent-to-PID-1 case cleanly); a redesign is a bigger
  conversation.
- Adding a generic `internal/testutil` package for shared
  deadlines. One-file constant is enough until a second consumer
  appears.
