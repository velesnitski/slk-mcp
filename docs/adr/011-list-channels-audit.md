# ADR 011 — Channel audit on `list_channels`

**Status:** accepted
**Date:** 2026-05-26
**Tag at acceptance:** v0.4.9

## Context

A workspace owner asked the MCP for "all channels I haven't joined"
as a routine governance task — stale channels, duplicated topics,
groups they should leave or join. The previous `list_channels`
output exposed channel name and member count only; whether the
operator was already a member was invisible. No filter for the
"not joined" subset either.

The underlying Slack response (`conversations.list` via xoxp) does
carry the relevant fields — `IsMember`, `IsPrivate`, `Purpose` —
the renderer just wasn't surfacing them.

### Options considered

- **a.** Always render every available field per channel (member
  list, created date, creator, purpose, topic, archived state).
  Heavy; an audit caller wants a scannable list, not a wall of
  metadata per row.
- **b.** Add a `unjoined_only: bool` parameter and quietly drop
  membership info on the regular path. Solves the filter case but
  leaves the unfiltered call still blind.
- **c.** Add `unjoined_only: bool` AND surface markers on every
  line — `[NOT JOINED]` for non-members, `🔒` for private. Joined
  public channels stay quiet so the common case isn't noisy. Topic
  → Purpose fallback so a channel without a topic still carries
  description text.

## Decision

Use **(c)**. The contract change:

1. New optional `unjoined_only: bool` (default `false`).
2. Per-line markers in `renderChannelLine(ch)`:
   - `🔒` glyph immediately after `#name` for private channels.
   - `[NOT JOINED]` suffix when `IsMember == false`.
   - Joined public channels render with no markers — silent.
3. Context falls back: `Topic.Value` first, `Purpose.Value` only if
   topic is empty. Both trimmed; longest 80 chars then `...`.
4. The previous handler's behaviour for any joined-public channel
   with a topic is unchanged (same `- #name (N) topic` shape) so
   existing scripted consumers stay working.

`filterUnjoined(channels, unjoinedOnly)` is a small in-place
filter — when the flag is off it returns the slice unchanged so the
happy path is allocation-free. The handler header notes the filter
state explicitly (`"42 channels (operator is not a member)"`) so
the caller can grep that line for context.

## Consequences

- **Workspace audit is one call.**
  `list_channels(unjoined_only=true)` returns exactly the channels
  the operator should consider joining or archiving.
- **No new Slack API call.** The fields already come back from
  `conversations.list`; the previous renderer dropped them. Zero
  rate-limit impact.
- **Marker discipline.** `[NOT JOINED]` and `🔒` show only when
  meaningful — a default-joined-public channel rendering is
  unchanged from v0.4.8. Reviewers grepping log lines for the
  markers get a clean signal.
- **Purpose fallback preserves the "what is this channel" answer**
  for channels with no topic. Many service / log channels never set
  a topic but document themselves via purpose.
- **Private channel visibility unchanged.** Only private channels
  the operator is already a member of can be listed (Slack scope
  limit). Truly hidden private channels are still invisible — no
  fix possible from the client side.

## Validation

- `TestFilterUnjoined_*` (2 cases) — off path passes through, on
  path keeps only non-members.
- `TestRenderChannelLine_silentForJoinedPublic` — happy path
  unchanged from v0.4.8 output.
- `TestRenderChannelLine_markersForUnjoinedPrivate` — both markers
  appear together in correct order.
- `TestRenderChannelLine_fallsBackFromTopicToPurpose` — context
  fallback works.
- `TestRenderChannelLine_truncatesLongContext` — 80-char cap holds.
- `TestRenderChannelLine_emptyTopicAndPurposeNoTrailingSpace` —
  empty-context line has no trailing whitespace (clean for grep /
  pipe).
- Full suite: 373 → 381 (+8) green.

## Out of scope

- Channel creator / created date on the list view. Available via
  `get_channel_info` for the rare case the caller needs them.
- Membership *of other users* in arbitrary channels. Out of scope
  for `list_channels`; `get_channel_info` with `include_members`
  already handles that.
- Joining a channel from the MCP. Audit is read-only; a follow-up
  could add a `join_channel` tool if the workflow needs it.
