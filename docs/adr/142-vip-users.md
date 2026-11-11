# ADR 142: VIP users come first

Date: 2026-11-11
Status: accepted

## Context

Slack lets a person mark VIPs whose messages notify even under Do Not
Disturb. That is a statement of priority, but the digests ignored it: a
VIP's one-line DM could rank below a busy channel and fall off the end
under `max_chars`, and an ask from a VIP waited out `stale_hours` like
anyone else's. Slack keeps the VIP list in client preferences with no
public API, so the server cannot read it.

## Decision

`SLACK_VIP_USERS` mirrors the list: names or handles, comma-separated,
one list for all workspaces.

- **Matched per workspace, never guessed.** Each entry becomes the one
  person whose ID, handle, display or real name it is, or whose real
  name holds all its parts in any order (platforms swap them). A name two
  people share, or nobody has, is reported as "VIP not matched here", so
  the list gets fixed. Matches are cached for 30 minutes; a roster that
  cannot be read leaves the normal order and is retried a minute later.
- **`get_unread_summary`:** a conversation is VIP when it is a DM with a
  VIP or holds a new message or thread reply by one. VIP conversations
  come right after priority channels, are labelled `⭐ VIP`, and are
  never collapsed as low-signal.
- **`get_my_actions`:** a commitment is VIP when a VIP asked it, it sits
  in a DM with one, or it mentions one. VIP items lead each list and are
  labelled. A VIP's unanswered ask or question is on the ping list at
  once instead of after `stale_hours`. Deadlines, and the judgement of
  open or done, do not change.

## Consequences

- With no VIPs configured nothing changes and no roster is read.
- The list must be kept in step with Slack's by hand.
