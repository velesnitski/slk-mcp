# ADR 127: a top-level mention followed by an own channel post is annotated, not dropped

Date: 2026-10-22
Status: accepted

## Context

ADR 126 drops a thread mention the operator has since answered in the
same thread. A mention posted at the top level of a channel is often
answered differently: the operator writes a new top-level message in
the channel instead of opening a thread. That mention kept surfacing as
pending.

The two cases differ in certainty. A later reply in the mention's own
thread answers that thread. A later top-level post in a busy channel may
answer the mention or may be about something else entirely; dropping
the mention on that evidence could hide a question that was never
answered.

## Decision

- The thread mention backstop also keeps, per channel, the operator's
  newest top-level post, from the same `from:me` search ADR 126 runs —
  no extra call. Thread replies do not count here.
- A top-level mention older than that post is kept and marked: the
  channel's `FollowUps` maps its ts to the post's ts, and the digest
  prints "you posted in this channel after it (HH:MM) — check whether
  that answered it" under it (`format.WithFollowUps`).
- Thread replies are never annotated this way; ADR 126 covers them.
- `mergeThreadMentions` carries the annotations into the merged channel.

## Consequences

- The reader sees that the mention was probably handled and when, and
  decides; nothing is hidden on uncertain evidence.
- The annotation costs one line per followed-up mention.
