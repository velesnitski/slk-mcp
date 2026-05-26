# ADR 013 — `archive_channel` and `unarchive_channel`

**Status:** accepted
**Date:** 2026-05-26
**Tag at acceptance:** v0.4.11

## Context

v0.4.9 added the workspace audit primitive — `list_channels(
unjoined_only=true)` returns the channels the operator hasn't
joined, including the orphan rooms with 0–2 members that
accumulate in any long-running workspace. A real audit produces
two follow-up needs:

1. **Join** a useful channel — Slack UI is fine here.
2. **Archive** a dead channel — also Slack UI, but the operator
   has just identified 5+ candidates in one MCP call. Switching
   contexts to the UI per channel breaks the cleanup flow.

The MCP had no write-side coverage for channel lifecycle, only for
messages (`post_message`, `add_reaction`, `mark_read`). Filling the
gap closes the audit-to-action loop in one tool surface.

### Options considered

- **a.** Add only `archive_channel`. Tight scope; matches the
  audit use case.
- **b.** Add a single `manage_channel(action: "archive" | "unarchive")`
  tool. One name, mode flag. Compact but reads worse at call
  sites and hides intent in a string.
- **c.** Add **both** `archive_channel` and `unarchive_channel` as
  separate tools. Symmetric pair; either is a tiny handler over
  the same `ChannelService` method. Lets a caller recover from an
  accidental archive without leaving the MCP. Clearest at call
  sites.

## Decision

Use **(c)**. Concretely:

1. `ChannelService.Archive(ctx, channelID)` and
   `ChannelService.Unarchive(ctx, channelID)` wrap the
   `conversations.archive` / `conversations.unarchive` Slack API
   via the existing `ratelimit.Do` wrapper. The methods take a
   channel ID and don't resolve internally — that's the handler's
   job.
2. Two MCP tools `archive_channel` and `unarchive_channel`, both
   gated by `!h.cfg.ReadOnly && !h.cfg.IsDisabled(name)`. The
   gate matches `post_message` / `add_reaction` / `mark_read`.
3. Both handlers resolve the input via `h.Channels().ResolveID`,
   which already short-circuits on canonical channel IDs from
   v0.4.6. A caller pasting `C0ABC1234DE` straight from
   `list_channels` output works without a separate lookup.
4. `ChannelClient` interface gains `Archive` and `Unarchive`,
   matching the v0.4.7 / v0.4.8 pattern of growing contracts in
   one place. Compile-time assertion enforces
   `*slack.ChannelService` satisfies it.

Permissions: the user token needs `channels:manage` for public
channels and `groups:write` for private ones. Documented in the
tool descriptions and the service method's GoDoc. If the operator
has only a bot token, the tool registers but the API call will
return a Slack scope error — which propagates verbatim.

## Consequences

- **Closed audit-to-action loop.** Operator can identify orphan
  channels in one call (`list_channels(unjoined_only=true)`) and
  archive them in another (`archive_channel`) without context-
  switching to the UI.
- **Read-only deployments unaffected.** Both tools are gated by
  `SLACK_READ_ONLY`. A workspace running the MCP in read-only
  mode never sees them — same posture as the message-write tools.
- **Reversible operation.** Archive is not a permanent delete;
  Slack restores via unarchive. The MCP exposes both so an
  accidental archive is recoverable through the same surface.
  True permanent deletion remains a workspace-owner-only Slack UI
  action.
- **Surface growth: 2 new tools, 2 new contract methods.** The
  ChannelClient interface continues to enforce narrow consumer
  contracts at the tools↔slack boundary.
- **No bot-token degradation.** `conversations.archive` /
  `unarchive` require user-token scopes; a bot-token-only
  deployment gets a clear Slack error rather than a silent
  no-op.

## Validation

- `TestArchive_callsConversationsArchive` /
  `TestUnarchive_callsConversationsUnarchive` — pin the API call
  shape (correct method name, channel ID in form data).
- `TestArchive_propagatesSlackError` /
  `TestUnarchive_propagatesSlackError` — Slack error replies
  (`already_archived`, `not_archived`) surface verbatim through
  the wrapper.
- The compile-time assertion `var _ ChannelClient =
  (*slack.ChannelService)(nil)` in `contracts.go` confirms the
  broadened interface is satisfied.
- Full suite: 381 → 385 green (+4).

## Out of scope

- **True permanent deletion.** Slack only exposes that via the
  workspace-owner UI (not the API) for standard workspaces, or
  Enterprise Grid's `admin.conversations.delete`. The MCP cannot
  paper over a missing API; the tool description spells out the
  reversibility so a caller doesn't expect total removal.
- **Bulk archive.** A workspace-cleanup workflow could
  conceivably take a list of IDs and archive them in one call.
  Deferred — one-at-a-time keeps the tool simple and lets the
  caller stage decisions; bulk wrapping is a 3-line script on the
  caller side if needed.
- **Audit-trail / who-archived.** Slack records the operator on
  its side; the MCP doesn't need to.
- **Confirmation prompt.** Archive is reversible, so we don't
  block on a confirm step — keeping with `post_message` /
  `add_reaction` semantics. If a future caller wants safety, the
  pattern is `IsDisabled(archive_channel)` in the env config.
