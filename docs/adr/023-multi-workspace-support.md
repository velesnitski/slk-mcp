# ADR 023 — multi-workspace support

**Status:** accepted
**Date:** 2026-06-11
**Tag at acceptance:** v0.5.0

## Context

slk-mcp was single-workspace by construction: one `SLACK_TOKEN` /
`SLACK_USER_TOKEN` pair, one `goslack.Client`, one set of tools. Slack
tokens are workspace-scoped — a token minted in workspace A cannot read
workspace B — so a second Slack space was simply invisible. The daily
unread/mentions digest, the main reason this server exists, could only
ever cover the workspace whose token was configured.

We want one server to merge several workspaces, with two firm
constraints:

1. **No product-specific environment-variable keys.** Adding a workspace
   must not require an env var named after it. Labels are operational
   detail, not configuration schema.
2. **Zero regression for the single-workspace path**, which is every
   existing deployment and every existing test.

## Decision

### Config — names in values, never keys

A new optional `SLACK_WORKSPACES` holds a JSON array; each entry is
`{name, bot_token, user_token, channels}`. The label is a JSON **value**,
so the env *key* is always the generic `SLACK_WORKSPACES` regardless of
how many workspaces or what they're called.

`Load` assembles an ordered `Config.Workspaces`: the legacy
`SLACK_TOKEN`/`SLACK_USER_TOKEN` pair becomes `Workspaces[0]` (the
primary, labelled by the optional `SLACK_WORKSPACE_NAME`, default
`primary`), followed by the `SLACK_WORKSPACES` entries. The primary's
tokens are mirrored back onto the legacy `BotToken`/`UserToken`/`Channels`
scalar fields, so `PrimaryToken()`, `HasBotToken()`, and the primary
`slack.Client` keep reading `Workspaces[0]` with no change. A
`SLACK_WORKSPACES`-only config (no legacy token) still works: its first
entry becomes the primary.

`WorkspaceViews()` derives one `*Config` per workspace — token/channel
fields scoped to that workspace, every global scalar (read-only, disabled
tools, digest hours, …) shared with the parent. A config with no
`Workspaces` (e.g. a hand-built test config) yields a single `primary`
view backed by itself, so nothing downstream special-cases the count.

### Client — a registry, primary first

`slack.NewRegistry(cfg)` builds one `*Client` per view, preserving order;
`registry[0]` is the primary. One client per workspace is the only
correct model — separate credentials mean separate HTTP/auth state.

### Tools — shallow-copy Hub, zero blast radius

The `Hub` gains a `registry []slack.Workspace`, but `h.client` / `h.cfg`
**stay pointed at the primary**. Every existing handler that reads
`h.client` or the `h.Channels()`/`h.Unread()`/… accessors therefore keeps
targeting the primary, untouched.

To retarget a workspace, `withClient(c)` returns a *shallow copy* of the
Hub with `client` (and `cfg`, taken from the client) swapped. Because
every accessor and helper reads those two fields, one swap retargets the
entire handler surface — no per-call plumbing, no threading a workspace
handle through dozens of signatures. The copy shares `log` and
`registry`, mutates nothing, and is safe to use concurrently.

`get_unread_summary` and `get_mentions` were refactored so their
per-workspace body is a method (`buildUnreadSummary` / `buildMentions`)
returning a string; a thin runner loops the registry, calls each via
`withClient`, and composes. One workspace renders exactly as before (same
title, same body). Two or more render each under a `## [label]` heading
with a `# … — N workspaces` header. Both tools accept an optional
`workspace` argument to scope to one label.

### What stays single-workspace

Drill-in tools (`get_channel_digest`, `post_message`, `mark_read`,
`search_messages`, `list_channels`, …) operate on the primary. Reads are
safe to scope later via the same `withClient` seam; **writes**
(`post_message`, `add_reaction`, `mark_read`) deliberately stay
primary-only for now — routing a write to the wrong workspace is the
expensive mistake, and "default to primary" is the safe default. A
per-tool `workspace` param for these is a follow-up.

## Consequences

- A second (third, …) Slack space now merges into the daily digest with
  no new env keys and no rename of existing ones.
- Single-workspace deployments are byte-for-byte unaffected: the registry
  collapses to one element and the output path is unchanged.
- `withClient` is a reusable seam — extending any read tool to be
  workspace-aware later is a localised change, not a refactor.
- `max_chars` is applied per workspace when several are merged; a global
  cap across workspaces is not implemented (noted, not needed yet).

## Validation

- `go vet ./...` clean; `go test -race ./...` — 453 pass.
- Config: `ParseWorkspaces` (blank/invalid/values-not-keys), `Load`
  (single back-compat, legacy+JSON, JSON-only, invalid-JSON→Validate),
  `Validate` (token-less workspace), `WorkspaceViews` (shares globals /
  scopes tokens / empty-fallback).
- Hub: registry size, primary = `registry[0]`, empty-registry panic,
  `workspaceTargets` (all / named / case-insensitive / unknown→nil),
  `withClient` retargets without mutating the parent, unknown-workspace
  runner error path (no network).

## Out of scope

- Per-tool `workspace` routing for drill-in reads and writes.
- Per-workspace concurrency in the digest loop (sequential today;
  workspace count is small).
- Cross-workspace identity correlation (same human in two spaces).
