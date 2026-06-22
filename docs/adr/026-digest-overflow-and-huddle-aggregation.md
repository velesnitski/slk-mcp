# ADR 026 — bound digest size and aggregate huddle noise

**Status:** accepted
**Date:** 2026-06-19
**Tag at acceptance:** v0.5.2

## Context

`get_unread_summary` on a weekend backlog (55 channels, 226 top-level +
167 thread replies across two workspaces) rendered ~57k characters and
**overflowed the MCP host's result-size limit**, forcing the spill-to-file
fallback — a poor experience and a hard failure of the primary tool.

Two compounding causes:

1. **`max_chars` defaulted to 0 (unlimited).** The graceful-degradation
   machinery already existed (`budgetAppend` → "+N channels omitted"
   footer) but was off by default, so nothing bounded a large backlog.
2. **Huddle inflation (self-inflicted by ADR 025).** Surfacing huddles as
   `[huddle]` instead of hiding them as `[blocks: 1]` was correct for DMs
   (a real call is signal) but turned busy channels (#meetings, #team-alpha,
   #dev-backend standups) into a wall of content-less `[huddle]` lines.

## Decision

### A. Auto-cap max_chars (the overflow fix)

`max_chars` now has three states instead of two:

- **absent** → auto: `DefaultTotalMaxChars` (24000) split across the
  workspaces being rendered (`resolveMaxChars`). Output can no longer
  overflow by default; excess channels collapse to the omitted-footer.
- **0** → unlimited (explicit power-user escape hatch, unchanged).
- **N > 0** → hard per-workspace cap (unchanged).

The sentinel is `-1` at the tool boundary (`GetFloat("max_chars", -1)`),
which lets us distinguish "absent" from an explicit `0`.

### B. Aggregate huddle noise in channels

New `format.WithHuddleAggregation()` option: a content-less huddle (a
huddle with `ReplyCount == 0` and no attached thread replies) is counted,
not rendered, and the channel gets a single `· N huddles` line. Huddles
with a reply chain stay (the reply is content). A channel whose only
activity is huddles is dropped under `WithOmitEmpty()` (the sweep) and
shown as `## label\n· N huddles` for a direct `get_channel_digest`.

The unread sweep passes the option **only for non-DM channels**
(`slack.IsDirectMessage`), so a 1:1 huddle (the original ADR-025
motivation) still renders inline.

## Consequences

- `get_unread_summary` can't overflow the host limit by default; a big
  backlog degrades to the existing omitted-channels footer.
- Busy channels show `· N huddles` instead of N noise lines; DM calls keep
  their inline `[huddle]`.
- The default is conservative (24k total). Power users wanting the full
  firehose pass `max_chars=0`; wanting more per workspace, a positive N.
- `max_chars` semantics changed for the **absent** case (was unlimited,
  now auto-capped). Anyone relying on unbounded output must now pass
  `max_chars=0` explicitly — called out in the tool description.

## Validation

- `go vet ./...` clean; `go test -race ./...` — 461 pass.
- format: aggregates noise to `· N huddles`, excludes them from the header
  count, keeps DM huddles inline, never aggregates a huddle that has
  replies, drops huddle-only channels under omitEmpty.
- tools: `resolveMaxChars` table (auto split / single / guard / explicit-0
  / explicit-N).

## Out of scope

- Token-accurate budgeting (chars are a proxy; cheap for ASCII, dearer for
  Cyrillic). A char cap is a safe approximation; revisit if it proves
  loose.
- Bounding the References footer (small relative to the channel budget).
- Applying an auto-cap to `get_mentions` (its `limit` already bounds it).
