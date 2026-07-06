# ADR 035: unread delta cursor + refs gate — token-effective re-pulls

Date: 2026-07-06
Status: accepted

## Context

Dogfooding surfaced two token sinks in `get_unread_summary`, the
highest-traffic tool (multiple pulls per day):

1. **No delta.** The operator asks for "today's summary" several times a
   day. Each pull re-emits ~80% identical content — unread state
   accumulates and DM threads repeat verbatim. `mark_read` is not a fix:
   it would corrupt the operator's real Slack unread state. A day of
   re-pulls wasted an estimated 10–12k tokens.
2. **References block is always-on balloon.** ~100 issue IDs + branches +
   MRs render on every pull (~400–600 tokens); 3–5 of them get used.

## Decision

Two arguments on `get_unread_summary`, both defaulting to the cheaper
behaviour:

- **`after` (delta cursor).** A Slack ts; `filterAfter` prunes every
  message and thread reply at or before it, then drops channels left
  empty — but keeps a channel alive on a *fresh reply to an old
  parent*, since that is still new signal. Applied after the DM /
  thread-mention merges so it prunes those too. Fail-open on
  unparseable ts (a message we can't place in time is kept, never
  silently dropped).
- **`include_refs` (default false).** Gates the trailing References
  block. The information callers actually chained into it for — a
  machine-readable handle on the pull — is replaced by one cheap line.

The line is the **cursor**: `newestTS(results)` emits the max ts across
all messages/replies as `cursor: <ts>` in the header. Feeding it back
as `after` next pull yields only what arrived since — the delta loop is
self-perpetuating and the operator never tracks wall-clock time. A
property test pins the contract (`newestTS` → `after` → empty).

## Consequences

- A same-day re-pull now costs roughly what actually changed, not the
  whole backlog — ~50–60% off repeat pulls at the observed cadence,
  plus ~400–600 tokens saved per pull from the default-off refs.
- `after` is purely subtractive and defaults empty, so every existing
  caller (and all 502 prior tests) is byte-unchanged except for the new
  one-line `cursor:` header and the now-absent refs footer — neither of
  which any test asserted on.
- `RenderReferences` / `CollectReferences` stay; they are one flag away
  and still used when a caller will chain into the IDs.
- Deliberately scoped to `get_unread_summary`. `get_mentions` renders
  no refs and is not a re-pull hotspot; leaving it alone keeps the
  change tight. 502 → 510 tests.
