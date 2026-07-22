# ADR 060: canvas lookup cascade (identities + shared canvas files)

Date: 2026-07-22
Status: accepted

## Context

read_canvas (ADR 058) resolved only the channel-attached canvas tab via
`conversations.info properties.canvas` on the primary (bot) client. Live
miss: an operator pointed at a channel that visibly contained a canvas,
and the tool answered "no canvas attached". Two structural blind spots:
(1) canvas visibility on conversations.info differs between bot and
user identities; (2) a "canvas in the channel" is frequently a
STANDALONE canvas document shared into the channel as a file — that
never appears on properties.canvas at all.

## Decision

New `CanvasService` holding BOTH goslack identities, and a lookup
cascade in the tool:

1. `ChannelCanvas` — conversations.info on the primary identity, then
   the user identity when distinct. First hit wins.
2. `CanvasFiles` — `files.list types=canvas` for the channel, user
   identity first (canvas files ride user-level visibility), primary as
   fallback. The tool picks the newest by Created (`pickNewestCanvas`,
   pure) and renders it through the same files.info → download →
   canvasToText path.

Wired as a narrow `CanvasClient` contract with the standard compile-time
assertion. Error message now distinguishes "no canvas tab AND no shared
canvas files" from transport failures.

## Consequences

- Both real-world shapes of "canvas in a channel" resolve; token-
  visibility differences no longer produce false "no canvas".
- Cost: at most two conversations.info + one files.list per call.
- 584 → 585 tests. Minor release (1.17.0).
