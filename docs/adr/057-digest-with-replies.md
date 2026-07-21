# ADR 057: with_replies — thread drill-in for get_channel_digest

Date: 2026-07-21
Status: accepted

## Context

`conversations.history` returns only top-level messages, so
`get_channel_digest` rendered threads as bare `(N replies)` counters. A
channel whose real content lives in threads — a huddle with its
discussion attached, a request answered in replies — produced a digest
that *said* there were replies but couldn't show them. Hit live: a
call's thread had to be confirmed indirectly because the digest showed
`[huddle] (2 replies)` with no way to read the two replies (and Slack
search lags on fresh thread replies, so `get_user_messages` came back
empty too). The unread sweep already inlines replies (`fetchReplies`);
the drill-in digest — the tool you reach for to *investigate* a channel
— did not.

## Decision

Add `with_replies` (bool, default false) + `thread_preview_replies`
(int, default 10) to `get_channel_digest`.

- New `collectThreadReplies(ctx, MessageClient, channelID, window)`:
  for every thread parent in the window (`thread_ts == ts`,
  `reply_count > 0`) fetch `conversations.replies`, strip the parent
  (the API includes it), key by parent ts. Best-effort — one unreadable
  thread is skipped, not fatal. Takes the narrow `MessageClient`, so
  tests drive it with the existing `fakeMsgClient`.
- Rendering reuses the unread sweep's existing machinery:
  `format.WithThreadReplies` + `WithThreadPreviewReplies` — replies
  appear as the familiar `↳` continuation lines. Reply authors are fed
  into the user-name resolver alongside top-level authors.
- Default cap 10 per thread (the drill-in wants substance; unread's
  default 3 is for breadth). Multi-channel digest and morning recap stay
  reply-free — fan-out × threads would multiply API calls; drill into
  one channel to expand.

## Consequences

- "покажи канал вместе с тредами" is one call; the huddle-discussion
  case renders the actual conversation instead of a counter.
- Cost is explicit and opt-in: one `conversations.replies` per thread in
  the window, only when `with_replies=true`.
- 574 → 577 tests (fetch/strip, best-effort error, no-parents-no-calls).
  Minor release (1.14.0).
