# ADR 029 — workspace-aware reads + post_message dedup

**Status:** accepted
**Date:** 2026-06-30
**Tag at acceptance:** v0.5.5

## Context

ADR 023 made the *sweeps* (`get_unread_summary`, `get_mentions`)
multi-workspace, and ADR 027 made the *write* + single-channel
*introspection* tools (`post_message`, `add_reaction`, `list_channels`,
`get_channel_info`, archive/unarchive, `delete_message`) workspace-aware.
But the **single-channel / search READ** tools were never wired up — they
always hit the primary client.

This bit in daily use: the unread sweep surfaces a secondary workspace's
activity, but you couldn't then **drill into** any of its channels —
`get_channel_digest`, `get_thread`, and `search_messages` were
primary-only. Concretely: a digest of a non-primary channel, a date-ranged
breakdown of a non-primary reporting channel, and "did user X post in the
other workspace" were all impossible. ADR 027 even
flagged this as deferred ("Not in scope: per-workspace search_messages /
get_thread — a later ADR if cross-workspace search is needed"). It's
needed.

Separately, a recurring operator instruction — "don't post if it's already
there" — had to be enforced by hand (fetch the channel, eyeball it), and
couldn't be checked at all for a non-primary workspace (no workspace-aware
digest).

## Decision

### A. Workspace-aware reads

Add the optional `workspace` arg (the existing `workspaceTarget` helper
from ADR 027, empty → primary) to:

- `get_channel_digest` — scopes `channelDigestRange` via `withClient`.
- `search_messages` — scopes `Search()`.
- `get_thread` — scopes `Channels().ResolveID` + `Messages().ThreadReplies`
  + `resolveRefs`.

Same pattern as the write tools: unknown label → `unknownWorkspaceMsg`
before any Slack call; empty → primary, so single-workspace behaviour is
unchanged.

**Deferred** (channel-list *sweeps* whose channel resolution comes from
`SLACK_CHANNELS`/auto-discover, a bigger change): `get_multi_channel_digest`,
`get_morning_recap`, `find_decisions`, `get_user_messages`, `mark_read`,
`get_list_items`. `search_messages workspace=…` already covers the common
"find X in the other workspace" need via `from:@user in:#chan`.

### B. post_message dedup (`skip_if_recent`)

`post_message` gains `skip_if_recent` (minutes, default 0 = off). When > 0,
`recentSelfDuplicate` checks whether the authenticated user already posted
the identical (trimmed) text into that channel within the window; if so the
post is skipped (`skipped … (skip_if_recent)`) instead of duplicated.

**Best-effort, fails OPEN:** it needs a user token to resolve "self"
(`Unread().Self`); with no user token, or on any auth/history error, it
returns false and the post proceeds — a missed dedup is recoverable, a
refused post is not. Works per-workspace (runs against the scoped client),
so the guard finally covers non-primary channels too.

## Consequences

- The multi-workspace **read** surface is closed for the high-traffic
  tools: digests, threads, and search now drill into any workspace.
- The "don't repeat my message" instruction is enforceable in-tool and
  cross-workspace, not by manual pre-check.
- Backward-compatible: every new arg defaults to the prior behaviour
  (primary / dedup off).
- +1 test (`recentSelfDuplicate` no-user-token fail-open, network-free);
  the `runPostMessage` signature gained `skipIfRecentMin` (call sites +
  the unknown-workspace test updated). 471 → 472.
- Not closed: the deferred sweep tools above — a later ADR if per-workspace
  recaps/decisions are wanted.
