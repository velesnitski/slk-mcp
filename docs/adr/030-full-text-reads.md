# ADR 030 — full_text on get_thread + get_channel_digest

**Status:** accepted
**Date:** 2026-06-30
**Tag at acceptance:** v0.5.6

## Context

`MessageLine` truncates a long body to `MessageLineLimit` and appends a
`(+N chars)` marker — the right default for compact digests/unread sweeps,
where dozens of messages share a token budget. But `get_thread` and
`get_channel_digest` route every body through it with **no escape hatch**,
so a single content-rich message comes back clipped (e.g. `+1728 chars`
hidden).

That bit a real task: ingesting a Slack thread into a knowledge base, where
the three most substantive messages were exactly the ones truncated. The
only workaround was `search_messages full_text=true` (which already had an
un-truncated mode) — awkward, and it can't fetch a specific thread's
replies. The reads needed their own `full_text`.

## Decision

Add `full_text` (bool, default false) to **`get_thread`** and
**`get_channel_digest`**.

Format layer:
- `MessageLineFull` — the existing `MessageLine` logic with the
  `MessageLineLimit` truncation skipped. Both now delegate to a private
  `messageLineImpl(msg, userName, users, fullText)`.
- `ChannelDigest` gains a `WithFullText()` `DigestOption`; it threads
  `fullText` to both the top-level render and `writeReplies`, so reply
  chains are un-truncated too.

Tools layer:
- `get_thread`: when `full_text`, render replies with `MessageLineFull`.
- `get_channel_digest`: `full_text` flows through `channelDigest` /
  `channelDigestRange` into `ChannelDigest(..., WithFullText())`.

Default behaviour is unchanged everywhere (truncated). `get_multi_channel_digest`
/ `get_morning_recap` keep truncating — they're breadth tools; pass
`fullText=false` explicitly.

## Consequences

- A thread or single-channel digest can be pulled verbatim — the KB-ingest
  case no longer needs the search workaround.
- Caller beware: `full_text` on a busy channel can be large; it's opt-in
  and pairs with `max_messages` / a tight `after`/`before` window.
- `MessageLine`'s public signature is unchanged (delegates); new
  `MessageLineFull` is additive. +2 format tests (truncate vs full for both
  `MessageLine` and `ChannelDigest`). 472 → 474.
- Not touched: `search_messages` already had `full_text`; the sweep tools
  stay compact by design.
