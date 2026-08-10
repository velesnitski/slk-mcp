# ADR 073: reach behind last_read so live threads stay visible

Date: 2026-08-10
Status: accepted

## Context

During a production incident the operator was following a channel
thread, replying in it, watching it move. A delta pull moments later
reported "all caught up" while the thread had grown from 3 messages to
15, including the message that closed the incident.

The `own_thread_hours` backstop (ADR 062) is supposed to cover exactly
this and did not, so the first diagnosis was that search lag had hidden
it: `UnreadOwnThreads` discovers threads with `search.messages` on
`from:me`, and Slack's index trails new messages by seconds to minutes.
That is true, and it is a real weakness, but it is not the cause.

The cause is one layer down and much simpler. `Unread` fetches
`conversations.history` with `Oldest: last_read`, so it only ever sees
messages newer than the read marker. `fetchReplies` then walks *those*
messages looking for thread parents. A thread whose parent is already
read is therefore never returned by history, never walked, and its
replies cannot enter the sweep at all — regardless of how active it is,
regardless of who is talking. Reading a channel permanently removes its
existing threads from the sweep.

Every previous fix in this family (057 channel-thread replies, 062 own
threads, 064 thread lanes, 070 latest-mode) added a *discovery
mechanism* alongside this blind spot. None removed it.

## Decision

Widen the history window instead of adding another mechanism.

`Unread` now pulls history from `min(last_read, now - 12h)` and hands
the **full page** to `fetchReplies` as the parent list, while
`cu.Messages` keeps its exact previous meaning: strictly newer than
`last_read`. Pure `activeThreadParents` then keeps only parents whose
`latest_reply` is newer than `last_read` — a field Slack already returns
on every parent, so the filter costs nothing and prevents the wider
window from fanning out into dead threads. A parent with no
`latest_reply` recorded falls back to the original rule, so nothing
regresses.

The page size gains `threadParentHeadroom = 30`. `conversations.history`
returns newest-first, so the genuinely new messages are always in the
page; the headroom is what leaves room for the older parents behind
them.

## Consequences

- A thread stays visible for as long as it is alive, whether or not the
  operator started it, replied in it, or was mentioned. This is the
  generic fix the three earlier backstops were each approximating.
- Cost is one larger page per channel, not more calls. Extra
  `conversations.replies` calls happen only for parents that genuinely
  moved since `last_read`.
- Threads whose parent is older than 12 hours still cannot surface this
  way. That is deliberate: it bounds the work, and a day-old parent with
  new replies is what the mention and own-thread backstops are for.
- Search lag remains unaddressed and is now understood to be a separate,
  smaller issue: `UnreadOwnThreads` still cannot see a thread the
  operator only just joined and that has no other trace. Left alone
  rather than papered over with a cache.
- 618 → 619 tests. Minor release (1.30.0).
