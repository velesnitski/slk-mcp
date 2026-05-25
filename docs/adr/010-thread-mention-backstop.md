# ADR 010 — Thread-mention backstop on `get_unread_summary`

**Status:** accepted
**Date:** 2026-05-25
**Tag at acceptance:** v0.4.8

## Context

Silent-miss bug discovered in production use: a teammate tagged the
operator in a reply to a thread whose parent had already been read.
Slack delivered the mobile notification, but `get_unread_summary`
reported "0 mentions" and the channel was filtered out entirely
under `mentions_only=true`.

Root cause in `internal/slack/unread.go`:

```go
func (s *UnreadService) fetchReplies(ctx, channelID, oldest, cu) error {
    for _, m := range cu.Messages {  // ← only iterates NEW top-level
        if m.ThreadTimestamp == "" || m.ThreadTimestamp != m.Timestamp ...
        // fetches replies via conversations.replies
    }
}
```

`fetchReplies` only iterates `cu.Messages` — the new top-level
messages discovered by the unread sweep. If the thread's parent is
older than `last_read`, it's not in `cu.Messages`, so its replies
are never fetched. Slack's `users.conversations` does not expose
"unread thread reply count" in a way we can use here.

`ChannelMentions(cu, selfID)` then walks both `cu.Messages` and
`cu.Replies` looking for `<@SELFID>` — but the reply that mentions
the operator never made it into either. The channel is silently
filtered out.

### Options considered

- **a.** Subscribe to all threads the operator participates in and
  track unread reply counts client-side. Enterprise-only Slack API;
  doesn't apply to standard workspaces.
- **b.** Add a `conversations.replies` call for every thread the
  user is subscribed to per sweep. Unbounded — a heavy user may be
  subscribed to hundreds of threads.
- **c.** Use Slack's own `search.messages to:me` index as a backstop.
  Slack indexes the explicit `<@SELFID>` mention regardless of where
  it lands (top-level or thread reply, old parent or new). One
  search call per sweep, bounded by the existing rate-limit wrapper.
  Merge results into the unread sweep so downstream filters and
  rendering treat the mention identically to a natively-detected one.

## Decision

Use **(c)**. New method
`UnreadService.UnreadThreadMentions(ctx, hours)`:

1. Build query `to:me after:<date>` where `<date>` is `now − hours`
   formatted as `YYYY-MM-DD`.
2. Call `SearchService.Messages(ctx, query, 100)` (already wrapped
   for rate-limit handling).
3. For each hit, parse `?thread_ts=` from the permalink. If present
   and differs from the hit's own timestamp, it's a thread reply →
   attach to `cu.Replies[threadTS]`. Otherwise → `cu.Messages`.
4. Slack's `after:` is date-granular only, so messages from earlier
   the same day can leak in. The post-fetch filter drops anything
   older than the exact hour-precise cutoff.
5. Group hits by channel ID and return one `*ChannelUnread` per
   affected channel.

Handler integration: new optional `thread_mention_hours: int`
parameter on `get_unread_summary` (default `24`). When `> 0`, the
handler calls the backstop and merges via
`mergeThreadMentions(base, mentions)`:

- New channels (not in base because the parent was read) are
  appended.
- Existing channels (other unread activity surfaced them) gain the
  mention reply in their `Replies[threadTS]` bucket.
- Dedup by message timestamp so re-runs don't pile up duplicates.

`UnreadClient` contract grows by one method
(`UnreadThreadMentions`), matching the v0.4.7 pattern.

## Consequences

- **Closes the silent-miss gap.** A mention in a reply to an old
  thread now surfaces in the same sweep that would have surfaced a
  mention in a fresh top-level message.
- **One additional API call per sweep.** The cost is fixed (not
  per-channel) and bounded by `search.messages`'s 100-hit cap. The
  existing ratelimit wrapper handles backoff.
- **Default `thread_mention_hours=24`.** The default is *enabled*
  because the bug fix is opinionated — most consumers want
  mentions to be detected. Set to `0` to disable explicitly.
- **`mentions_only=true` now actually filters correctly.** Before
  this change, `mentions_only` could silently drop channels where
  the operator HAD been mentioned, because the mention reply was
  invisible to the filter.
- **Slack-search nuances apply.** Slack's `to:me` matches DMs and
  explicit `<@SELFID>` mentions, but NOT plain `@username` text or
  user-group mentions. The backstop inherits these limits — a
  workspace that uses @-group conventions will still miss some
  mentions, and the fix is documenting that, not changing the
  backstop primitive.
- **`UnreadService` now depends on `SearchService`.** The dependency
  is injected in `client.go` and passed as `nil` in disabled-token
  test setups; the method short-circuits on `s.search == nil`.

## Validation

- `TestUnreadThreadMentions_returnsNilOnZeroHours` —
  hours-zero must no-op.
- `TestUnreadThreadMentions_returnsNilWhenSearchAbsent` — missing
  search service must not panic.
- `TestUnreadThreadMentions_groupsByChannel` — multiple hits to
  the same channel collapse to one entry; thread vs top-level
  routing works (replies under `Replies[threadTS]`, top-level under
  `Messages`).
- `TestUnreadThreadMentions_filtersHourGranularLeakage` — confirms
  the post-fetch filter drops same-day-but-pre-cutoff hits.
- `TestSearchHitToMessage_parsesThreadTS` and `…noThreadTSForTopLevel`
  — pin the permalink parser.
- `TestMergeThreadMentions_*` (6 cases) — append new channels,
  merge into existing, dedup by ts (both Messages and Replies),
  nil-safety, top-level vs reply routing.
- Full suite: 361 → 373 green (+12).

## Out of scope

- Group mentions (`<!subteam^...>`) and user-group aliases. Slack's
  `to:me` doesn't match them, and the fix preserves that boundary.
- Polling Slack's notification API as an alternative source. The
  search-based approach is simpler, doesn't need additional scopes,
  and matches the existing surface area.
- Auto-marking the mention as read. Out of scope — the operator's
  intent is to *see* the mention, not auto-dismiss it.
