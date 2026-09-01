# ADR 087: thread replies render even when their parent is out of window

Date: 2026-09-01
Status: accepted

Amends the `WithOmitEmpty` behaviour introduced in ADR 021.

## Context

The unread sweep counted thread replies it never showed. A pull would
report `6 channels, 1 top-level + 16 thread replies` and render none of
them.

`ChannelDigest` is driven by top-level messages; replies ride along as an
option, keyed by parent timestamp. When a channel's only new content was
replies to a parent older than the window, `messages` was empty,
`WithOmitEmpty` returned `""`, and the caller's `if rendered == ""
{ continue }` dropped the channel — after the counters upstream had
already counted those replies.

The shape of what this hides is the problem. **An escalation is almost
always a reply.** Someone files a report; two hours later the answer,
the root cause and the "this is now blocking users" land underneath it.
By then the parent has scrolled out of the delta window, so precisely the
messages that changed the situation were the ones dropped — while the
header promised they existed.

`WithOmitEmpty` was not wrong when written (ADR 021). Its case was a DM
pulled in by `dm_window_hours` carrying *stale* replies the operator had
already read. That premise does not survive the sweep: `filterAfter`
prunes everything at or before the cursor before rendering, so any reply
that reaches the renderer is new by construction.

## Decision

When a channel has no top-level messages but does have replies,
`ChannelDigest` renders them as their own block instead of returning
empty:

    ## #channel (3 replies in earlier threads)
    thread from 14:19:
      ↳ [16:51 Ada] …

Threads are ordered by parent timestamp so the block reads
chronologically, and each carries the parent's time — the only handle a
reader has for finding a thread whose parent is not in this result.

`WithOmitEmpty` keeps its original job: a channel with genuinely nothing
still collapses to `""`, and an empty reply chain does not resurrect it.

## Consequences

- The header count and the body now agree. A counter that promises more
  than the output shows is worse than a smaller number, because it reads
  as completeness.
- Two existing tests asserted the old behaviour and were rewritten, not
  deleted: they encoded the bug. Their real concern — that a truly empty
  channel is dropped — is kept as its own test.
- Slightly more output per sweep, bounded by the same reply cap and
  char budget as every other block.
- The fix sits in `ChannelDigest`, so every caller benefits, not just the
  unread sweep.

## Related gap, not fixed here

The replies are shown without their parent's text. Fetching it would
cost one API call per orphan thread on a sweep that is meant to be cheap.
The parent timestamp is enough to locate the thread, and the tools to
open it (`get_thread`, `get_message`) already exist.
