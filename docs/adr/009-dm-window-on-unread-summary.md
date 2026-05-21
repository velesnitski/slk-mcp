# ADR 009 — DM time-window override on `get_unread_summary`

**Status:** accepted
**Date:** 2026-05-21
**Tag at acceptance:** v0.4.7

## Context

`get_unread_summary` is the daily-recap entry point. The unread
signal works well for channel activity the operator hasn't seen
yet, but breaks for conversations the operator is themselves *in*:

- 1:1 DM threads where decisions are made
- Multi-party DMs used as exec sync
- Side-chat handoffs that get read by the operator within minutes
  of arrival

In all of these, the operator's `last_read` advances continuously
because they're an active participant. By end of day, nothing in
those threads is "unread" — yet the day's most consequential
exchanges may live exactly there.

A separate consumer that ran a parallel daily-digest tool surfaced
DMs in its output because it scanned recent activity per channel
regardless of read state. `get_unread_summary` did not, and the
gap was concrete enough to be reported as a missed item in a Day
29 status review.

### Options considered

- **a.** Add `include_read_dms: bool`. Simple but unbounded — DMs
  with thousands of historical messages would be fetched whole.
- **b.** Add a separate `get_dm_activity` tool. Cleanest scope but
  doubles the surface area for what's really one merged daily view.
- **c.** Add `dm_window_hours: int` to `get_unread_summary`. When
  `> 0`, also fetch DM history newer than `now − hours` and merge
  into the result. Bounded by the time window, single tool stays
  the entry point, default-zero is non-breaking.

## Decision

Use **(c)**. The contract change:

1. New optional `dm_window_hours: int` parameter (default `0`).
2. When `> 0`, the handler calls a new
   `UnreadService.RecentDMActivity(ctx, hours, maxPerChannel)`:
   - lists joined channels (`JoinedChannels` already includes `im`
     + `mpim`);
   - filters to `IsIM || IsMpIM` in the worker pool;
   - pulls `conversations.history` for each DM with
     `oldest = now − hours`;
   - reuses `fetchReplies` so thread-reply shape matches `UnreadAll`.
3. The handler merges the override with `UnreadAll`'s result via
   a new `mergeDMOverride(base, override)` helper:
   - DM/MPIM entries in `base` are replaced by their override
     counterparts (fresher / fuller content);
   - non-DM entries pass through untouched;
   - DMs that weren't in `base` (because they were already read)
     get appended.
4. Time source is a package-level `nowUnixFn` so tests can pin the
   cutoff deterministically — preferable to plumbing a `time.Time`
   through the service interface for one method.

`RecentDMActivity` returns `nil, nil` when `hours <= 0` — callers
can guard with a simple `if hours > 0` and never have to construct
empty fallback slices.

## Consequences

- **Bounded.** Time-window scope keeps the worst case at "history
  since N hours ago," not "everything ever in this DM." Caller
  chooses `hours` based on appetite (24 typical, 4 for a focused
  late-day sweep).
- **Non-breaking.** Default `dm_window_hours=0` preserves the
  pre-existing unread-only contract for every existing consumer.
- **API cost.** One `users.conversations` (already cached for the
  duration of the call) + N `conversations.history` calls where N
  ≤ count of DMs/MPIMs. Bounded by what the operator's Slack
  sidebar can hold; not workspace-wide.
- **Downstream.** Existing urgency ranker, max-chars budget, and
  log/git detectors all run unchanged — the merge just feeds the
  same shape into them. DM channels rank low on urgency by default
  (no `@here` keywords), so they naturally sort behind real
  channels in the rendered output.
- **`UnreadClient` contract grows by one method.** Same pattern as
  v0.4.3 (broadened `UserClient.Name`). The compile-time
  assertion in `contracts.go` continues to enforce that
  `*slack.UnreadService` satisfies the broader interface.
- **Time-source seam.** `nowUnixFn` is a package-level `var`, not a
  field. This is a deliberate trade — tests can override it
  without an interface, the production path stays free of
  injection ceremony. Concurrent test runs could race on the seam;
  the existing tests use `t.Cleanup` to restore and don't run in
  parallel.

## Validation

- `TestRecentDMActivity_filtersToDMsOnly` — listing returns mixed
  public + DM + MPDM channels; the fetch path must only touch DMs.
- `TestRecentDMActivity_noopOnZeroHours` /
  `…_noopOnNegativeHours` — pin the cheap-no-op contract.
- `TestRecentDMActivity_requiresUserToken` — token-gate fires
  before any listing call.
- `TestMergeDMOverride_*` (5 cases) — empty override, DM
  replacement, non-DM preservation, new-DM append, nil-safety,
  MPDM treatment.
- Full suite: 351 → 361 green (+10).

## Out of scope

- Cross-window dedup. If a message lands within `hours` AND was
  unread at the time of the sweep, both paths might surface it;
  the merge prefers the DM-window version (fresher / fuller),
  which is the better answer either way.
- Indefinite history. The deliberately bounded `hours` parameter
  is the answer to "how much do I want." No need for cursor-based
  pagination.
- Per-DM threshold (e.g. "only DMs with at least N new messages").
  The existing `UnreadAll` filter (`len(Messages) > 0 ||
  len(Replies) > 0`) already drops empty DMs.
