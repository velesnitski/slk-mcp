# ADR 027 — make channel-addressed tools workspace-addressable

**Status:** accepted
**Date:** 2026-06-24
**Tag at acceptance:** v0.5.3

## Context

ADR 023 added multi-workspace support, but only to the **read sweeps**
(`get_unread_summary`, `get_mentions`, the digests) via `workspaceTargets`
+ `withClient`. The **write and single-channel tools** were never wired
up: `post_message`, `add_reaction`, `list_channels`, `get_channel_info`,
`archive_channel`, `unarchive_channel` all hard-target the primary
workspace (`h.client` / `h.Channels()`), with no `workspace` argument.

This surfaced in practice: asked to post the same message into a channel
present in both workspaces, the server could only reach the primary one.
`list_channels` returned a single workspace's channels, and `post_message`
had no way to route a write to the secondary workspace at all. A
half-migrated surface (read tools workspace-aware, write tools not) is a
footgun — "why can I read the secondary workspace's inbox but not post to
it?".

## Decision

Every channel-addressed tool now accepts an optional `workspace` argument.
Two resolution shapes, matching how the tool is used:

### Fan-out reads → `workspaceTargets` (empty = all)

`list_channels` reuses the digest pattern: an empty `workspace` lists
**every** workspace, each in its own `## [label]` section (multi only); a
named one scopes to it; an unknown one errors. A single-workspace install
renders the flat list **byte-for-byte as before**. Logic extracted to
`runListChannels` + `renderChannelList` so the routing is testable.

### Single-target tools → `workspaceTarget` (empty = primary)

`post_message`, `add_reaction`, `get_channel_info`, `archive_channel`,
`unarchive_channel` use a new `workspaceTarget` helper — the write-side
twin of `workspaceTargets`. An empty `workspace` resolves to the **primary**
(registry[0]), keeping every existing call backward-compatible; a named
one scopes via `withClient`; an unknown one errors with the configured
labels. The asymmetry is deliberate: **you sweep all inboxes, but you post
to one.**

Confirmations gained a `wsLabel` suffix — `posted to #general [secondary]
(ts: …)` — emitted **only when multiple workspaces are configured**, so
single-workspace output is unchanged.

`post_message` routing was extracted to `runPostMessage` so its
unknown-workspace path (which returns before any Slack call) is
unit-testable without a live server, mirroring `runUnreadSummary`.

## Consequences

- Cross-workspace posting works: `post_message(channel="general",
  workspace="secondary")` reaches the secondary workspace.
- The whole channel-addressed surface is uniformly workspace-aware — no
  more "reads see all workspaces, writes see one" mismatch.
- Backward-compatible: every tool's behaviour with `workspace` absent is
  identical to v0.5.2 (single → flat/unlabelled; the primary for writes).
- New tests cover `workspaceTarget` (empty→primary, named, case-insensitive,
  unknown→nil), `wsLabel` (multi vs single), and the unknown-workspace
  error paths of `runPostMessage` / `runListChannels`, all network-free.
- Not in scope: per-workspace `search_messages` / `get_thread` (they take
  IDs, not a workspace handle) — a later ADR if cross-workspace search is
  needed.
