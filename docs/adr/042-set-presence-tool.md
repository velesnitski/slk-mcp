# ADR 042: `set_presence` — flip the dot without touching status

Date: 2026-07-09
Status: accepted

## Context

`set_status` (ADR 039) can set presence alongside a custom status, which
is right for the AFK-in-one-call case ("AFK till tomorrow" + away). But
it conflates two things: empty status text *clears* the status. So there
was no way to say "go away but keep my current status" — flipping
presence to away while a "lunch" status was up required re-issuing the
whole status (text + emoji + a recomputed expiry) just to avoid wiping
it. That surfaced in real use: setting lunch, then wanting the grey dot,
meant re-sending lunch with the remaining minutes recomputed by hand.

## Decision

Add `set_presence` (`away` bool, default true; `workspace`, you-global
like `set_status`) that calls only `users.setPresence` and never touches
the custom status. `set_status` keeps its combined path for the AFK case;
`set_presence` is the standalone dot-flip.

Yes, presence is now reachable from two tools — but each has a clear
job: `set_status` = "change my status (and maybe the dot)",
`set_presence` = "just flip the dot, leave my status alone." The
alternative (a `keep_status` flag on `set_status`) made the empty-text
semantics ambiguous; a second single-responsibility tool reads better
and the model picks the right one from the descriptions.

Registration and gating are shared with `set_status` (user token
required, skipped under `SLACK_READ_ONLY`); `set_presence` has its own
`DISABLED_TOOLS` entry. Errors reuse `statusErrorHint`, so a
missing-scope failure points at `users:write` the same way.

## Consequences

- "presence away" / "set me back to active" is one call that preserves
  whatever status (and expiry) is already up.
- No breaking change: `set_status`'s presence args are untouched, so the
  one-call AFK flow still works. Additive tool ⇒ minor release (1.4.0).
- 524 → 525 tests.
