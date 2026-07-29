# ADR 064: threads are separate lanes in answered-DM suppression

Date: 2026-07-29
Status: accepted

## Context

Answered-DM suppression (ADR 059/062) probes a DM's newest messages via
`conversations.history` — which returns **top-level messages only**. A
counterpart's live question posted as a **thread reply** is therefore
invisible to the probe, so a DM got suppressed merely because the
operator's newest *top-level* message (about an unrelated topic) was
later in wall-clock time. Hit live: an analyst's substantive thread
reply (BigQuery table layout, 11:42) was hidden because the operator had
posted a new top-level message at 12:01 in the same DM.

The modelling error was comparing globally by timestamp. A thread is a
**separate conversation lane**: holding the last word at top level says
nothing about whether a thread is still waiting on you — the same
insight ADR 057 applied to channel digests.

## Decision

`isAnsweredDM` now checks every thread first, before the top-level
window:

- `threadEndsWithLiveCounterpart(replies, selfID)` (pure): walk a
  thread's replies from the newest backwards — operator message → lane
  closed; substantive counterpart message → lane live; trailing closing
  acks (ADR 062) are skipped.
- Any live lane keeps the DM visible, **regardless of wall-clock order**
  of the operator's top-level messages.
- Threads come from the already-fetched `ChannelUnread.Replies`, so the
  fix costs **zero extra API calls**.

## Consequences

- The last structural blind spot of the suppression closes: top-level
  (ADR 059), ack tails (ADR 062), and now thread lanes.
- Suppression stays conservative — it can now only hide a DM when every
  lane AND the top level are settled.
- 591 → 593 tests (lane semantics incl. ack tail and guards; the
  real-world "newer top-level vs live thread reply" shape). Minor
  release (1.21.0).
