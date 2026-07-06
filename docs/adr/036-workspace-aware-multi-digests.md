# ADR 036: workspace-aware `get_multi_channel_digest` / `get_morning_recap`

Date: 2026-07-06
Status: accepted

## Context

ADR 034's architecture review recorded one surface inconsistency: every
read tool took a `workspace` argument except `get_multi_channel_digest`
and `get_morning_recap`, which silently swept the primary workspace
only. Freezing the tool surface at 1.0 with two read tools that can't
address a secondary workspace would bake the asymmetry into the
compatibility contract — a schema-level gap, not cosmetic.

## Decision

Both tools gain `workspace` (workspaceArgAll wording: empty = every
workspace, matching the unread sweep and list_channels convention). The
handler bodies moved into scoped `*Body` methods invoked once per
workspace through `withClient`, wrapped by the same fan-out shape as
`runUnreadSummary` / `runListChannels`:

- **single workspace** (the common case, or an explicit label) →
  flat body, byte-for-byte identical to pre-1.0 output;
- **two or more** → each under a `## [label]` heading;
  `get_morning_recap` keeps its `# Morning Recap` title once above the
  labelled sections.

Channel resolution is deliberately workspace-local: each workspace runs
`resolveTargetChannels` against its own `SLACK_CHANNELS` / joined
channels, so auto-discovery and config fallback stay correct per space
rather than leaking the primary's channel list into a secondary.

## Consequences

- The read surface is now uniform: every reader is workspace-
  addressable, which is the precondition for the 1.0 contract (ADR 037).
- Zero behaviour change for single-workspace users (the overwhelming
  majority) — verified by the unchanged existing tests; new tests cover
  the routing (unknown-label error, named-label scoping).
- `get_multi_channel_digest` across N workspaces with an explicit
  `channels` list resolves those names in each workspace; a name absent
  from one surfaces as that workspace's inline `## #name error:` line,
  consistent with the tool's existing per-channel error handling.
