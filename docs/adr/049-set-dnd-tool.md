# ADR 049: set_dnd — pause/resume notifications (Do Not Disturb)

Date: 2026-07-15
Status: accepted

## Context

`set_status` and `set_presence` can display "do not disturb" and flip the
grey dot, but neither actually **silences** notifications — that is
Slack's Do Not Disturb (snooze), a separate surface (`dnd.setSnooze` /
`dnd.endSnooze`). So "pause my notifications for 30 minutes on both
workspaces" had no single-call path; the operator had to toggle DND by
hand in each Slack client.

## Decision

Add a `set_dnd` tool, built on the same personal-action pattern as
status/presence.

- New `DNDService` (internal/slack/dnd.go), built on the **user** client
  (nil for a bot-only workspace): `Snooze(minutes)` →
  `SetSnoozeContext`, `EndSnooze()` → `EndSnoozeContext`, `Enabled()`.
  Every method guards on the nil user client and returns
  `ErrNoUserTokenDND` so a bot-only workspace fails loudly.
- `set_dnd(minutes, resume, workspace)`: `minutes>0` snoozes;
  `minutes<=0` or `resume=true` ends the snooze. Like status/presence,
  DND is a property of YOU — an empty `workspace` applies to **every**
  configured workspace (you-global), iterating with per-workspace
  skip/error lines.
- `DNDClient` contract + compile-time assertion; `Hub.DND()` accessor.
  Registered only when not read-only and at least one workspace has a
  user token. `dndErrorHint` maps `missing_scope` → add **dnd:write**
  (mirrors `statusErrorHint`).

## Consequences

- "pause notifications for 30m on both workspaces" is now one call:
  `set_dnd(minutes=30)`.
- Requires the **dnd:write** user scope; on a token without it the tool
  returns the scope-fix hint rather than a bare `missing_scope`.
- Mirrors the status/presence surface exactly (user-token gating,
  you-global default, per-workspace result lines), so nothing new to
  learn. New tool → minor release (1.8.0). Tool count 22 → 23.
- Pure additive; no change to existing tools. Snooze/EndSnooze guard
  logic and the scope hint are unit-tested (nil-token behaviour,
  unknown-workspace, no-user-token, missing_scope mapping). 550 → 554
  tests.
