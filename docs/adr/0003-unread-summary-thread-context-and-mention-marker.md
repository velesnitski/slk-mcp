# ADR 0003: thread context + mention markers in `get_unread_summary`

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0001 (`get_unread_summary` correctness).

## Context

Operators using `get_unread_summary` to triage Slack asked for two
upgrades:

1. **Thread context.** A top-level unread message that begins a thread
   used to be rendered alone, hiding any new replies. Replies are
   often where decisions are reached and where direct mentions land.
2. **"Where I'm tagged" should jump out.** `get_mentions` already
   exists as a separate tool, but reading a long unread digest is the
   primary triage flow; mentions buried in dozens of messages are easy
   to miss.

Both items are LLM-friendly *only if* the raw output structures them.
Asking the model to "scan for mentions" works less reliably than
prepending a marker character it can pattern-match on; same story for
indented replies under their parent.

## Decision

Three additions, scoped to the unread summary path:

1. `UnreadService.Self(ctx)` — calls `auth.test` once, caches the
   `user_id` for the lifetime of the service. Used to resolve the
   operator's own ID without forcing it into config.
2. `UnreadService.Unread` — for every top-level unread message that is
   itself a thread parent (`thread_ts == ts && reply_count > 0`),
   fetch `conversations.replies` and store replies newer than
   `last_read` on `ChannelUnread.Replies` (keyed by parent timestamp).
   Failure to fetch replies is logged and ignored; the rest of the
   digest still renders.
3. `format.ChannelDigest` — variadic `DigestOption`:
   - `WithMentionHighlight(selfID)` prepends `🏷️ ` to any line whose
     body contains `<@selfID>` (parents and replies alike).
   - `WithThreadReplies(map[string][]Message)` inlines replies
     indented under their parent, capped at `ThreadPreviewReplies`
     (3) with a `+N more replies` collapse.
4. `tools/unread.go` ranks channels with mentions ahead of plain busy
   channels (`rankUnread` adds +1,000,000 to the volume score when a
   mention is present, dominating any plausible volume tie).

## Scope and limits

- **Replies on already-read parents.** If a thread parent is older
  than `last_read` but has new replies, `conversations.history` does
  not return that parent (it filters by the parent's own timestamp,
  not its `latest_reply`). Detecting this case would require either a
  per-channel `latest_reply > last_read` scan or wiring a separate
  signal — both expensive on workspaces with many threads. Operators
  who care about that case should keep using `get_mentions`, which
  pulls directly from `search.messages` and catches the mention even
  when the surrounding thread is otherwise read.
- **Localisation.** Mention syntax is `<@USERID>` regardless of the
  user's display language; the marker logic is byte-based and works
  identically for messages in Russian, English, etc.
- **Bot user mentions.** `Self()` returns the user-token's `user_id`,
  which is the human operator. Bot mentions are not highlighted
  (intentionally — bot pings are usually noise).

## Consequences

- API cost: at most one `conversations.replies` call per unread thread
  parent. With 4 worker channels × ~3 thread parents per channel per
  digest, this is roughly 12 extra calls — well within the
  rate-limited retry budget.
- One extra `auth.test` call per process lifetime. Cached after first
  use.
- Backwards compatibility: `ChannelDigest` is variadic, so the digest,
  recap, and search tools that don't pass options are unchanged.
- Test coverage: 8 new unit tests across `internal/format` and
  `internal/slack` packages — covering mention substring guards
  (`<@U0010>` must NOT match `<@U001>`), Russian-language messages,
  reply truncation, the boundary case where a reply equals `last_read`
  exactly, and the no-fetch case for thread parents with `reply_count == 0`.
