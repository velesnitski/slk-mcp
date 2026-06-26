# ADR 028 — delete_message tool

**Status:** accepted
**Date:** 2026-06-26
**Tag at acceptance:** v0.5.4

## Context

The write surface had `post_message` and `add_reaction` but no way to
remove a message. A real need surfaced: a couple of `post_message` calls
fired before a "don't post if it's already there" instruction landed,
leaving duplicate messages the operator then had to delete by hand in the
Slack UI because the server simply couldn't.

## Decision

Add `delete_message`, wrapping `chat.delete` (`DeleteMessageContext`).
It mirrors the existing write tools exactly:

- **Workspace-aware** (ADR 027): optional `workspace`, resolved through
  `workspaceTarget` (empty → primary), confirmation carries the `[label]`
  suffix only when multi-workspace.
- **Write-gated**: registered inside the `SLACK_READ_ONLY` block, so it's
  absent in read-only mode alongside `post_message` / `add_reaction`.
- **Routing extracted** to `runDeleteMessage` so the validation and
  unknown-workspace paths (all of which return before any Slack call) are
  unit-testable without a live server, matching `runPostMessage`.

### Smart targeting — permalink OR channel+ts

The tool accepts **either** a Slack `permalink` **or** `channel` +
`timestamp`. A permalink already carries a canonical channel ID, so when
given it wins and short-circuits the name→ID lookup — one paste straight
from search/digest output deletes the message. `channel` + `timestamp`
(e.g. the `ts` returned by `post_message`) is the other path. Reuses the
existing `ParseSlackPermalink` helper — no new parsing.

### Safety

Deletion is irreversible, but the blast radius is bounded **server-side**:
with a user token Slack only permits deleting messages that user authored
(`cant_delete_message` otherwise); a bot token, only the bot's own. We do
not re-implement that check client-side — we surface it. `deleteErrorHint`
translates the two common terse failures (`cant_delete_message`,
`message_not_found`) into actionable text instead of a bare code. No
interactive confirmation exists in MCP; the read-only gate plus the
ownership constraint are the guardrails, and the description flags
irreversibility.

## Consequences

- The write surface can now clean up after itself; the duplicate-message
  situation that motivated this is recoverable from the server.
- Tool count 15 → 16. `MessageClient` gains `Delete`; the compile-time
  contract assertion and the `fakeMessageClient` test double are updated.
- +6 tests (validation: missing target, invalid permalink, permalink fills
  target, unknown workspace; plus `deleteErrorHint`). 466 → 471.
- Not in scope: a `post_message` "skip if duplicate" guard (the other half
  of the motivating incident) — a separate, opt-in change if wanted.
