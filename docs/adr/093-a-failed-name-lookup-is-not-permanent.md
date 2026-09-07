# ADR 093: a failed name lookup is not permanent

Date: 2026-09-07
Status: accepted

## Context

`UserService` resolves user IDs to display names and caches the result
for the life of the process. That is right for successes: display names
change rarely, and a slightly stale name is harmless.

It was also doing it for failures. On any error — a rate-limit pause, a
dropped connection, a request cancelled mid-flight — the resolver wrote
the ID into the success cache as its own name and returned. The entry
was indistinguishable from a real one, so it was never retried.

The effect was out of all proportion to the cause. One transient error
during one sweep pinned that person to a raw `U…` on every surface for
the rest of the session: digests, mention lists, thread context, search
results. Nothing recovered it short of restarting the server, and
nothing indicated that a lookup had ever failed — the reader simply saw
IDs where names should be, in a report that otherwise looked complete.

Failures also cluster. A rate-limit pause hits a batch of IDs at once,
so an entire conversation's participants can go anonymous together.

## Decision

Keep two maps. Successes live in the cache as before. Failures go into
a separate map holding the time of the failure, and are honoured only
for `userResolveRetryAfter` — one minute.

Inside that minute the ID is returned without touching the API, so a
hot loop cannot hammer a failing endpoint. After it, the lookup is
tried again. A success clears the recorded failure.

The clock is injected so the window is testable without sleeping.

## Consequences

- A transient failure degrades one report, not the session.
- Genuinely unknown IDs — deleted users, other workspaces — cost one
  API call per minute rather than one per reference.
- Cache reads take one lock acquisition for both maps; the hot path is
  unchanged.
