# ADR 050: list_scheduled_messages — surface your queued-to-send messages

Date: 2026-07-15
Status: accepted

## Context

The digest tools show what's come IN, but nothing showed what the
operator has queued to go OUT — Slack's scheduled messages. Those are
part of the "full picture" (what am I about to send, and when), and had
no read path through the MCP.

A sibling ask — silencing reply notifications for a specific thread —
was prototyped in the same cycle and **rejected**: Slack's
`subscriptions.thread.remove` returns `not_allowed_token_type` for our
OAuth (`xoxp`) token (it needs the browser `xoxc` session token), so
thread-level unfollow is not viable through the MCP. That spike was
stripped; only the scheduled-messages read — which uses a documented,
OAuth-friendly method — ships here.

## Decision

Add a read-only `list_scheduled_messages` tool over
`chat.scheduledMessages.list` (slack-go `GetScheduledMessagesContext`).

- New `ScheduledService` on the **user** client (scheduled messages are
  per-identity — a bot token wouldn't see the operator's queue). `List`
  paginates to completion; `Enabled()`/`ErrNoUserTokenScheduled` guard a
  bot-only workspace. `ScheduledClient` contract + compile assertion;
  `Hub.Scheduled()` accessor.
- `list_scheduled_messages(workspace)` is **you-global** like the digest:
  an empty `workspace` lists across every configured workspace, each in
  its own section. Bot-only workspaces are skipped; a per-workspace fetch
  error is reported inline so one bad workspace doesn't sink the rest.
- Rendering is a pure, unit-tested helper (`renderScheduled`): sort
  soonest-first, resolve channel IDs to names (raw id when unresolved,
  e.g. a DM), local send time, and a rune-safe 80-char preview (so
  Cyrillic isn't split). `scheduledErrHint` maps a scope/token failure to
  the messaging-scope fix.
- Registered whenever a user token exists; **not** gated on
  `SLACK_READ_ONLY` — it mutates nothing.

## Consequences

- "what have I got queued to send, and when" is now one call, across both
  workspaces. A future step can fold a "queued to send" section into
  `get_unread_summary` for a truly complete daily picture.
- Read-only, additive, no change to existing tools. New tool → minor
  release (1.9.0); tool count 23 → 24. 554 → 560 tests.
- Documented, OAuth-friendly method (unlike the rejected thread-unfollow),
  so no token-type surprise — validated by build against slack-go's real
  types and the paginating List path.
