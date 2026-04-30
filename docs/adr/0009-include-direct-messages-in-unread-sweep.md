# ADR 0009: include direct messages in `get_unread_summary`

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0001 (`get_unread_summary` correctness baseline).

## Context

`UnreadService.JoinedChannels` requested only `public_channel` and
`private_channel` types from `users.conversations`. As a result, every
direct message and group DM the operator participated in was silently
dropped from the unread sweep.

Symptom observed in real workspace use: `get_unread_summary` returned
"all caught up — 0 unread" while `get_mentions` showed 50 hits in the
last 72 hours, the bulk of them in DMs (channel IDs of the form
`U…` from `to:me` search). The operator had no way to triage DM
threads through the digest path.

## Decision

Two coupled changes:

1. **Expand the conversation-types list** in `JoinedChannels` to
   `["public_channel", "private_channel", "mpim", "im"]`. Every
   conversation the operator sees in their Slack sidebar now flows
   through the unread sweep.
2. **Channel-label rendering API.** Both `format.ChannelDigest` and
   `format.LogChannelDigest` previously hardcoded a `#` prefix in the
   header (`## #channelname`). DMs have no usable name (Slack returns
   an empty `Channel.Name` for IMs); group DMs have a synthetic
   `mpdm-a--b--c-1` name. The format functions now take a
   `channelLabel` string used verbatim — callers are responsible for
   the `#`/`@`/`mpdm-` prefix.
   - `tools.channelDisplayLabel(ctx, ch, users)` resolves the right
     label per channel kind:
     - `IsIM` → `"@" + users.Name(ch.User)`, falling back to `"@?"`
       when the peer ID is missing.
     - `IsMpIM` → the raw `Channel.Name` (already a synthetic
       `mpdm-…`), falling back to `"mpdm-?"`.
     - regular channel → `"#" + ch.Name`, falling back to `"#?"`.
   - `tools/digest.go` (recap + multi-channel digest) prepends `#`
     since those callers only receive channel names from
     configuration / arguments, never DMs.

## Why the API break is fine

The format-package types are not in any external user's program — slk-mcp
is a CLI binary, not a library. The only consumers of `ChannelDigest`
and `LogChannelDigest` are the internal tools handlers, all of which
land in this same change. No external compatibility surface to maintain.

## Consequences

- DMs and group DMs now appear in `get_unread_summary` output. For an
  operator triaging a backlog, this typically doubles or triples the
  channel count.
- The IM branch performs one `users.info` lookup per distinct DM peer
  (cached after first hit). For workspaces with many active DMs the
  initial digest pays a brief warm-up cost; subsequent calls are
  cache-hot.
- Test coverage: `internal/tools/unread_dm_test.go` adds 6 cases
  covering each branch of `channelDisplayLabel` (regular channel,
  empty-name channel, mpim with name, mpim without name, im without
  user, im with user). The IM-with-user case asserts on the `@` prefix
  rather than the resolved username so it doesn't depend on a working
  Slack API endpoint.
- Output schema change: digest headers no longer include a hardcoded
  `#`. LLM consumers that pattern-match on `## #` need to relax to
  `## ` followed by the label. Documented in the CHANGELOG.
