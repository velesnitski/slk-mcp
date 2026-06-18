# ADR 025 — render huddle events instead of hiding them as "[blocks: 1]"

**Status:** accepted
**Date:** 2026-06-18
**Tag at acceptance:** v0.5.1

## Context

A 23-minute huddle between the operator and a teammate happened in a DM,
but the digest reported the opposite ("call never connected"). Root cause:
a Slack **huddle** is a real-time audio session, not a text message.
Slack delivers it as a block-kit message with **empty text** and subtype
`huddle_thread`. The renderer's empty-body fallback
(`renderHiddenPayloadMarker`) collapsed it to a meaningless `[blocks: 1]`,
indistinguishable from any other structured message — so the digest
author couldn't see a call had occurred and inferred from older text that
it hadn't.

This is a representation gap, not a crash: the event reached the digest
but in a form that hid its meaning.

## Decision

Detect huddles by subtype and surface them:

- `format.IsHuddle(msg)` ⟺ `msg.SubType == "huddle_thread"`
  (`HuddleSubtype` constant — slack-go has none).
- `renderHiddenPayloadMarker` returns `"[huddle]"` for a huddle, taking
  precedence over the generic `[attached: N]` / `[blocks: N]` markers.
- `HasContent` treats a huddle as content unconditionally, so a huddle
  with neither text nor blocks is never filtered out of the sweep.

### What we deliberately do NOT do

Render duration or participants. slack-go v0.15.0's typed `Message` drops
the `room` object (no `Room`/`Call` field on `Msg`), so `date_start`/
`date_end`/`participants` are gone by the time the data reaches our code.
Recovering them would require re-unmarshalling raw history JSON in the
client layer or patching slack-go — a much larger change. `[huddle]` is
the honest, low-risk win; `[huddle • Nm • @who]` is a documented
follow-up gated on raw-room access.

## Consequences

- Huddles are visible in `get_unread_summary` / digests as `[huddle]`
  instead of `[blocks: 1]`; the renderer no longer hides a class of
  real-time events (the same path will surface any future
  empty-text/huddle-style event we tag).
- Digest narration must stop inferring "no call" from the absence of
  text — a `[huddle]` line (or, pre-fix, an opaque `[blocks: 1]` near a
  call context) can mean a call happened.
- No duration yet — readers see that a huddle occurred, not how long.

## Validation

- `TestRenderHiddenPayloadMarker_huddleBeatsBlocks`,
  `TestMessageLine_rendersHuddle`, `TestHasContent_huddleWithoutBlocks`.
- `go test ./...` green; `go vet ./...` clean.

## Out of scope

- Huddle duration / participant list (needs the raw `room` object).
- Distinguishing huddle *start* vs *end* events.
