# ADR 053: DM history backstop for get_mentions (search-index lag)

Date: 2026-07-20
Status: accepted

## Context

`get_mentions` finds messages via Slack's `search.messages` (`to:me`).
Slack's search index **lags on DMs** — a message the other party sent
minutes ago is often not yet indexed, so `get_mentions` silently misses
it. Hit live: a colleague replied in a DM thread and `get_mentions`
(pending) didn't show it, while history-based tools
(`get_unread_summary` `dm_window`, `get_channel_digest`) saw it
immediately. Not an slk-mcp bug — it's the search backend — but a real
reliability gap for "what just landed in my DMs".

## Decision

Back the search sweep with a **history read for DMs**, which is
real-time (no index lag). After the `to:me` search, `buildMentions`
calls the existing `RecentDMActivity` (reads DM `conversations.history`
over the same window) and folds its fresh messages **from other people**
into the match set:

- `dmActivityToHits` converts each `ChannelUnread` message (top-level and
  thread reply) into a synthetic `goslack.SearchMessage`, filling
  user/text/ts/channel and a constructed permalink (with `?thread_ts=…`
  for replies, so reply-parsing and `with_context` behave exactly like a
  real hit). Own messages are dropped — a DM is "directed at you" only
  when the other side sent it; an empty self id makes it a no-op rather
  than surfacing your own lines.
- `mergeSearchHits` dedups by `(channel, ts)` — a real search hit wins
  over its history twin (canonical permalink) — and re-sorts newest-first
  so ordering matches a pure search.
- New `dm_history` arg, **default true**. `pending_only` / `strict_mention`
  / `with_context` all operate on the merged set unchanged (strict still
  drops DMs since they carry no literal `<@SELFID>`, which is correct).

## Consequences

- `get_mentions` no longer silently misses a just-arrived DM — the fresh
  message shows up via history even before search indexes it.
- Cost: one `RecentDMActivity` pass (DM history, 4-worker pool) per
  `get_mentions` call. `dm_history=false` restores the search-only,
  lower-latency behaviour.
- Reuses tested machinery (`RecentDMActivity`); the new conversion +
  merge are pure and unit-tested (self-exclusion, thread-ts permalinks,
  dedup, newest-first order). 564 → 567 tests. Minor release (1.12.0).
