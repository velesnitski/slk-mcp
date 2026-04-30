# ADR 0001: `get_unread_summary` trusts `conversations.info`, not `users.conversations`

- Status: Accepted
- Date: 2026-04-30

## Context

`get_unread_summary` was returning `all caught up — 0 unread` even when the
Slack UI showed several channels in bold (i.e. with new messages). The tool
backs onto `UnreadService.UnreadAll`, which:

1. Lists joined channels via `users.conversations`
   (`GetConversationsForUserContext`).
2. For each channel, fetches `conversations.info` to read `last_read` /
   `unread_count`, then `conversations.history` for messages newer than
   `last_read`.

To save calls, step 2 was guarded by an early-skip on the list response:

```go
if ch.UnreadCount == 0 && ch.UnreadCountDisplay == 0 {
    continue
}
```

The `users.conversations` Web API endpoint does **not** populate
`unread_count` / `unread_count_display`. Those fields are filled in by
`conversations.info` (and a handful of other endpoints), not by the
listing endpoint. As a result every joined channel looked like "zero
unread" in the listing pass and was skipped before the authoritative
`conversations.info` lookup ever ran.

## Decision

Remove the pre-filter. `UnreadService.Unread` already short-circuits when
`info.UnreadCount == 0` after the `conversations.info` round-trip, so the
correct behaviour falls out of the existing per-channel logic — at the
cost of one `conversations.info` call per joined channel.

The list-response counters are not a reliable signal and must not be used
for filtering. If we need to reduce per-channel calls in future, the right
move is to batch `conversations.info` (or stop iterating on cursor pages
once we've answered the user's prompt), not to trust the listing fields.

## Consequences

- Correct: channels with unread messages are surfaced again.
- API cost: `UnreadAll` now issues `N` `conversations.info` calls where
  `N` is the number of joined channels (capped at the existing 4-worker
  concurrency and the rate-limit retry helper). For typical workspaces
  (tens to a few hundred channels) this completes in seconds.
- Test coverage: `internal/slack/unread_test.go` adds a regression test
  (`TestUnreadAll_DoesNotTrustJoinedChannelsUnreadCount`) plus broader
  coverage of `Unread`, `JoinedChannels`, and `MarkRead` against an
  `httptest` Slack stand-in.
