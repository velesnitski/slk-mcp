# ADR 055: combined per-workspace delta cursor for get_unread_summary

Date: 2026-07-20
Status: accepted

## Context

Multi-workspace pulls emitted one `cursor:` line **per workspace**, but
the `after` argument accepted only a single timestamp applied to every
workspace. The operator's delta loop therefore had to take the **min**
of the per-workspace cursors by hand — an extra manual step on the most
frequent call in the workflow, and an accuracy loss: the faster
workspace re-showed everything between min(cursors) and its own cursor
on every delta pull.

## Decision

One combined, round-trippable cursor token.

- Multi-workspace output now ends with a single trailing line:
  `cursor: primary=<ts>;secondary=<ts> (pass as after= …)`.
- `after` accepts both shapes: the combined `ws=ts;ws2=ts2` token
  (exact per-workspace filtering, keys case-insensitive, `,` tolerated,
  malformed pairs skipped) and the historical plain timestamp (applied
  to every workspace) — fully backward compatible.
- Plumbing: `buildUnreadSummary` returns its workspace's newest ts
  alongside the body; `runUnreadSummary` parses `after` once
  (`parseAfterCursor`), applies each workspace's own cursor
  (`cursorForWorkspace`), and renders the trailing token
  (`combinedCursor`) in registry order (primary first, deterministic).
- A caught-up or errored workspace **carries its incoming cursor
  forward** — a combined cursor never regresses, so quiet workspaces
  don't cause re-emission later.

## Consequences

- The delta loop becomes copy-paste exact: feed back the one trailing
  token; no min-of-cursors arithmetic, no duplicate re-shown messages in
  the faster workspace.
- Per-workspace `cursor:` lines inside each section remain (unchanged
  single-workspace behaviour; the combined line only appears in
  multi-workspace output).
- Helpers are pure and unit-tested: plain/combined parsing,
  case-insensitivity, malformed-pair tolerance, render order, and the
  render→parse round-trip.
