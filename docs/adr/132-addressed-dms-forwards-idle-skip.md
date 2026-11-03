# ADR 132: DMs count as addressed, forwards name their author, idle conversations are not read

Date: 2026-11-03
Status: accepted

## Context

Five things went wrong or slow in one morning's sweeps:

1. `get_unread_summary` with `mentions_only=true` reported "no unread
   channels mention you" while two DMs were waiting. The filter kept only
   conversations with an `<@self>` mention, and nobody writes @you to the
   person they are writing to, so every DM was dropped.
2. A forwarded message rendered as the forwarder's own words. A forward
   has an empty body and carries the original in an attachment; the
   hidden-payload renderer lifted the text without its author, and lifted
   it twice when Slack sent it once with a skin-tone modifier and once
   without. A forward with a comment lost the forwarded text altogether,
   since the payload is rendered only for an empty body.
3. The list of channels cut by `max_chars` showed `#channel (0)` for a
   channel whose news was all thread replies: the count was top-level
   messages only.
4. The unread pass is nearly all of a sweep's time (ADR 131). For every
   joined conversation it calls `conversations.info` and then reads a
   history page, even when the conversation has had nothing new for days.
5. The DM window waited for the whole unread pass, to reuse its DM pages
   (ADR 125). When the deadline cut the pass, the DM window never ran,
   and the sweep had no recap of DMs at all.

## Decision

1. `mentions_only` keeps DMs and group DMs as well as channels with a
   mention.
2. An attachment with the original's `ts` and a `from_url` permalink under
   `/archives/` is a shared Slack message (a forward, or the unfurl of a
   pasted message link). It renders as `↪ <author>: <text>`, the author
   resolved from `author_id` and then from `author_name`. Next to a
   comment it is appended to the body. Payload parts are compared without
   skin-tone modifiers when duplicates are dropped. Bot cards may set
   `ts` or `author_name` but carry no message permalink, so they render as
   before.
3. The omitted-channel list counts replies too: `(3)`, `(2 replies)`,
   `(1+2 replies)`.
4. The history read is skipped when `conversations.info` returns a
   `latest` message that is read, older than the 12-hour thread lookback,
   and, when it sits exactly at `last_read`, carries no thread. The page
   then holds no new message and no thread parent the reply fetch would
   consider, so the skip loses nothing the pass would have shown. When
   Slack sends no `latest`, the page is read as before. DMs are queued
   first, so a pass the deadline cuts has read them.
5. The DM window runs beside the unread pass. The two share DM pages
   through a cache that lives in the sweep's context: whichever phase asks
   for a page first reads it, and the other waits for that read instead of
   reading again. A failed read is not shared; the second reader reads for
   itself. The DM window asks for the page length the unread pass uses for
   a DM, so one read serves both. The cache exists only for the sweep that
   created it, and only `get_unread_summary` creates one, because its
   delta cursor is capped at the sweep's start (ADR 125).

`show_timings` adds one line with the unread pass's conversations,
history reads, idle skips and failures, so the effect of the skip is
visible from a sweep.

## Consequences

- `mentions_only` sweeps show DMs.
- A forward shows who wrote it, and a commented forward shows what was
  forwarded.
- How much the skip saves depends on whether Slack's `conversations.info`
  returns `latest` for every conversation type. The stats line answers it
  on real workspaces; when `latest` is missing, nothing changes.
- A sweep no longer loses its DM window to a deadline in the unread pass,
  and the two phases make no more history calls than before.

## Test note

`TestUnreadOwnThreads_ReportsTheDeadline` expected exactly one
`conversations.replies` call after a cancel. Since ADR 131 the own-thread
replies are fetched by concurrent workers, so a second thread's request
can already be in flight when the first cancels; CI's race run caught it.
The test now asserts what the deadline guarantees: at most one call per
thread, no retry.
