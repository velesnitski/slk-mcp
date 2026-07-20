# ADR 056: summary mode for get_mentions (operational-load stats)

Date: 2026-07-20
Status: accepted

## Context

An operational-load report ("how many times was I pinged, by whom,
where") required pulling the full `get_mentions` hit list and
hand-counting senders/channels. Slack search's own counts are unreliable
(the same window returned 100 and 20 across two calls), and the list
form spends tokens on message bodies the report doesn't need.

## Decision

Add `summary: true` to `get_mentions`. Instead of the hit list it
renders aggregates over the **same filtered match set** the list view
would show — so `pending_only`, `strict_mention`, `drop_closing_acks`
and the `dm_history` backstop (ADR 053, more reliable than raw search
counts) all compose identically:

```
37 mentions (last 120h) — summary
split: 30 DM / 7 channel · 9 unique senders
senders: alice×17, bob×9, … (+N more)
channels: #backend×3, … 
```

`summarizeMentions` / `topCounts` are pure: counts sorted desc with
name-asc tie-break, lists capped at top-10 with a `+N more` tail, DMs
counted in the split but excluded from the channels line. Per-workspace
sections aggregate independently (the existing `runMentions` fan-out).

## Consequences

- The load report is one call per window instead of list-plus-manual
  aggregation, and rides the history backstop for saner counts.
- Additive: `summary` defaults to false; list output unchanged.
  `with_context` is meaningless in summary mode and simply unused.
- Helpers unit-tested: DM/channel split, sender counts and ordering,
  pending header, top-N capping.
